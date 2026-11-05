package main

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"syscall"
)

// Free-port finding. A port is free for localhost when both loopback
// addresses can bind it right now; trying is authoritative, since it also
// catches ports held by other users or by sockets that aren't listening.

// devStarts are the usual first ports for dev servers: the usual suspects
// (Node/Rails, Vite, Django/http.server, alt-HTTP).
var devStarts = []int{3000, 5173, 8000, 8080}

const lastQuietPort = 32767 // above this, Linux hands out ports to outgoing connections

func canBind(port int) bool {
	l4, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	defer l4.Close()
	l6, err := net.Listen("tcp6", fmt.Sprintf("[::1]:%d", port))
	if err != nil {
		// IPv6 disabled is fine; a port someone else holds is not.
		return !errors.Is(err, syscall.EADDRINUSE) && !errors.Is(err, syscall.EACCES)
	}
	l6.Close()
	return true
}

// nextFree returns the first bindable port >= start, or 0 if none is found.
func nextFree(start int) int {
	limit := lastQuietPort
	if start > limit {
		limit = 65535
	}
	for p := start; p <= limit; p++ {
		if canBind(p) {
			return p
		}
	}
	return 0
}

// freeSuggestions returns the first free port at or after each dev start,
// without repeats.
func freeSuggestions() []int {
	var out []int
	for _, base := range devStarts {
		for p := nextFree(base); p != 0; p = nextFree(p + 1) {
			if !slices.Contains(out, p) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}
