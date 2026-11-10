//go:build linux

package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	defaultWaitTimeout = 60 * time.Second
	waitInterval       = 100 * time.Millisecond
)

// parseTimeout accepts Go durations ("30s", "2m"), a bare number of seconds
// ("30"), and 0 for "wait forever". Negative values are rejected.
func parseTimeout(s string) (time.Duration, error) {
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("negative")
		}
		return time.Duration(n * float64(time.Second)), nil
	}
	d, err := time.ParseDuration(s)
	if err == nil && d < 0 {
		err = fmt.Errorf("negative")
	}
	return d, err
}

// listening reports whether anything (TCP, or UDP when asked) is bound to port.
func listening(port int, udp bool) bool {
	for _, r := range collect(udp) {
		if r.Port == port {
			return true
		}
	}
	return false
}

// waitFor polls until listening(port) == wantListening or the timeout passes
// (timeout 0 = never). It returns whether the condition was reached.
func waitFor(port int, wantListening, udp bool, timeout time.Duration, check func(int, bool) bool) (bool, time.Duration) {
	start := time.Now()
	for {
		if check(port, udp) == wantListening {
			return true, time.Since(start)
		}
		if timeout > 0 && time.Since(start) >= timeout {
			return false, time.Since(start)
		}
		time.Sleep(waitInterval)
	}
}

func cmdWait(port int, o options, t *Term) int {
	want := !o.free
	ok, took := waitFor(port, want, o.udp, o.timeout, listening)
	state := "listening"
	if o.free {
		state = "free"
	}
	if o.json {
		printJSON(map[string]any{"port": port, "state": state, "reached": ok, "waited_ms": took.Milliseconds()})
	}
	if !ok {
		fail(fmt.Sprintf("timed out after %s waiting for port %d to be %s.", took.Round(time.Millisecond), port, state), t, "")
		return exitFail
	}
	if !o.json && isatty(os.Stdout) {
		fmt.Printf("port %d is %s (%.1fs)\n", port, state, took.Seconds())
	}
	return exitOK
}
