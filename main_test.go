//go:build linux

// End-to-end tests: the real portpeekBin, real listener processes, a real pty.
// Run: go test ./...
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

var portpeekBin string

// TestMain doubles as the listener helper: when re-executed with
// PORTPEEK_TEST_LISTENER=host,mode it opens a port, prints it, and sleeps.
func TestMain(m *testing.M) {
	if spec := os.Getenv("PORTPEEK_TEST_LISTENER"); spec != "" {
		host, mode, _ := strings.Cut(spec, ",")
		if mode == "ignore-term" {
			signal.Ignore(syscall.SIGTERM)
		}
		l, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(l.Addr().(*net.TCPAddr).Port)
		time.Sleep(60 * time.Second)
		return
	}
	dir, err := os.MkdirTemp("", "portpeek-test")
	if err != nil {
		panic(err)
	}
	portpeekBin = filepath.Join(dir, "portpeek")
	if out, err := exec.Command("go", "build", "-o", portpeekBin, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build failed: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func spawn(t *testing.T, host, mode, argv0 string) (*exec.Cmd, int) {
	t.Helper()
	if argv0 == "" {
		argv0 = os.Args[0]
	}
	cmd := &exec.Cmd{Path: os.Args[0], Args: []string{argv0},
		Env: append(os.Environ(), "PORTPEEK_TEST_LISTENER="+host+","+mode)}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("listener did not start: %v", err)
	}
	port, _ := strconv.Atoi(strings.TrimSpace(line))
	return cmd, port
}

func listener(t *testing.T) (*exec.Cmd, int) { return spawn(t, "127.0.0.1", "normal", "") }

type result struct {
	out, err string
	code     int
}

func pp(t *testing.T, args ...string) result { return ppCols(t, 100, args...) }

func ppCols(t *testing.T, cols int, args ...string) result {
	t.Helper()
	cmd := exec.Command(portpeekBin, args...)
	env := []string{}
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "NO_COLOR=") && !strings.HasPrefix(e, "COLUMNS=") {
			env = append(env, e)
		}
	}
	cmd.Env = append(env, fmt.Sprintf("COLUMNS=%d", cols), "LC_ALL=C.UTF-8")
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return result{o.String(), e.String(), code}
}

// rowFor finds a listing row by port and reports which section it sits in.
func rowFor(out string, port int) (row, section string) {
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "  OPEN TO THE NETWORK"):
			section = "open"
		case strings.HasPrefix(l, "  ONLY THIS COMPUTER"):
			section = "local"
		}
		if f := strings.Fields(l); len(f) > 1 && f[1] == strconv.Itoa(port) {
			return l, section
		}
	}
	return "", ""
}

func contains(t *testing.T, haystack string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			t.Errorf("missing %q in:\n%s", n, haystack)
		}
	}
}

func waitExit(t *testing.T, cmd *exec.Cmd) syscall.Signal {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		return cmd.ProcessState.Sys().(syscall.WaitStatus).Signal()
	case <-time.After(5 * time.Second):
		t.Fatal("process still running")
		return 0
	}
}

func alive(cmd *exec.Cmd) bool {
	return cmd.Process.Signal(syscall.Signal(0)) == nil && cmd.ProcessState == nil
}

func TestHelpVersionUsage(t *testing.T) {
	r := pp(t, "--help")
	if r.code != 0 {
		t.Fatalf("help exit %d", r.code)
	}
	contains(t, r.out, "portpeek kill 3000", "exit status")
	if r := pp(t, "--version"); r.code != 0 || strings.TrimSpace(r.out) != "portpeek "+version {
		t.Errorf("version: %+v", r)
	}
	for _, bad := range []string{"abc", "0", "70000", "-5"} {
		if r := pp(t, bad); r.code != 2 {
			t.Errorf("%q: exit %d, want 2", bad, r.code)
		}
	}
	if r := pp(t, "kill"); r.code != 2 {
		t.Errorf("bare kill: exit %d", r.code)
	}
	if r := pp(t, "--bogus"); r.code != 2 || !strings.Contains(r.err, "unrecognized") {
		t.Errorf("bogus flag: %+v", r)
	}
}

func TestListShowsListener(t *testing.T) {
	cmd, port := listener(t)
	r := pp(t)
	row, section := rowFor(r.out, port)
	if r.code != 0 || row == "" {
		t.Fatalf("no row for %d:\n%s", port, r.out)
	}
	contains(t, row, strconv.Itoa(cmd.Process.Pid))
	if section != "local" {
		t.Errorf("loopback listener listed under %q", section)
	}
	if strings.Contains(r.out, "\x1b") {
		t.Error("ANSI escapes in piped output")
	}
}

func TestJSON(t *testing.T) {
	cmd, port := listener(t)
	r := pp(t, "--json")
	var data struct {
		Listeners []struct {
			Port  int      `json:"port"`
			PIDs  []int    `json:"pids"`
			Bind  []string `json:"bind"`
			Scope string   `json:"scope"`
		} `json:"listeners"`
	}
	if err := json.Unmarshal([]byte(r.out), &data); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, r.out)
	}
	found := false
	for _, l := range data.Listeners {
		if l.Port == port {
			found = true
			if len(l.PIDs) != 1 || l.PIDs[0] != cmd.Process.Pid || l.Scope != "local" || l.Bind[0] != "127.0.0.1" {
				t.Errorf("wrong listener: %+v", l)
			}
		}
	}
	if !found || strings.Contains(r.out, "\x1b") {
		t.Errorf("found=%v, or escapes in JSON", found)
	}
	if !strings.Contains(r.out, `"pids": []`) && !strings.Contains(r.out, `"pids": [`) {
		t.Error("pids must always be an array")
	}
}

func TestOpenVsLocalAndIPv6(t *testing.T) {
	// Skip on hosts without IPv6 loopback (e.g. some containers/CI sandboxes).
	if l, err := net.Listen("tcp6", "[::1]:0"); err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	} else {
		l.Close()
	}
	_, openPort := spawn(t, "0.0.0.0", "normal", "")
	_, v6Port := spawn(t, "::1", "normal", "")
	out := pp(t).out
	if _, sec := rowFor(out, openPort); sec != "open" {
		t.Errorf("0.0.0.0 listener listed under %q, want open", sec)
	}
	if _, sec := rowFor(out, v6Port); sec != "local" {
		t.Errorf("::1 listener listed under %q, want local", sec)
	}
	contains(t, pp(t, strconv.Itoa(v6Port)).out, fmt.Sprintf("[::1]:%d", v6Port))
}

func TestInspect(t *testing.T) {
	cmd, port := listener(t)
	r := pp(t, strconv.Itoa(port))
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	contains(t, r.out, fmt.Sprintf("is listening on port %d.", port), "Only this computer can reach it.",
		fmt.Sprintf("pid %d", cmd.Process.Pid), fmt.Sprintf("127.0.0.1:%d", port),
		"what", "where", "running", fmt.Sprintf("portpeek kill %d", port))
}

func TestInspectFreePort(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	free := l.Addr().(*net.TCPAddr).Port
	l.Close()
	r := pp(t, strconv.Itoa(free))
	if r.code != 1 || r.out != "" || !strings.Contains(r.err, "nothing is listening") {
		t.Errorf("%+v", r)
	}
}

func TestNarrowTerminalNeverOverflows(t *testing.T) {
	listener(t)
	for _, cols := range []int{40, 60, 80, 120} {
		for _, line := range strings.Split(ppCols(t, cols).out, "\n") {
			if cells(line) > cols {
				t.Errorf("%d cols: %d-cell line %q", cols, cells(line), line)
			}
		}
	}
}

func TestControlCharactersNeutralised(t *testing.T) {
	_, port := spawn(t, "127.0.0.1", "normal", "\x1b[31mevil\x07")
	out := pp(t, strconv.Itoa(port)).out + pp(t).out
	contains(t, out, "evil")
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Error("control characters leaked through")
	}
}

func TestBrokenPipeIsQuiet(t *testing.T) {
	listener(t)
	var e bytes.Buffer
	cmd := exec.Command("sh", "-c", portpeekBin+" | head -1")
	cmd.Stderr = &e
	if err := cmd.Run(); err != nil || e.Len() != 0 {
		t.Errorf("err=%v stderr=%q", err, e.String())
	}
}

func TestKillStopsExactlyThatProcess(t *testing.T) {
	victim, port := listener(t)
	bystander, _ := listener(t)
	r := pp(t, "kill", strconv.Itoa(port), "-y")
	if r.code != 0 {
		t.Fatalf("exit %d\n%s%s", r.code, r.out, r.err)
	}
	contains(t, r.out, "stopped", fmt.Sprintf("Port %d is free.", port))
	if sig := waitExit(t, victim); sig != syscall.SIGTERM {
		t.Errorf("victim died of %v, want SIGTERM", sig)
	}
	if !alive(bystander) {
		t.Error("bystander was killed")
	}
}

func TestOptionsMayComeFirstAndCluster(t *testing.T) {
	victim, port := listener(t)
	if r := pp(t, "-y", "kill", strconv.Itoa(port)); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	waitExit(t, victim)
	victim, port = spawn(t, "127.0.0.1", "ignore-term", "")
	if r := pp(t, "kill", "-yf", strconv.Itoa(port)); r.code != 0 {
		t.Fatalf("-yf: %+v", r)
	}
	if sig := waitExit(t, victim); sig != syscall.SIGKILL {
		t.Errorf("died of %v, want SIGKILL", sig)
	}
}

func TestKillWithoutTTYOrYesRefuses(t *testing.T) {
	victim, port := listener(t)
	r := pp(t, "kill", strconv.Itoa(port))
	if r.code != 2 || !strings.Contains(r.err, "-y") || !alive(victim) {
		t.Errorf("%+v alive=%v", r, alive(victim))
	}
}

func TestStubbornProcessNeedsForce(t *testing.T) {
	victim, port := spawn(t, "127.0.0.1", "ignore-term", "")
	r := pp(t, "kill", strconv.Itoa(port), "-y")
	if r.code != 1 || !strings.Contains(r.out, "--force") || !alive(victim) {
		t.Fatalf("%+v", r)
	}
	if r := pp(t, "kill", strconv.Itoa(port), "-y", "--force"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if sig := waitExit(t, victim); sig != syscall.SIGKILL {
		t.Errorf("died of %v", sig)
	}
}

func TestKillFreePort(t *testing.T) {
	if r := pp(t, "kill", "9", "-y"); r.code != 1 {
		t.Errorf("%+v", r)
	}
}

func TestHiddenOwnerIsPermissionErrorNotAKill(t *testing.T) {
	var hidden *Listener
	for _, l := range collect(false) {
		if len(l.PIDs) == 0 {
			hidden = l
			break
		}
	}
	if hidden == nil || os.Geteuid() == 0 {
		t.Skip("no hidden-owner listener here, or running as root")
	}
	r := pp(t, "kill", strconv.Itoa(hidden.Port), "-y")
	if r.code != 3 || !strings.Contains(r.err, "sudo") {
		t.Errorf("%+v", r)
	}
}

func TestTerminateSkipsPidThatDoesNotOwnThePort(t *testing.T) {
	other, _ := listener(t)
	status, _ := terminate(other.Process.Pid, map[uint64]bool{0: true}, false, time.Second)
	if status != "changed" || !alive(other) {
		t.Errorf("status=%s alive=%v", status, alive(other))
	}
}

// runPTY runs portpeek on a real pseudo-terminal, feeding it `input`, and
// returns everything it printed.
func runPTY(t *testing.T, input string, cols int, args ...string) string {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skip("no /dev/ptmx")
	}
	defer master.Close()
	var unlock int32
	var n uint32
	ioctl := func(req uintptr, arg unsafe.Pointer) {
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), req, uintptr(arg)); e != 0 {
			t.Fatalf("ioctl: %v", e)
		}
	}
	ioctl(syscall.TIOCSPTLCK, unsafe.Pointer(&unlock))
	ioctl(syscall.TIOCGPTN, unsafe.Pointer(&n))
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(portpeekBin, args...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("COLUMNS=%d", cols), "LC_ALL=C.UTF-8", "TERM=xterm-256color")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	slave.Close()
	if input != "" {
		master.Write([]byte(input))
	}
	var out bytes.Buffer
	buf := make([]byte, 4096)
	for {
		k, err := master.Read(buf)
		out.Write(buf[:k])
		if err != nil {
			break // EIO once the child exits
		}
	}
	cmd.Wait()
	return out.String()
}

// Declining at the prompt must leave the process untouched. Needs a real tty.
func TestDecliningAtPromptLeavesProcessAlone(t *testing.T) {
	victim, port := listener(t)
	out := runPTY(t, "n\n", 100, "kill", strconv.Itoa(port))
	contains(t, out, "Left alone", `a polite "please quit"`)
	if !alive(victim) {
		t.Error("victim was killed after answering n")
	}
}

var ansiSeq = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestBannerOnTerminalNotInPipe(t *testing.T) {
	art := "██████╗  ██████╗" // the top of "PO"; the gradient splits it across color codes
	plain := func(s string) string { return ansiSeq.ReplaceAllString(s, "") }
	listener(t)
	contains(t, plain(runPTY(t, "", 100)), art, "listening")
	if strings.Contains(pp(t).out, art) {
		t.Error("ASCII art leaked into piped output")
	}
	if narrow := runPTY(t, "", 40); strings.Contains(plain(narrow), art) {
		t.Error("banner shown on a terminal too narrow for it")
	}
	contains(t, plain(runPTY(t, "", 100, "--help")), art, "commands:")
}

func TestFreeSkipsBusyPorts(t *testing.T) {
	_, busy := listener(t)
	r := pp(t, "free", strconv.Itoa(busy))
	got, err := strconv.Atoi(strings.TrimSpace(r.out))
	if r.code != 0 || err != nil || got <= busy {
		t.Fatalf("free %d -> %+v", busy, r)
	}
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", got))
	if err != nil {
		t.Errorf("suggested port %d is not actually free: %v", got, err)
	} else {
		l.Close()
	}
	if r := pp(t, "free", strconv.Itoa(busy), "--json"); !strings.Contains(r.out, fmt.Sprintf(`"port": %d`, got)) {
		t.Errorf("json: %s", r.out)
	}
}

func TestFreeUsage(t *testing.T) {
	for _, bad := range [][]string{{"free", "80"}, {"free", "abc"}, {"free", "3000", "4000"}} {
		if r := pp(t, bad...); r.code != 2 {
			t.Errorf("%v: exit %d, want 2", bad, r.code)
		}
	}
	if r := pp(t, "free"); r.code != 0 {
		t.Errorf("bare free: %+v", r)
	}
}

func TestListSuggestsFreePorts(t *testing.T) {
	_, busy := listener(t)
	out := pp(t)
	contains(t, out.out, "FREE FOR LOCALHOST", "portpeek free")
	var data struct {
		Free []int `json:"free_ports"`
	}
	if err := json.Unmarshal([]byte(pp(t, "--json").out), &data); err != nil || len(data.Free) == 0 {
		t.Fatalf("free_ports missing: %v", err)
	}
	for _, p := range data.Free {
		if p == busy {
			t.Errorf("suggested the busy port %d", p)
		}
	}
}

func TestCommandDropsRepeatedProgramName(t *testing.T) {
	for _, c := range []struct{ name, cmd, want string }{
		{"python", "python -m app.main", "-m app.main"},
		{"ollama", "ollama serve", "serve"},
		{"uvicorn", "python3 uvicorn app:app", "python3 uvicorn app:app"},
		{"averyveryverylongprogram"[:15], "averyveryverylongprogram --x", "--x"},
	} {
		if got := stripArgv0(c.name, c.cmd); got != c.want {
			t.Errorf("stripArgv0(%q, %q) = %q, want %q", c.name, c.cmd, got, c.want)
		}
	}
}

func TestDecodeAddr(t *testing.T) {
	for in, want := range map[string]string{
		"0100007F":                         "127.0.0.1",
		"00000000":                         "0.0.0.0",
		"00000000000000000000000001000000": "::1",
		"0000000000000000FFFF00000100007F": "127.0.0.1",
	} {
		if got := decodeAddr(in); got != want {
			t.Errorf("decodeAddr(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestBindLabel(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{[]string{"0.0.0.0", "::"}, "*"},
		{[]string{"127.0.0.1", "::1"}, "localhost"},
		{[]string{"10.0.0.5"}, "10.0.0.5"},
		{[]string{"127.0.0.1", "127.0.0.53", "::1"}, "localhost +1"},
	} {
		if got := bindLabel(c.in); got != c.want {
			t.Errorf("bindLabel(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFmtDuration(t *testing.T) {
	for in, want := range map[float64]string{5: "5s", 125: "2m 05s", 7384: "2h 03m", 190000: "2d 4h", -1: "unknown"} {
		if got := fmtDuration(in); got != want {
			t.Errorf("fmtDuration(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestUsageErrorsDoNotEchoEscapes(t *testing.T) {
	for _, args := range [][]string{{"\x1b[31mevil"}, {"kill", "\x1b]0;pwned\x07"}, {"--\x1b[2J"}, {"a\x1b[2J", "b"}} {
		r := pp(t, args...)
		if r.code != 2 || strings.ContainsRune(r.err, 0x1b) || strings.ContainsRune(r.err, 7) {
			t.Errorf("%q -> code %d, stderr %q", args, r.code, r.err)
		}
	}
}

func TestEmojiAreTwoCellsWide(t *testing.T) {
	for _, s := range []string{"🚀", "🧪", "🔥", "日"} {
		if cells(s) != 2 {
			t.Errorf("cells(%q) = %d, want 2", s, cells(s))
		}
	}
}

func TestFitIsWidthAware(t *testing.T) {
	tm := &Term{ell: "…"}
	if got := cells(tm.fit("日本語日本語", 7, false)); got != 7 {
		t.Errorf("wide text fit to %d cells, want 7", got)
	}
	if got := tm.fit("abc", 6, true); got != "abc   " {
		t.Errorf("pad: %q", got)
	}
}

func TestOpenListenerInspectWarnsAboutNetwork(t *testing.T) {
	_, port := spawn(t, "0.0.0.0", "normal", "")
	contains(t, pp(t, strconv.Itoa(port)).out, "Other machines on your network can reach it")
}

func TestHiddenOwnerGetsAGuessInPlainWords(t *testing.T) {
	var hidden *Listener
	for _, l := range collect(false) {
		if len(l.PIDs) == 0 && portHints[l.Port] != "" {
			hidden = l
			break
		}
	}
	if hidden == nil {
		t.Skip("no hidden owner on a well-known port here")
	}
	out := pp(t, strconv.Itoa(hidden.Port)).out
	contains(t, out, "probably "+portHints[hidden.Port], "sudo portpeek")
}

func TestShortCmdAndPaths(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{shortCmd("/usr/bin/node /home/me/app/server.js --port 3000", "/home/me"), "node ~/app/server.js --port 3000"},
		{shortCmd("[kworker/0:1]", "/home/me"), "[kworker/0:1]"},
		{tildePath("/home/me", "/home/me"), "~"},
		{tildePath("/srv/a/very/long/path/to/some/project/dir", "/home/me"), "…/project/dir"},
		{niceCwd("/", "/home/me"), ""},
		{niceCwd("/home/me", "/home/me"), ""},
		{niceCwd("/home/me/app", "/home/me"), "~/app"},
		{fmtAge(7384), "up 2h"},
		{fmtAge(40), "up 40s"},
		{fmtAge(-1), ""},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestWordmarkIsWellFormed(t *testing.T) {
	pupils := 0
	for _, r := range wordmark {
		if r != strings.TrimRight(r, " ") {
			t.Errorf("trailing spaces in %q", r)
		}
		for _, c := range r {
			if !strings.ContainsRune("█╗╔╝╚║═ ●", c) {
				t.Errorf("unexpected %q in %q", c, r)
			}
			if c == pupil {
				pupils++
			}
		}
	}
	if len(wordmark) != 6 || pupils != 1 {
		t.Errorf("%d rows and %d pupils, want 6 and 1", len(wordmark), pupils)
	}
	if w := wordmarkWidth(); w > 70 {
		t.Errorf("wordmark is %d cells wide; it should fit a 74-column terminal", w)
	}
}

func TestBannerColorsAndFallbacks(t *testing.T) {
	tm := &Term{tty: true, utf: true, color: true, width: 100}
	flat := strings.Join(tm.banner(), "")
	if !strings.Contains(flat, "38;5;") || strings.Contains(flat, "38;2;") {
		t.Error("expected 256-color codes without truecolor")
	}
	if !strings.Contains(flat, "38;5;231") {
		t.Error("the pupil should be drawn white")
	}
	tm.truecolor = true
	if flat := strings.Join(tm.banner(), ""); !strings.Contains(flat, "38;2;") {
		t.Error("expected 24-bit codes with truecolor")
	}
	tm.color = false
	if flat := strings.Join(tm.banner(), ""); strings.Contains(flat, "\x1b") || !strings.Contains(flat, "██") {
		t.Error("NO_COLOR banner should be plain blocks")
	}
	for name, term := range map[string]*Term{
		"not a tty":  {utf: true, width: 100},
		"not UTF-8":  {tty: true, width: 100},
		"too narrow": {tty: true, utf: true, width: 60},
	} {
		if rows := term.banner(); len(rows) != 1 || !strings.Contains(rows[0], "portpeek") {
			t.Errorf("%s: want the one-line fallback, got %q", name, rows)
		}
	}
	if rows := (&Term{tty: true, utf: true, width: 74}).banner(); len(rows) != 6 {
		t.Errorf("a 74-column terminal should get the full banner, got %d rows", len(rows))
	}
}
