package main

import (
	"fmt"
	"slices"
	"strings"
)

// Presentation: turns collected facts into text. No OS access here.

func fmtDuration(sec float64) string {
	if sec < 0 {
		return "unknown"
	}
	s := int(sec)
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m, s := s/60, s%60
	if m < 60 {
		return fmt.Sprintf("%dm %02ds", m, s)
	}
	h, m := m/60, m%60
	if h < 24 {
		return fmt.Sprintf("%dh %02dm", h, m)
	}
	return fmt.Sprintf("%dd %dh", h/24, h%24)
}

func bindLabel(addrs []string) string {
	left := map[string]bool{}
	for _, a := range addrs {
		left[a] = true
	}
	var out []string
	if left["0.0.0.0"] && left["::"] {
		out = append(out, "*")
		delete(left, "0.0.0.0")
		delete(left, "::")
	}
	if left["127.0.0.1"] && left["::1"] {
		out = append(out, "localhost")
		delete(left, "127.0.0.1")
		delete(left, "::1")
	}
	var rest []string
	for a := range left {
		rest = append(rest, a)
	}
	slices.Sort(rest)
	out = append(out, rest...)
	if len(out) > 1 { // full list: portpeek PORT
		return fmt.Sprintf("%s +%d", out[0], len(out)-1)
	}
	return out[0]
}

func hostPort(addr string, port int) string {
	if strings.Contains(addr, ":") {
		return fmt.Sprintf("[%s]:%d", addr, port)
	}
	return fmt.Sprintf("%s:%d", addr, port)
}

func pidLabel(r *Listener) string {
	switch len(r.PIDs) {
	case 0:
		return "?"
	case 1:
		return fmt.Sprint(r.PIDs[0])
	}
	return fmt.Sprintf("%d +%d", r.PIDs[0], len(r.PIDs)-1)
}

func scopeOf(r *Listener) string {
	if r.localOnly() {
		return "local"
	}
	return "open"
}

func firstProc(r *Listener, procs map[int]*Proc) *Proc {
	if len(r.PIDs) == 0 {
		return nil
	}
	return procs[r.PIDs[0]]
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// describe returns "name (pid N, user)" and "tcp 127.0.0.1:3000, ..." for a pid.
func describe(pid int, rows []*Listener, procs map[int]*Proc, port int) (who, where string) {
	seen := map[string]bool{}
	var places []string
	for _, r := range rows {
		if !slices.Contains(r.PIDs, pid) {
			continue
		}
		for _, a := range r.Addrs {
			s := r.Proto + " " + hostPort(a, port)
			if !seen[s] {
				seen[s] = true
				places = append(places, s)
			}
		}
	}
	slices.Sort(places)
	who = fmt.Sprintf("pid %d", pid)
	if p := procs[pid]; p != nil {
		who = fmt.Sprintf("%s (pid %d, %s)", p.Name, pid, p.User)
	}
	return who, strings.Join(places, ", ")
}

type listenerJSON struct {
	Proto         string   `json:"proto"`
	Port          int      `json:"port"`
	Bind          []string `json:"bind"`
	Scope         string   `json:"scope"`
	PIDs          []int    `json:"pids"`
	Process       *string  `json:"process"`
	Command       *string  `json:"command"`
	User          *string  `json:"user"`
	Cwd           *string  `json:"cwd"`
	UptimeSeconds *int     `json:"uptime_seconds"`
}

func toJSON(r *Listener, procs map[int]*Proc) listenerJSON {
	j := listenerJSON{Proto: r.Proto, Port: r.Port, Bind: r.Addrs, Scope: scopeOf(r), PIDs: r.PIDs}
	if p := firstProc(r, procs); p != nil {
		j.Process, j.Command, j.User = &p.Name, &p.Cmd, &p.User
		if p.Cwd != "" {
			j.Cwd = &p.Cwd
		}
		if p.Uptime >= 0 {
			u := int(p.Uptime)
			j.UptimeSeconds = &u
		}
	}
	return j
}
