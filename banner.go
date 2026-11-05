package main

import (
	"strconv"
	"strings"
)

// The banner is the "ANSI Shadow" style: solid blocks with box-drawing
// shadows. Block and box-drawing characters are painted edge to edge by
// terminals, so the letters stay solid even with extra line spacing; thin
// ASCII strokes (# _ |) fall apart there. The O is an eye: portpeek peeks.

const pupil = '●' // drawn white

var wordmark = []string{
	"██████╗  ██████╗ ██████╗ ████████╗██████╗ ███████╗███████╗██╗  ██╗",
	"██╔══██╗██╔═══██╗██╔══██╗╚══██╔══╝██╔══██╗██╔════╝██╔════╝██║ ██╔╝",
	"██████╔╝██║ ● ██║██████╔╝   ██║   ██████╔╝█████╗  █████╗  █████╔╝",
	"██╔═══╝ ██║   ██║██╔══██╗   ██║   ██╔═══╝ ██╔══╝  ██╔══╝  ██╔═██╗",
	"██║     ╚██████╔╝██║  ██║   ██║   ██║     ███████╗███████╗██║  ██╗",
	"╚═╝      ╚═════╝ ╚═╝  ╚═╝   ╚═╝   ╚═╝     ╚══════╝╚══════╝╚═╝  ╚═╝",
}

// Gradient stops: gold -> orange -> red-orange. Mid-tones, so it reads on both
// dark and light backgrounds.
var stops = [3][3]int{{255, 200, 40}, {255, 150, 0}, {255, 95, 20}}

func ramp(u float64) (int, int, int) {
	a, b := stops[0], stops[1]
	if u >= 0.5 {
		a, b, u = stops[1], stops[2], u-0.5
	}
	u *= 2
	f := func(i int) int { return a[i] + int(float64(b[i]-a[i])*u) }
	return f(0), f(1), f(2)
}

// to256 maps RGB to the nearest xterm-256 color-cube index.
func to256(r, g, b int) int {
	levels := [6]int{0, 95, 135, 175, 215, 255}
	near := func(v int) int {
		best := 0
		for i, l := range levels {
			if abs(v-l) < abs(v-levels[best]) {
				best = i
			}
		}
		return best
	}
	return 16 + 36*near(r) + 6*near(g) + near(b)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// sgr is the escape parameter string for banner cell (x, y): a diagonal
// gradient, quantized to 16 steps so neighbouring cells share runs.
func (t *Term) sgr(x, y, w, h int) string {
	u := (float64(x)/float64(w) + float64(y)/float64(h-1)) / 2
	r, g, b := ramp(float64(int(u*16+0.5)) / 16)
	if t.truecolor {
		return "1;38;2;" + strconv.Itoa(r) + ";" + strconv.Itoa(g) + ";" + strconv.Itoa(b)
	}
	return "1;38;5;" + strconv.Itoa(to256(r, g, b))
}

func wordmarkWidth() int {
	w := 0
	for _, r := range wordmark {
		w = max(w, cells(r))
	}
	return w
}

// banner is the wordmark in color, or a one-line fallback when the output
// isn't a terminal, isn't UTF-8 (the blocks need it), or is too narrow.
func (t *Term) banner() []string {
	w := wordmarkWidth()
	if !t.tty || !t.utf || t.width < w+4 {
		return []string{"  " + t.amber("portpeek")}
	}
	out := make([]string, len(wordmark))
	for y, row := range wordmark {
		var b, run strings.Builder
		runCode := ""
		flush := func() {
			b.WriteString(t.paint(runCode, run.String(), t.color))
			run.Reset()
		}
		b.WriteString("  ")
		for x, r := range []rune(row) {
			if r == ' ' {
				flush()
				b.WriteByte(' ')
				continue
			}
			code := t.sgr(x, y, w, len(wordmark))
			if r == pupil {
				code = "1;38;5;231"
			}
			if code != runCode {
				flush()
				runCode = code
			}
			run.WriteRune(r)
		}
		flush()
		out[y] = strings.TrimRight(b.String(), " ")
	}
	return out
}
