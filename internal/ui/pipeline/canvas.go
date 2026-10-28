package pipeline

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// A rune/style grid the pipeline is drawn on, then joined into lines.
// Adapted from wsl-monitor's graph canvas.

type cell struct {
	r rune // 0: right half of a wide rune
	s int  // index into canvas.st (0: unstyled)
}

type skey struct {
	c    color.RGBA
	bold bool
}

type canvas struct {
	w, h  int
	cells [][]cell
	st    []lipgloss.Style
	idx   map[skey]int
	hits  []Hit
}

func newCanvas(w, h int) *canvas {
	cv := &canvas{w: max(w, 0), h: max(h, 0), st: []lipgloss.Style{{}}, idx: map[skey]int{}}
	cv.cells = make([][]cell, cv.h)
	for y := range cv.cells {
		cv.cells[y] = make([]cell, cv.w)
		for x := range cv.cells[y] {
			cv.cells[y][x] = cell{r: ' '}
		}
	}
	return cv
}

// fg is the style index of a foreground colour and boldness.
func (cv *canvas) fg(col color.Color, bold bool) int {
	r, g, b, a := col.RGBA()
	k := skey{color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}, bold}
	if i, ok := cv.idx[k]; ok {
		return i
	}
	cv.st = append(cv.st, lipgloss.NewStyle().Foreground(col).Bold(bold))
	cv.idx[k] = len(cv.st) - 1
	return len(cv.st) - 1
}

func (cv *canvas) in(x, y int) bool { return x >= 0 && x < cv.w && y >= 0 && y < cv.h }

func (cv *canvas) set(x, y int, r rune, s int) {
	if cv.in(x, y) {
		cv.cells[y][x] = cell{r: r, s: s}
	}
}

// text writes s from x, never past xEnd (exclusive), truncating with "…".
func (cv *canvas) text(x, y int, s string, xEnd int, st int) {
	xEnd = min(xEnd, cv.w)
	if xEnd <= x {
		return
	}
	s = ansi.Truncate(s, xEnd-x, "…")
	for _, r := range s {
		rw := ansi.StringWidth(string(r))
		if rw == 0 {
			continue
		}
		if x+rw > xEnd {
			return
		}
		cv.set(x, y, r, st)
		if rw == 2 {
			cv.set(x+1, y, 0, st)
		}
		x += rw
	}
}

func (cv *canvas) lines() []string {
	out := make([]string, cv.h)
	for y, row := range cv.cells {
		var b, run strings.Builder
		cur := 0
		flush := func() {
			if cur == 0 {
				b.WriteString(run.String())
			} else {
				b.WriteString(cv.st[cur].Render(run.String()))
			}
			run.Reset()
		}
		for _, c := range row {
			if c.r == 0 {
				continue
			}
			if c.s != cur {
				flush()
				cur = c.s
			}
			run.WriteRune(c.r)
		}
		flush()
		out[y] = b.String()
	}
	return out
}

// ---- boxes -------------------------------------------------------------------

type border struct{ tl, tr, bl, br, h, v rune }

var (
	rounded = border{'╭', '╮', '╰', '╯', '─', '│'}
	double  = border{'╔', '╗', '╚', '╝', '═', '║'} // selected
	heavy   = border{'┏', '┓', '┗', '┛', '━', '┃'} // running
)

// box draws a w×h border with title in the top edge.
func (cv *canvas) box(x, y, w, h int, b border, st int, title string, tst int) {
	if w < 2 || h < 2 {
		return
	}
	for i := x + 1; i < x+w-1; i++ {
		cv.set(i, y, b.h, st)
		cv.set(i, y+h-1, b.h, st)
	}
	for j := y + 1; j < y+h-1; j++ {
		cv.set(x, j, b.v, st)
		cv.set(x+w-1, j, b.v, st)
		for i := x + 1; i < x+w-1; i++ {
			cv.set(i, j, ' ', 0)
		}
	}
	cv.set(x, y, b.tl, st)
	cv.set(x+w-1, y, b.tr, st)
	cv.set(x, y+h-1, b.bl, st)
	cv.set(x+w-1, y+h-1, b.br, st)
	if title != "" {
		cv.text(x+2, y, " "+title+" ", x+w-2, tst)
	}
}

// ---- edges ---------------------------------------------------------------------

const (
	dU uint8 = 1 << iota
	dD
	dL
	dR
)

func dirTo(a, b [2]int) uint8 {
	switch {
	case b[0] > a[0]:
		return dR
	case b[0] < a[0]:
		return dL
	case b[1] > a[1]:
		return dD
	case b[1] < a[1]:
		return dU
	}
	return 0
}

var lightGlyphs = map[uint8]rune{
	dL | dR: '─', dU | dD: '│', dD | dR: '┌', dD | dL: '┐', dU | dR: '└', dU | dL: '┘',
	dU | dD | dR: '├', dU | dD | dL: '┤', dL | dR | dD: '┬', dL | dR | dU: '┴', dU | dD | dL | dR: '┼',
	dL: '─', dR: '─', dU: '│', dD: '│',
}

var heavyGlyphs = map[uint8]rune{
	dL | dR: '━', dU | dD: '┃', dD | dR: '┏', dD | dL: '┓', dU | dR: '┗', dU | dL: '┛',
	dU | dD | dR: '┣', dU | dD | dL: '┫', dL | dR | dD: '┳', dL | dR | dU: '┻', dU | dD | dL | dR: '╋',
	dL: '━', dR: '━', dU: '┃', dD: '┃',
}

// path is a polyline of cells from source to target (target exclusive);
// the last cell gets an arrowhead.
type path struct {
	cells  [][2]int
	st     int
	heavy  bool
	active bool
}

// polyline expands corner points into every cell between them.
func polyline(pts ...[2]int) [][2]int {
	var out [][2]int
	for i, p := range pts {
		if i == 0 {
			out = append(out, p)
			continue
		}
		prev := pts[i-1]
		dx, dy := sign(p[0]-prev[0]), sign(p[1]-prev[1])
		for c := prev; c != p; {
			c = [2]int{c[0] + dx, c[1] + dy}
			out = append(out, c)
		}
	}
	return out
}

func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	}
	return 0
}

// drawPaths merges overlapping paths into junction glyphs. A heavy path
// wins a shared cell's weight and style.
func (cv *canvas) drawPaths(paths []path) {
	type ec struct {
		mask  uint8
		heavy bool
		st    int
	}
	cells := map[[2]int]*ec{}
	var order [][2]int
	for _, p := range paths {
		n := len(p.cells)
		for i, pt := range p.cells {
			var mask uint8
			if i > 0 {
				mask |= dirTo(pt, p.cells[i-1])
			} else if n > 1 {
				mask |= opposite(dirTo(pt, p.cells[1]))
			}
			if i < n-1 {
				mask |= dirTo(pt, p.cells[i+1])
			} else if n > 1 {
				mask |= opposite(dirTo(pt, p.cells[n-2]))
			}
			c := cells[pt]
			if c == nil {
				c = &ec{st: p.st}
				cells[pt] = c
				order = append(order, pt)
			}
			c.mask |= mask
			if p.heavy || !c.heavy {
				c.heavy, c.st = c.heavy || p.heavy, p.st
			}
		}
	}
	for _, pt := range order {
		c := cells[pt]
		g := lightGlyphs[c.mask]
		if c.heavy {
			g = heavyGlyphs[c.mask]
		}
		if g == 0 {
			g = '·'
		}
		cv.set(pt[0], pt[1], g, c.st)
	}
	for _, p := range paths {
		if n := len(p.cells); n > 1 {
			last := p.cells[n-1]
			arrow := map[uint8]rune{dR: '▶', dL: '◀', dD: '▼', dU: '▲'}[dirTo(p.cells[n-2], last)]
			cv.set(last[0], last[1], arrow, p.st)
		}
	}
}

func opposite(d uint8) uint8 {
	switch d {
	case dU:
		return dD
	case dD:
		return dU
	case dL:
		return dR
	case dR:
		return dL
	}
	return 0
}

// dotsAt returns the positions along a path of length n where flowing dots
// are drawn on this frame.
func dotsAt(frame, n int) []int {
	const spacing = 4
	var out []int
	for p := frame % spacing; p < n-1; p += spacing {
		out = append(out, p)
	}
	return out
}

func (cv *canvas) drawDots(paths []path, frame int, dot int) {
	for _, p := range paths {
		if !p.active {
			continue
		}
		for _, i := range dotsAt(frame, len(p.cells)) {
			pt := p.cells[i]
			cv.set(pt[0], pt[1], '●', dot)
		}
	}
}
