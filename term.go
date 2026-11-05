//go:build linux

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unicode"
	"unsafe"
)

const (
	amber = "38;5;214"
	coral = "38;5;203"
	sage  = "38;5;108"
	teal  = "38;5;73"
)

// Term holds what we know about the output terminal.
type Term struct {
	color, errColor bool
	tty             bool // stdout is a terminal (even if color is off)
	truecolor       bool // terminal advertises 24-bit color
	width           int
	arrow, dot, ell string
	tick, cross     string
	open, local     string // scope markers
	home            string
	utf             bool
}

type style func(string) string

func isatty(f *os.File) bool {
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return e == 0
}

func termWidth() int {
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		return n
	}
	var ws struct{ rows, cols, x, y uint16 }
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
	if e == 0 && ws.cols > 0 {
		return int(ws.cols)
	}
	return 80
}

func utf8Locale() bool {
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := os.Getenv(k); v != "" {
			v = strings.ToLower(v)
			return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
		}
	}
	return false
}

func newTerm() *Term {
	dumb := os.Getenv("TERM") == "dumb"
	plain := os.Getenv("NO_COLOR") != "" || dumb
	t := &Term{
		tty:       isatty(os.Stdout),
		truecolor: hasTruecolor(),
		color:     isatty(os.Stdout) && !plain,
		errColor:  isatty(os.Stderr) && !plain,
		width:     termWidth(),
	}
	t.home, _ = os.UserHomeDir()
	if t.utf = utf8Locale() && !dumb; t.utf {
		t.arrow, t.dot, t.ell, t.tick, t.cross = "▸", "·", "…", "✓", "✗"
		t.open, t.local = "●", "○"
	} else {
		t.arrow, t.dot, t.ell, t.tick, t.cross = ">", "-", "~", "ok", "!!"
		t.open, t.local = "*", "-"
	}
	return t
}

func (t *Term) paint(code, s string, on bool) string {
	if !on || s == "" {
		return s
	}
	return fmt.Sprintf("\x1b[%sm%s\x1b[0m", code, s)
}

func (t *Term) bold(s string) string  { return t.paint("1", s, t.color) }
func (t *Term) dim(s string) string   { return t.paint("2", s, t.color) }
func (t *Term) amber(s string) string { return t.paint("1;"+amber, s, t.color) }
func (t *Term) sage(s string) string  { return t.paint(sage, s, t.color) }
func (t *Term) coral(s string) string { return t.paint(coral, s, t.color) }
func (t *Term) teal(s string) string  { return t.paint(teal, s, t.color) }

func ident(s string) string { return s }

// runeWidth: terminal cells a rune occupies (0 combining, 2 wide, else 1).
func runeWidth(r rune) int {
	switch {
	case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r):
		return 0
	case r >= 0x1100 && r <= 0x115F, r >= 0x2E80 && r <= 0xA4CF, r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF, r >= 0xFE30 && r <= 0xFE6F, r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6, r >= 0x1F300 && r <= 0x1F64F, r >= 0x1F680 && r <= 0x1F6FF,
		r >= 0x1F900 && r <= 0x1FAFF, r >= 0x20000 && r <= 0x3FFFD:
		return 2
	}
	return 1
}

func cells(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

// fit clips s to width terminal cells (with an ellipsis) and optionally pads.
func (t *Term) fit(s string, width int, pad bool) string {
	if cells(s) > width {
		var b strings.Builder
		used := 0
		for _, r := range s {
			w := runeWidth(r)
			if used+w > width-1 {
				break
			}
			b.WriteRune(r)
			used += w
		}
		s = b.String() + t.ell
	}
	if pad {
		s += strings.Repeat(" ", max(0, width-cells(s)))
	}
	return s
}

// hasTruecolor reports whether the terminal advertises 24-bit color, either
// through COLORTERM or by being one of the terminals known to support it.
func hasTruecolor() bool {
	if c := os.Getenv("COLORTERM"); c == "truecolor" || c == "24bit" {
		return true
	}
	switch term := os.Getenv("TERM"); {
	case strings.HasSuffix(term, "-direct"), term == "xterm-kitty", term == "xterm-ghostty",
		term == "alacritty", strings.HasPrefix(term, "foot"), strings.HasPrefix(term, "wezterm"):
		return true
	}
	return false
}
