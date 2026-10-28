// Package pipeline draws a site's code pipeline: multidevs feeding dev, and
// dev → test → live, with what's waiting on each hop, deploys in flight as
// flowing dots, and landings and failures on the target boxes.
//
// Rendering is a pure function of a Graph; the ui package builds the Graph
// from loaded Terminus data.
package pipeline

import (
	"fmt"
	"strings"

	"github.com/rithlath/lazyterminus/internal/ui/theme"
)

// State of an environment in the pipeline, in order of precedence.
type State int

// Node states.
const (
	Unknown State = iota
	OK
	Waiting
	Landed
	Failed
	Running
)

// Symbol is the state's color-independent marker.
func (s State) Symbol() string {
	switch s {
	case OK:
		return theme.SymOK
	case Waiting:
		return theme.SymAhead
	case Landed:
		return theme.SymSuccess
	case Failed:
		return theme.SymFailed
	case Running:
		return theme.SymRunning
	}
	return theme.SymUnknown
}

// Node is one environment box.
type Node struct {
	ID       string
	Mode     string // git, sftp
	State    State
	Status   string  // "in sync", "↑2 waiting", "deploying 3/6", "landed 2 commits", "failed: …"
	Head     string  // newest commit: "a1c9e0f message"
	When     string  // "deployed 4d ago"
	Progress float64 // 0..1 while running; <0 when unknown
	Extra    string  // mode line suffix, e.g. "✎2 uncommitted"
}

// Edge is a hop code takes. Pending is the number of commits waiting to
// move (-1 unknown). Active means a deploy or merge is moving it now.
type Edge struct {
	From, To string
	Pending  int
	Active   bool
}

// Flow is content (database and/or files) being cloned between two main
// environments; it's drawn as an arc above the boxes, in either direction.
type Flow struct {
	From, To string
	Label    string
	Active   bool
}

// Graph is a site's pipeline.
type Graph struct {
	Multidevs []Node
	Main      []Node // dev, test, live (whichever exist, in that order)
	Edges     []Edge
	Flows     []Flow
}

// Order is the selection order: multidevs, then dev, test, live.
func (g Graph) Order() []string {
	var out []string
	for _, n := range g.Multidevs {
		out = append(out, n.ID)
	}
	for _, n := range g.Main {
		out = append(out, n.ID)
	}
	return out
}

// Edge returns the edge between two environments.
func (g Graph) Edge(from, to string) (Edge, bool) {
	for _, e := range g.Edges {
		if e.From == from && e.To == to {
			return e, true
		}
	}
	return Edge{}, false
}

// Animated reports whether anything is moving (so the view needs frames).
func (g Graph) Animated() bool {
	for _, e := range g.Edges {
		if e.Active {
			return true
		}
	}
	for _, f := range g.Flows {
		if f.Active {
			return true
		}
	}
	for _, n := range append(append([]Node{}, g.Main...), g.Multidevs...) {
		if n.State == Running {
			return true
		}
	}
	return false
}

// MinWideWidth is the narrowest canvas that fits the side-by-side layout.
const MinWideWidth = 96

type styles struct {
	muted, text, bold, ok, warn, fail, sftp, accent, dot, run, flow int
}

func newStyles(cv *canvas) styles {
	return styles{
		muted: cv.fg(theme.Muted, false), text: 0, bold: cv.fg(theme.White, true),
		ok: cv.fg(theme.Green, false), warn: cv.fg(theme.Amber, false), fail: cv.fg(theme.Red, false),
		sftp: cv.fg(theme.Violet, false), accent: cv.fg(theme.Accent, true), dot: cv.fg(theme.Amber, true),
		run: cv.fg(theme.Amber, true), flow: cv.fg(theme.Violet, true),
	}
}

func (s styles) state(st State) int {
	switch st {
	case OK, Landed:
		return s.ok
	case Waiting, Running:
		return s.warn
	case Failed:
		return s.fail
	}
	return s.muted
}

// Render draws g into exactly w×h cells and returns the lines. sel is the
// selected environment; frame drives the animation.
func Render(g Graph, w, h int, sel string, frame int) []string {
	lines, _ := RenderHits(g, w, h, sel, frame)
	return lines
}

// Hit is where an environment was drawn, for the mouse. More marks the
// "+N more" line; its ID is the hidden multidev a click scrolls to.
type Hit struct {
	ID         string
	X, Y, W, H int
	More       bool
}

// RenderHits is Render plus where each environment landed.
func RenderHits(g Graph, w, h int, sel string, frame int) ([]string, []Hit) {
	cv := newCanvas(w, h)
	s := newStyles(cv)
	if len(g.Main) == 0 && len(g.Multidevs) == 0 {
		cv.text(0, 0, "No environments loaded.", w, s.muted)
		return cv.lines(), nil
	}
	if w >= MinWideWidth {
		renderWide(cv, s, g, sel, frame)
	} else {
		renderNarrow(cv, s, g, sel, frame)
	}
	return cv.lines(), cv.hits
}

func (cv *canvas) hit(id string, x, y, w, h int) {
	cv.hits = append(cv.hits, Hit{ID: id, X: x, Y: y, W: w, H: h})
}

// nextHidden is the multidev a click on "+N more" selects: the first one
// below the window, or the first one when the window is at the end.
func nextHidden(mds, shown []Node) string {
	if len(shown) == 0 {
		return mds[0].ID
	}
	last := indexOf(mds, shown[len(shown)-1].ID)
	if last+1 >= len(mds) {
		return mds[0].ID
	}
	return mds[last+1].ID
}

const (
	boxH   = 6 // main boxes: border + 4 lines
	mdBoxH = 4 // multidev boxes: border + 2 lines
)

// drawNode draws a main environment box with its four content lines.
func drawNode(cv *canvas, s styles, n Node, x, y, w int, selected bool, frame int) {
	b, bst := rounded, s.state(n.State)
	if n.State == OK || n.State == Unknown || n.State == Waiting {
		bst = s.muted
	}
	if n.State == Running {
		b = heavy
	}
	tst := s.bold
	if selected {
		b, bst, tst = double, s.accent, s.accent
	}
	cv.box(x, y, w, boxH, b, bst, n.ID, tst)
	cv.hit(n.ID, x, y, w, boxH)
	inner := x + w - 2
	cx := x + 2
	sym := n.State.Symbol()
	if n.State == Running {
		sym = string([]rune("◐◓◑◒")[frame%4])
	}
	cv.text(cx, y+1, sym+" "+n.Status, inner, s.state(n.State))
	mode := s.muted
	if n.Mode == "sftp" {
		mode = s.sftp
	}
	cv.text(cx, y+2, n.Mode, inner, mode)
	if n.Extra != "" {
		cv.text(cx+len([]rune(n.Mode))+1, y+2, n.Extra, inner, s.sftp)
	}
	cv.text(cx, y+3, n.Head, inner, s.text)
	if n.State == Running && n.Progress >= 0 {
		barW := max(inner-cx-len([]rune(n.When))-1, 4)
		fill := int(n.Progress*float64(barW) + 0.5)
		cv.text(cx, y+4, strings.Repeat("█", fill), cx+fill, s.warn)
		cv.text(cx+fill, y+4, strings.Repeat("░", barW-fill), cx+barW, s.muted)
		cv.text(cx+barW+1, y+4, n.When, inner, s.muted)
	} else {
		cv.text(cx, y+4, n.When, inner, s.muted)
	}
}

// drawMultidev draws a compact multidev box.
func drawMultidev(cv *canvas, s styles, n Node, x, y, w int, selected bool, frame int) {
	b, bst, tst := rounded, s.muted, s.bold
	switch n.State {
	case Running:
		b, bst = heavy, s.warn
	case Failed:
		bst = s.fail
	case Landed:
		bst = s.ok
	}
	if selected {
		b, bst, tst = double, s.accent, s.accent
	}
	cv.box(x, y, w, mdBoxH, b, bst, n.ID, tst)
	cv.hit(n.ID, x, y, w, mdBoxH)
	sym := n.State.Symbol()
	if n.State == Running {
		sym = string([]rune("◐◓◑◒")[frame%4])
	}
	cv.text(x+2, y+1, sym+" "+n.Status, x+w-2, s.state(n.State))
	mode := s.muted
	if n.Mode == "sftp" {
		mode = s.sftp
	}
	line := n.Mode
	if n.Extra != "" {
		line += " " + n.Extra
	}
	cv.text(x+2, y+2, line, x+w-2, mode)
}

func edgeLabel(e Edge) (string, bool) {
	switch {
	case e.Pending < 0:
		return "…", false
	case e.Pending == 0:
		return theme.SymSuccess, true
	}
	return fmt.Sprintf("%s%d", theme.SymAhead, e.Pending), false
}

// window picks which multidevs fit in n slots, keeping the selected one
// visible. more is how many are hidden.
func window(mds []Node, n int, sel string) (shown []Node, more int) {
	if n <= 0 {
		return nil, len(mds)
	}
	if len(mds) <= n {
		return mds, 0
	}
	slots := n - 1 // one row for "+N more"
	start := 0
	for i, m := range mds {
		if m.ID == sel && i >= slots {
			start = i - slots + 1
		}
	}
	shown = mds[start : start+slots]
	return shown, len(mds) - slots
}

func renderWide(cv *canvas, s styles, g Graph, sel string, frame int) {
	w, h := cv.w, cv.h
	mdW, trunk := 0, 0
	if len(g.Multidevs) > 0 {
		mdW, trunk = min(max(w/6, 18), 24), 5
	}
	const gap = 8
	n := max(len(g.Main), 1)
	bw := min((w-mdW-trunk-gap*(n-1)-1)/n, 36)
	x0 := mdW + trunk
	y := 1
	// Content clones between main environments arc over the boxes, so they
	// need two rows above them.
	var flows []Flow
	for _, f := range g.Flows {
		if indexOf(g.Main, f.From) >= 0 && indexOf(g.Main, f.To) >= 0 {
			flows = append(flows, f)
		}
	}
	if len(flows) > 0 {
		y = 3
	}
	mid := y + 2 // edges meet the main boxes on their second content line

	var paths []path
	xs := make([]int, len(g.Main))
	for i := range g.Main {
		xs[i] = x0 + i*(bw+gap)
	}
	// Hops between main environments.
	for i := 1; i < len(g.Main); i++ {
		e, ok := g.Edge(g.Main[i-1].ID, g.Main[i].ID)
		if !ok {
			continue
		}
		st := s.muted
		if e.Active {
			st = s.warn
		} else if e.Pending > 0 {
			st = s.warn
		}
		from, to := xs[i-1]+bw, xs[i]-1
		paths = append(paths, path{cells: polyline([2]int{from, mid}, [2]int{to, mid}), st: st, heavy: e.Active, active: e.Active})
		label, okSym := edgeLabel(e)
		lst := s.warn
		if okSym {
			lst = s.ok
		} else if e.Pending < 0 {
			lst = s.muted
		}
		lx := from + (to-from+1-len([]rune(label)))/2
		cv.text(lx, mid-1, label, to+1, lst)
	}
	// Multidevs feed dev through a shared trunk.
	var devX = -1
	for i, m := range g.Main {
		if m.ID == "dev" {
			devX = xs[i]
		}
	}
	if mdW > 0 {
		cv.text(0, 0, "MULTIDEVS", mdW, s.muted)
		slots := max((h-1)/mdBoxH, 0)
		shown, more := window(g.Multidevs, slots, sel)
		tx := mdW + 1
		for i, m := range shown {
			my := 1 + i*mdBoxH
			ry := my + 1
			if devX >= 0 {
				e, _ := g.Edge(m.ID, "dev")
				st := s.muted
				if e.Pending > 0 || e.Active {
					st = s.warn
				}
				pts := polyline([2]int{mdW, ry}, [2]int{tx, ry}, [2]int{tx, mid}, [2]int{devX - 1, mid})
				if ry == mid {
					pts = polyline([2]int{mdW, ry}, [2]int{devX - 1, mid})
				}
				paths = append(paths, path{cells: pts, st: st, heavy: e.Active, active: e.Active})
			}
		}
		cv.drawPaths(paths)
		for i, m := range shown {
			drawMultidev(cv, s, m, 0, 1+i*mdBoxH, mdW, m.ID == sel, frame)
		}
		if more > 0 {
			cv.text(1, 1+len(shown)*mdBoxH, fmt.Sprintf("+%d more (←/→ to scroll)", more), mdW+trunk, s.muted)
			cv.hits = append(cv.hits, Hit{ID: nextHidden(g.Multidevs, shown), X: 1, Y: 1 + len(shown)*mdBoxH, W: mdW + trunk - 1, H: 1, More: true})
		}
	} else {
		cv.drawPaths(paths)
	}
	var arcs []path
	for _, f := range flows {
		sx := xs[indexOf(g.Main, f.From)] + bw/2
		tx := xs[indexOf(g.Main, f.To)] + bw/2
		arcs = append(arcs, path{cells: polyline([2]int{sx, y - 1}, [2]int{sx, 1}, [2]int{tx, 1}, [2]int{tx, y - 1}),
			st: s.flow, heavy: f.Active, active: f.Active})
		lo, hi := min(sx, tx), max(sx, tx)
		cv.text(lo+max((hi-lo-len([]rune(f.Label)))/2, 1), 0, f.Label, hi, s.flow)
	}
	cv.drawPaths(arcs)
	for i, m := range g.Main {
		drawNode(cv, s, m, xs[i], y, bw, m.ID == sel, frame)
	}
	cv.drawDots(paths, frame, s.dot)
	cv.drawDots(arcs, frame, s.flow)
}

func indexOf(nodes []Node, id string) int {
	for i, n := range nodes {
		if n.ID == id {
			return i
		}
	}
	return -1
}

func renderNarrow(cv *canvas, s styles, g Graph, sel string, frame int) {
	w, h := cv.w, cv.h
	bw := min(w, 52)
	y := 0
	needMain := len(g.Main)*boxH + max(len(g.Main)-1, 0)*3
	if len(g.Multidevs) > 0 {
		rows := max(min(len(g.Multidevs), h-needMain-1), 0)
		cv.text(0, y, "MULTIDEVS → dev", bw, s.muted)
		y++
		shown, more := window(g.Multidevs, rows, sel)
		for _, m := range shown {
			marker, st := "  ", s.state(m.State)
			if m.ID == sel {
				marker, st = theme.SymCursor+" ", s.accent
			}
			line := marker + m.State.Symbol() + " " + m.ID + "  " + m.Status
			if m.Extra != "" {
				line += " · " + m.Extra
			}
			cv.text(0, y, line, bw, st)
			cv.hit(m.ID, 0, y, bw, 1)
			y++
		}
		if more > 0 {
			cv.text(2, y, fmt.Sprintf("+%d more", more), bw, s.muted)
			cv.hits = append(cv.hits, Hit{ID: nextHidden(g.Multidevs, shown), X: 2, Y: y, W: bw - 2, H: 1, More: true})
			y++
		}
	}
	var paths []path
	cx := bw / 2
	for i, m := range g.Main {
		drawNode(cv, s, m, 0, y, bw, m.ID == sel, frame)
		y += boxH
		if i == len(g.Main)-1 {
			break
		}
		e, ok := g.Edge(m.ID, g.Main[i+1].ID)
		if ok {
			st := s.muted
			if e.Active || e.Pending > 0 {
				st = s.warn
			}
			paths = append(paths, path{cells: polyline([2]int{cx, y}, [2]int{cx, y + 2}), st: st, heavy: e.Active, active: e.Active})
			label, okSym := edgeLabel(e)
			lst := s.warn
			if okSym {
				lst = s.ok
			}
			cv.text(cx+2, y+1, label, bw, lst)
		}
		y += 3
	}
	cv.drawPaths(paths)
	cv.drawDots(paths, frame, s.dot)
}
