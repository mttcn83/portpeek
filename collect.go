package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"net/netip"
	"os"
	"os/user"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Collection: facts from the OS, no presentation.

type Sock struct {
	Proto, Addr string
	Port        int
	Inode       uint64
}

// Listener is one row: a port held by one set of processes (v4/v6 merged).
type Listener struct {
	Proto  string
	Port   int
	Addrs  []string
	PIDs   []int // empty = owner not visible to us
	Inodes map[uint64]bool
}

func (l *Listener) localOnly() bool {
	for _, a := range l.Addrs {
		if a != "::1" && !strings.HasPrefix(a, "127.") {
			return false
		}
	}
	return true
}

type Proc struct {
	PID       int
	Name, Cmd string
	User      string
	Uptime    float64 // seconds; <0 = unknown
	Cwd       string  // "" = unknown
}

// proto, /proc table, socket state that means "accepting / bound".
var tables = []struct{ proto, path, state string }{
	{"tcp", "/proc/net/tcp", "0A"}, // TCP_LISTEN
	{"tcp", "/proc/net/tcp6", "0A"},
	{"udp", "/proc/net/udp", "07"}, // bound, unconnected
	{"udp", "/proc/net/udp6", "07"},
}

// decodeAddr turns "0100007F" into "127.0.0.1". /proc prints each 32-bit word
// in host byte order.
func decodeAddr(h string) string {
	var raw [16]byte
	for i := 0; i+8 <= len(h); i += 8 {
		w, _ := strconv.ParseUint(h[i:i+8], 16, 32)
		binary.NativeEndian.PutUint32(raw[i/2:], uint32(w))
	}
	if len(h) == 8 {
		return netip.AddrFrom4([4]byte(raw[:4])).String()
	}
	return netip.AddrFrom16(raw).Unmap().String()
}

func readSockets(includeUDP bool) []Sock {
	var out []Sock
	for _, t := range tables {
		if t.proto == "udp" && !includeUDP {
			continue
		}
		f, err := os.Open(t.path)
		if err != nil { // e.g. IPv6 disabled
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Scan() // header
		for sc.Scan() {
			p := strings.Fields(sc.Text())
			if len(p) < 10 || p[3] != t.state {
				continue
			}
			i := strings.LastIndexByte(p[1], ':')
			port, err1 := strconv.ParseUint(p[1][i+1:], 16, 16)
			inode, err2 := strconv.ParseUint(p[9], 10, 64)
			if i < 0 || err1 != nil || err2 != nil {
				continue
			}
			out = append(out, Sock{t.proto, decodeAddr(p[1][:i]), int(port), inode})
		}
		f.Close()
	}
	return out
}

// socketInodes lists socket inodes a process holds open. Nil if it exited or
// isn't ours to read.
func socketInodes(pid int) []uint64 {
	base := fmt.Sprintf("/proc/%d/fd", pid)
	d, err := os.Open(base)
	if err != nil {
		return nil
	}
	defer d.Close()
	names, _ := d.Readdirnames(-1)
	var out []uint64
	for _, fd := range names {
		link, err := os.Readlink(base + "/" + fd)
		if err != nil || !strings.HasPrefix(link, "socket:[") {
			continue
		}
		if n, err := strconv.ParseUint(link[8:len(link)-1], 10, 64); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func mapOwners(inodes map[uint64]bool) map[uint64][]int {
	owners := map[uint64][]int{}
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		for _, inode := range socketInodes(pid) {
			if !inodes[inode] {
				continue
			}
			if o := owners[inode]; len(o) == 0 || o[len(o)-1] != pid {
				owners[inode] = append(o, pid)
			}
		}
	}
	return owners
}

func collect(includeUDP bool) []*Listener {
	socks := readSockets(includeUDP)
	want := map[uint64]bool{}
	for _, s := range socks {
		want[s.Inode] = true
	}
	owners := mapOwners(want)
	sort.Slice(socks, func(i, j int) bool {
		a, b := socks[i], socks[j]
		if a.Port != b.Port {
			return a.Port < b.Port
		}
		if a.Proto != b.Proto {
			return a.Proto < b.Proto
		}
		return a.Addr < b.Addr
	})
	groups := map[string]*Listener{}
	var rows []*Listener
	for _, s := range socks {
		pids := append([]int(nil), owners[s.Inode]...)
		sort.Ints(pids)
		key := fmt.Sprint(s.Proto, s.Port, pids)
		l := groups[key]
		if l == nil {
			l = &Listener{Proto: s.Proto, Port: s.Port, PIDs: pids, Inodes: map[uint64]bool{}}
			if l.PIDs == nil {
				l.PIDs = []int{}
			}
			groups[key] = l
			rows = append(rows, l)
		}
		dup := false
		for _, a := range l.Addrs {
			dup = dup || a == s.Addr
		}
		if !dup {
			l.Addrs = append(l.Addrs, s.Addr)
		}
		l.Inodes[s.Inode] = true
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Port != b.Port {
			return a.Port < b.Port
		}
		if a.Proto != b.Proto {
			return a.Proto < b.Proto
		}
		return slices.Compare(a.PIDs, b.PIDs) < 0
	})
	return rows
}

// clean: process names and argv are attacker-controlled; never pass escapes through.
func clean(s string) string {
	s = strings.ToValidUTF8(s, "�")
	return strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return '?'
	}, s)
}

// elapsed seconds since the process started. USER_HZ is 100 on every Linux
// architecture, so no sysconf is needed.
func elapsed(pid int) float64 {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return -1
	}
	s := string(b)
	f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
	ub, err2 := os.ReadFile("/proc/uptime")
	if len(f) < 20 || err2 != nil {
		return -1
	}
	ticks, err3 := strconv.ParseFloat(f[19], 64)
	up, err4 := strconv.ParseFloat(strings.Fields(string(ub))[0], 64)
	if err3 != nil || err4 != nil {
		return -1
	}
	return max(0, up-ticks/100)
}

func readProc(pid int) *Proc {
	dir := fmt.Sprintf("/proc/%d/", pid)
	comm, err := os.ReadFile(dir + "comm")
	if err != nil {
		return nil // exited while we looked
	}
	cmdline, _ := os.ReadFile(dir + "cmdline")
	status, err := os.ReadFile(dir + "status")
	if err != nil {
		return nil
	}
	uid := ""
	for _, line := range strings.Split(string(status), "\n") {
		if f := strings.Fields(line); len(f) >= 3 && f[0] == "Uid:" {
			uid = f[2] // effective
		}
	}
	name := strings.TrimSpace(string(comm))
	var argv []string
	for _, a := range strings.Split(string(cmdline), "\x00") {
		if a != "" {
			argv = append(argv, a)
		}
	}
	cmd := "[" + name + "]" // kernel threads have no argv
	if len(argv) > 0 {
		cmd = strings.Join(argv, " ")
	}
	who := uid
	if u, err := user.LookupId(uid); err == nil {
		who = u.Username
	}
	cwd, _ := os.Readlink(dir + "cwd")
	return &Proc{pid, clean(name), clean(cmd), clean(who), elapsed(pid), clean(cwd)}
}

func loadProcs(rows []*Listener) map[int]*Proc {
	procs := map[int]*Proc{}
	for _, r := range rows {
		for _, pid := range r.PIDs {
			if procs[pid] == nil {
				if p := readProc(pid); p != nil {
					procs[pid] = p
				}
			}
		}
	}
	return procs
}
