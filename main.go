//go:build linux

// portpeek - see what's using your ports, and stop it.
//
// Linux only (reads /proc, uses pidfds: kernel 5.3+). Standard library only.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var version = "1.0.0" // overridden by -ldflags "-X main.version=..."

const (
	exitOK    = 0
	exitFail  = 1 // nothing found, or kill failed
	exitUsage = 2
	exitPerm  = 3
)

const usageText = `usage: portpeek [options] [list]
       portpeek [options] PORT
       portpeek [options] kill PORT
       portpeek [options] free [PORT]
       portpeek [options] wait PORT [--free] [--timeout DURATION]
`

const helpText = usageText + `
See what's using your ports, stop it, or find a free one.

commands:
  (none), list   what's listening, grouped by who can reach it
  PORT           who has it: command, folder, uptime, reach
  kill PORT      stop the process holding it (asks first)
  free [PORT]    print the first port >= PORT (default 3000) that is free
                 for localhost, handy in scripts: vite --port $(portpeek free)

  wait PORT      block until something listens on PORT (--free: until it is
                 released). Exit 0 when reached, 1 on timeout.
                 e.g. portpeek wait 5432 && ./migrate

options:
  --free         wait: wait for the port to be released instead
  -t, --timeout DURATION
                 wait: give up after DURATION, e.g. 30s, 2m (default 60s; 0 = never)
  -h, --help     show this help message and exit
  -u, --udp      include UDP sockets in the listing
  --json         JSON output (list, PORT and free)
  -y, --yes      kill: don't ask for confirmation
  -f, --force    kill: send SIGKILL instead of SIGTERM
  -V, --version  show program's version number and exit

examples:
  portpeek                 what's listening right now
  portpeek 3000            who has port 3000?
  portpeek kill 3000       stop it (SIGTERM, asks first; -f if it won't die)
  portpeek free 8000       a free port, starting the search at 8000
  portpeek wait 3000 -t 20s   block until a dev server is up on 3000
  portpeek --json | jq     machine-readable listing

exit status: 0 ok, 1 nothing found or kill failed, 2 usage error, 3 permission denied
Only processes you can inspect show an owner; use sudo to see everyone's.
`

type options struct {
	udp, json, yes, force bool
	words                 []string
	free                  bool          // wait: until released
	timeout               time.Duration // wait
}

func usageError(msg string) int {
	fmt.Fprint(os.Stderr, usageText)
	// msg may echo the user's arguments; keep control characters out of the terminal.
	fmt.Fprintf(os.Stderr, "portpeek: error: %s\n", clean(msg))
	return exitUsage
}

// parseArgs accepts options anywhere, like `portpeek kill -y 3000`.
func parseArgs(args []string) (o options, done bool, code int) {
	o.timeout = defaultWaitTimeout
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--free":
			o.free = true
		case a == "-t" || a == "--timeout" || strings.HasPrefix(a, "--timeout="):
			v, hasV := strings.CutPrefix(a, "--timeout=")
			if !hasV {
				if i+1 >= len(args) {
					return o, true, usageError(a + " needs a duration, e.g. 30s")
				}
				i++
				v = args[i]
			}
			d, err := parseTimeout(v)
			if err != nil {
				return o, true, usageError(fmt.Sprintf("'%s' is not a duration like 30s, 2m or 0", v))
			}
			o.timeout = d
		case a == "--":
			o.words = append(o.words, args[i+1:]...)
			return o, false, 0
		case a == "-h" || a == "--help":
			printHelp()
			return o, true, exitOK
		case a == "-V" || a == "--version":
			fmt.Println("portpeek " + version)
			return o, true, exitOK
		case a == "--udp":
			o.udp = true
		case a == "--json":
			o.json = true
		case a == "--yes":
			o.yes = true
		case a == "--force":
			o.force = true
		case strings.HasPrefix(a, "--"):
			return o, true, usageError("unrecognized option " + a)
		case len(a) > 1 && a[0] == '-': // short flags, possibly clustered: -yf
			for _, c := range a[1:] {
				switch c {
				case 'u':
					o.udp = true
				case 'y':
					o.yes = true
				case 'f':
					o.force = true
				case 'h':
					printHelp()
					return o, true, exitOK
				case 'V':
					fmt.Println("portpeek " + version)
					return o, true, exitOK
				default:
					return o, true, usageError(fmt.Sprintf("unrecognized option -%c", c))
				}
			}
		default:
			o.words = append(o.words, a)
		}
	}
	return o, false, 0
}

// printHelp shows the banner above the help text when stdout is a terminal.
func printHelp() {
	if t := newTerm(); t.tty {
		fmt.Println(strings.Join(t.banner(), "\n"))
		fmt.Println()
	}
	fmt.Print(helpText)
}

func parsePort(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 || strings.ContainsAny(s, "+-") {
		return 0, false
	}
	return n, true
}

func fail(msg string, t *Term, hint string) {
	fmt.Fprintf(os.Stderr, "%s %s\n", t.paint(coral, "portpeek:", t.errColor), msg)
	if hint != "" {
		fmt.Fprintf(os.Stderr, "  %s\n", hint)
	}
}

func forPort(port int) []*Listener {
	var rows []*Listener
	for _, r := range collect(true) {
		if r.Port == port {
			rows = append(rows, r)
		}
	}
	return rows
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

func cmdList(o options, t *Term) int {
	rows := collect(o.udp)
	procs := loadProcs(rows)
	hidden := 0
	for _, r := range rows {
		if len(r.PIDs) == 0 {
			hidden++
		}
	}
	free := freeSuggestions()
	if o.json {
		list := make([]listenerJSON, 0, len(rows))
		for _, r := range rows {
			list = append(list, toJSON(r, procs))
		}
		printJSON(map[string]any{"listeners": list, "hidden_owners": hidden, "free_ports": free})
	} else {
		fmt.Println(renderList(rows, procs, hidden, free, t, o.udp))
	}
	return exitOK
}

func cmdInspect(port int, o options, t *Term) int {
	rows := forPort(port)
	if len(rows) == 0 {
		fail(fmt.Sprintf("nothing is listening on port %d.", port), t, "see what is: portpeek")
		return exitFail
	}
	procs := loadProcs(rows)
	if o.json {
		list := make([]listenerJSON, 0, len(rows))
		for _, r := range rows {
			list = append(list, toJSON(r, procs))
		}
		printJSON(map[string]any{"port": port, "listeners": list})
	} else {
		fmt.Println(renderInspect(port, rows, procs, t))
	}
	return exitOK
}

func cmdFree(start int, o options, t *Term) int {
	p := nextFree(start)
	if p == 0 {
		fail(fmt.Sprintf("no free port found at or after %d.", start), t, "")
		return exitFail
	}
	if o.json {
		printJSON(map[string]int{"port": p})
	} else {
		fmt.Println(p)
	}
	return exitOK
}

func cmdKill(port int, o options, t *Term) int {
	rows := forPort(port)
	if len(rows) == 0 {
		fail(fmt.Sprintf("nothing is listening on port %d.", port), t, "")
		return exitFail
	}
	var pids []int
	inodes := map[uint64]bool{}
	anyHidden := false
	for _, r := range rows {
		if len(r.PIDs) == 0 {
			anyHidden = true
			continue
		}
		for _, p := range r.PIDs {
			if !slices.Contains(pids, p) {
				pids = append(pids, p)
			}
		}
		for in := range r.Inodes {
			inodes[in] = true
		}
	}
	slices.Sort(pids)
	if len(pids) == 0 {
		fail(fmt.Sprintf("port %d is in use, but its owner is hidden from you.", port), t,
			fmt.Sprintf("try: sudo portpeek kill %d", port))
		return exitPerm
	}
	for _, p := range pids {
		if p == 1 || p == os.Getpid() {
			fail(fmt.Sprintf("refusing to stop pid %d (init or portpeek itself).", p), t, "")
			return exitFail
		}
	}
	procs := loadProcs(rows)
	sig, sigNote := "SIGTERM", `a polite "please quit"`
	if o.force {
		sig, sigNote = "SIGKILL", "instant, no cleanup"
	}

	fmt.Printf("\n  About to stop what's on port %s:\n", t.bold(fmt.Sprint(port)))
	for _, pid := range pids {
		who, where := describe(pid, rows, procs, port)
		fmt.Printf("    %s  %s\n", t.bold(who), t.dim(where))
	}
	if anyHidden {
		fmt.Println(t.dim(fmt.Sprintf("  (another listener on port %d has a hidden owner and is left alone)", port)))
	}
	if !o.yes {
		if !isatty(os.Stdin) {
			fail("refusing to stop anything without confirmation.", t, "pass -y to skip the prompt.")
			return exitUsage
		}
		target := "it"
		if len(pids) > 1 {
			target = "all of them"
		}
		fmt.Fprintf(os.Stderr, "\n  Send %s (%s) to %s? [y/N] ", sig, sigNote, target)
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			fmt.Print("  Left alone.\n\n")
			return exitFail
		}
	}
	fmt.Println()

	code := exitOK
	bump := func(c int) { code = max(code, c) }
	for _, pid := range pids {
		who, _ := describe(pid, rows, procs, port)
		status, took := terminate(pid, inodes, o.force, killTimeout)
		switch status {
		case "stopped":
			fmt.Printf("  %s %s stopped in %.1fs\n", t.sage(t.tick), who, took.Seconds())
		case "gone":
			fmt.Printf("  %s %s already gone\n", t.sage(t.tick), who)
		case "denied":
			fmt.Printf("  %s %s: permission denied (owned by another user)\n", t.coral(t.cross), who)
			fmt.Println(t.dim(fmt.Sprintf("    try: sudo portpeek kill %d", port)))
			bump(exitPerm)
		case "changed":
			fmt.Printf("  %s %s: no longer holds the port, skipped\n", t.coral(t.cross), who)
			bump(exitFail)
		case "unsupported":
			fmt.Printf("  %s %s: this kernel lacks pidfd support (needs Linux 5.3+)\n", t.coral(t.cross), who)
			bump(exitFail)
		default:
			fmt.Printf("  %s %s still running after %.0fs\n", t.coral(t.cross), who, took.Seconds())
			fmt.Println(t.dim(fmt.Sprintf("    ignoring %s? try: portpeek kill %d --force", sig, port)))
			bump(exitFail)
		}
	}

	time.Sleep(200 * time.Millisecond)
	again := forPort(port)
	if code == exitOK && len(again) == 0 {
		fmt.Printf("\n  %s\n", t.sage(fmt.Sprintf("Port %d is free.", port)))
	}
	if code == exitOK && len(again) > 0 {
		var who []string
		for _, r := range again {
			if len(r.PIDs) > 0 {
				s := fmt.Sprintf("pid %d", r.PIDs[0])
				if !slices.Contains(who, s) {
					who = append(who, s)
				}
			}
		}
		by := "another process"
		if len(who) > 0 {
			slices.Sort(who)
			by = strings.Join(who, ", ")
		}
		fmt.Println(t.dim(fmt.Sprintf("\n  port %d is in use again (%s) %s something may be restarting it", port, by, t.dot)))
	}
	fmt.Println()
	return code
}

func run(args []string) int {
	o, done, code := parseArgs(args)
	if done {
		return code
	}
	t := newTerm()
	w := o.words
	switch {
	case len(w) == 0 || (len(w) == 1 && w[0] == "list"):
		return cmdList(o, t)
	case w[0] == "free":
		start := 3000
		if len(w) > 2 {
			return usageError("free takes at most one PORT, e.g. portpeek free 8000")
		}
		if len(w) == 2 {
			n, ok := parsePort(w[1])
			if !ok || n < 1024 {
				return usageError(fmt.Sprintf("'%s' is not a usable start port (1024-65535; lower ports need root)", w[1]))
			}
			start = n
		}
		return cmdFree(start, o, t)
	case w[0] == "wait":
		if len(w) != 2 {
			return usageError("wait needs exactly one PORT, e.g. portpeek wait 3000")
		}
		port, ok := parsePort(w[1])
		if !ok {
			return usageError(fmt.Sprintf("'%s' is not a port number (1-65535)", w[1]))
		}
		return cmdWait(port, o, t)
	case w[0] == "kill":
		if len(w) != 2 {
			return usageError("kill needs exactly one PORT, e.g. portpeek kill 3000")
		}
		port, ok := parsePort(w[1])
		if !ok {
			return usageError(fmt.Sprintf("'%s' is not a port number (1-65535)", w[1]))
		}
		return cmdKill(port, o, t)
	case len(w) == 1:
		port, ok := parsePort(w[0])
		if !ok {
			return usageError(fmt.Sprintf("'%s' is not a port number (1-65535)", w[0]))
		}
		return cmdInspect(port, o, t)
	}
	return usageError("don't know what to do with: " + strings.Join(w, " "))
}

func main() {
	// Leave the terminal tidy on Ctrl-C. Writes to a closed pipe (`| head`)
	// kill the process via SIGPIPE, which is what we want.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT)
	go func() {
		<-sigs
		fmt.Fprintln(os.Stderr)
		os.Exit(130)
	}()
	os.Exit(run(os.Args[1:]))
}
