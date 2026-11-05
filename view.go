package main

import (
	"fmt"
	"strings"
)

// The human-facing views: list and inspect. Pure text out, no OS access.

// header is the two-line heading used by inspect.
func (t *Term) header(title, subtitle string) []string {
	return []string{"", "  " + t.amber("portpeek") + " " + t.dim(t.arrow) + " " + title, "  " + subtitle, ""}
}

// listHeader is the banner (or its one-line fallback) plus a summary line.
func (t *Term) listHeader(summary string) []string {
	out := append([]string{""}, t.banner()...)
	if len(out) == 2 { // no room or no terminal: summary beside the name
		out[1] += "  " + t.dim(t.dot) + "  " + summary
		return append(out, "")
	}
	return append(out, "", "  "+summary, "")
}

func (t *Term) arrowLine(cmd, note string) string {
	room := t.width - 26 // indent, arrow, padded command
	if room < 8 {
		return "  " + t.amber(t.arrow) + " " + t.bold(cmd)
	}
	return "  " + t.amber(t.arrow) + " " + t.bold(t.fit(cmd, 22, true)) + t.dim(t.fit(note, room, false))
}

func (t *Term) rpad(s string, w int) string { return strings.Repeat(" ", max(0, w-cells(s))) + s }

type item struct {
	port, name, pid, cmd, cwd, up string
	hidden, open                  bool
}

func newItem(r *Listener, procs map[int]*Proc, t *Term) item {
	it := item{port: fmt.Sprint(r.Port), pid: pidLabel(r), open: !r.localOnly()}
	if r.Proto == "udp" {
		it.port += "/udp"
	}
	if p := firstProc(r, procs); p != nil {
		it.name, it.cwd = p.Name, niceCwd(p.Cwd, t.home)
		it.cmd = stripArgv0(p.Name, shortCmd(p.Cmd, t.home))
		it.up = fmtAge(p.Uptime)
		return it
	}
	it.hidden, it.name = true, "(hidden)"
	it.cmd = "owner hidden"
	if h, ok := portHints[r.Port]; ok {
		it.cmd = "probably " + h
	}
	return it
}

func summary(n, exposed int, t *Term, short bool) string {
	listening := fmt.Sprintf("%s listening", t.bold(fmt.Sprint(n)))
	var net string
	switch {
	case exposed == 0 && short:
		net = t.paint(sage, "none open", t.color)
	case exposed == 0:
		net = t.paint(sage, "none open to the network", t.color)
	case short:
		net = t.amber(fmt.Sprint(exposed)) + " open"
	default:
		net = t.amber(fmt.Sprint(exposed)) + " open to the network"
	}
	return listening + " " + t.dim(t.dot) + " " + net
}

func renderList(rows []*Listener, procs map[int]*Proc, hidden int, free []int, t *Term, includeUDP bool) string {
	W := min(t.width, 100) // don't sprawl on very wide terminals
	if len(rows) == 0 {
		msg := "No listening TCP ports found.  (--udp includes UDP)"
		if includeUDP {
			msg = "No listening ports found."
		}
		return strings.Join(append(t.listHeader(msg), ""), "\n")
	}

	var open, local []item
	for _, r := range rows {
		if it := newItem(r, procs, t); it.open {
			open = append(open, it)
		} else {
			local = append(local, it)
		}
	}
	all := append(append([]item{}, open...), local...)
	portW, nameW, pidW, upW, cmdW, cwdW := 0, 0, 0, 0, 0, 0
	for _, it := range all {
		portW, nameW = max(portW, cells(it.port)), max(nameW, cells(it.name))
		pidW, upW = max(pidW, cells(it.pid)), max(upW, cells(it.up))
		cmdW = max(cmdW, cells(it.cmd))
		if it.cwd != "" {
			cwdW = max(cwdW, 3+cells(it.cwd))
		}
	}
	nameW, cmdW = min(nameW, 14), min(cmdW, 56)
	showUp, showPid := upW > 0, true
	avail := func() int { // cells left for the detail column
		w := W - (2 + 1 + 2 + portW + 2 + nameW + 2)
		if showPid {
			w -= 2 + pidW
		}
		if showUp {
			w -= 2 + upW
		}
		return w
	}
	if showUp && avail() < 18 {
		showUp = false
	}
	if avail() < 14 {
		showPid = false
	}
	cwdW = min(cwdW, 30)
	if cwdW > 0 && avail()-2-cwdW < 14 { // not enough room for both: keep the command
		cwdW = 0
	}
	cmdW = min(cmdW, avail()-cond(cwdW > 0, 2+cwdW, 0))
	showDet := cmdW >= 8

	row := func(it item) string {
		mark, portStyle := t.teal(t.local), style(t.bold)
		if it.open {
			mark, portStyle = t.amber(t.open), t.amber
		}
		name := t.fit(it.name, nameW, showPid || showDet || showUp)
		if it.hidden {
			name = t.dim(name)
		}
		cols := []string{mark, portStyle(t.rpad(it.port, portW)), name}
		if showPid {
			cols = append(cols, t.dim(t.fit(it.pid, pidW, true)))
		}
		if showDet {
			pad := showUp || cwdW > 0
			w := cmdW
			if it.cwd == "" && cwdW > 0 { // no folder to show: the command may use that space
				w += 2 + cwdW
			}
			cmd := t.fit(it.cmd, w, pad)
			if it.hidden {
				cmd = t.dim(cmd)
			}
			cols = append(cols, cmd)
			if cwdW > 0 && it.cwd != "" {
				cols = append(cols, t.paint("38;5;110", t.fit("in "+it.cwd, cwdW, showUp), t.color))
			}
		}
		if showUp {
			cols = append(cols, t.dim(t.rpad(it.up, upW)))
		}
		return strings.TrimRight("  "+strings.Join(cols, "  "), " ")
	}
	section := func(label string, labelStyle style, caption string, items []item) []string {
		if len(items) == 0 {
			return nil
		}
		out := []string{"  " + labelStyle(label) + t.dim("  "+t.fit(caption, max(0, W-4-len(label)), false))}
		for _, it := range items {
			out = append(out, row(it))
		}
		return append(out, "")
	}

	sub := summary(len(rows), len(open), t, false)
	prefix := 2
	if !t.tty || t.width < 42 {
		prefix = 13 // "portpeek  .  " precedes the summary on one line
	}
	if W < prefix+cells(fmt.Sprintf("%d listening - %d open to the network", len(rows), len(open))) {
		sub = summary(len(rows), len(open), t, true)
	}
	out := t.listHeader(sub)
	out = append(out, section("OPEN TO THE NETWORK", t.amber, "other machines can reach these", open)...)
	out = append(out, section("ONLY THIS COMPUTER", func(s string) string { return t.paint("1;"+teal, s, t.color) },
		"not reachable from other machines", local)...)

	if len(free) > 0 {
		label := "FREE FOR LOCALHOST"
		nums := make([]string, len(free))
		for i, p := range free {
			nums[i] = fmt.Sprint(p)
		}
		out = append(out,
			"  "+t.paint("1;"+sage, label, t.color)+t.dim("  "+t.fit("first open port from each usual dev start", max(0, W-4-len(label)), false)),
			"    "+t.paint("1;"+sage, strings.Join(nums, "   "), t.color), "")
	}

	pick := rows[0]
	for _, r := range rows {
		if len(r.PIDs) > 0 {
			pick = r
			break
		}
	}
	out = append(out,
		t.arrowLine(fmt.Sprintf("portpeek %d", pick.Port), "see what it is"),
		t.arrowLine(fmt.Sprintf("portpeek kill %d", pick.Port), "stop it"),
		t.arrowLine("portpeek free", "print one free port (for scripts)"))
	if hidden > 0 {
		out = append(out, "", t.dim(t.fit(fmt.Sprintf("  %d %s belong to other users - run sudo portpeek to see them",
			hidden, cond(hidden == 1, "listener", "listeners")), W-2, false)))
	}
	return strings.Join(append(out, ""), "\n")
}

func cond[T any](ok bool, a, b T) T {
	if ok {
		return a
	}
	return b
}

func renderInspect(port int, rows []*Listener, procs map[int]*Proc, t *Term) string {
	label := 10
	kv := func(k, v string) string {
		return "    " + t.dim(fmt.Sprintf("%-*s", label, k)) + t.fit(v, max(8, t.width-4-label), false)
	}
	sub := fmt.Sprintf("%d listeners", len(rows))
	if len(rows) == 1 {
		sub = "something is listening"
		if p := firstProc(rows[0], procs); p != nil {
			sub = p.Name
			if age := fmtAge(p.Uptime); age != "" {
				sub += " " + t.dot + " " + age
			}
		} else {
			sub = "owner hidden"
		}
	}
	out := t.header(t.bold(fmt.Sprintf("port %d", port)), t.fit(sub, max(8, t.width-4), false))
	anyOwner := false
	for i, r := range rows {
		if i > 0 {
			out = append(out, "")
		}
		p := firstProc(r, procs)
		use := cond(r.Proto == "udp", "using UDP port", "listening on port")
		if p != nil {
			anyOwner = true
			out = append(out, fmt.Sprintf("  %s is %s %d.", t.bold(p.Name), use, port))
		} else if h, ok := portHints[port]; ok {
			out = append(out, fmt.Sprintf("  Something is %s %d (probably %s).", use, port, h))
		} else {
			out = append(out, fmt.Sprintf("  Something is %s %d.", use, port))
		}
		if r.localOnly() {
			out = append(out, "  "+t.teal("Only this computer can reach it."))
		} else {
			out = append(out, "  "+t.amber("Other machines on your network can reach it")+t.dim(" (unless a firewall blocks them)."))
		}
		if p == nil {
			out = append(out, "  "+t.dim(fmt.Sprintf("Its owner isn't visible to you. Run sudo portpeek %d to see what it is.", port)), "")
		} else {
			out = append(out, "")
			out = append(out, kv("what", p.Cmd))
			if p.Cwd != "" {
				out = append(out, kv("where", tildePath(p.Cwd, t.home)))
			}
			out = append(out, kv("who", fmt.Sprintf("%s %s pid %d", p.User, t.dot, p.PID)))
			if len(r.PIDs) > 1 {
				rest := make([]string, 0, len(r.PIDs)-1)
				for _, pid := range r.PIDs[1:] {
					rest = append(rest, fmt.Sprint(pid))
				}
				out = append(out, kv("shared", "also held by pid "+strings.Join(rest, ", ")))
			}
			out = append(out, kv("running", fmtDuration(p.Uptime)))
		}
		for j, a := range r.Addrs {
			out = append(out, kv(cond(j == 0, "address", ""), fmt.Sprintf("%s  %s", r.Proto, hostPort(a, port))))
		}
	}
	if anyOwner {
		out = append(out, "", t.arrowLine(fmt.Sprintf("portpeek kill %d", port), "stop it"))
	}
	return strings.Join(append(out, ""), "\n")
}
