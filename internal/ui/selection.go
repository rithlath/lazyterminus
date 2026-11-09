package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rithlath/lazyterminus/internal/ui/theme"
)

// Text selection: drag with the left button to highlight text, release to
// copy it. A left click therefore acts on release, and only if the mouse
// didn't move; a right click still acts on press. The selection stays
// inside the box (panel, pipeline, modal) the drag started in, so copying
// a panel's lines doesn't pick up its borders or the panel beside it.

// mouseState follows one left-button gesture. The frame's mouse handler
// runs on the program's event loop, in event order, so it owns this state
// and hands Update finished snapshots (whose commands may arrive out of
// order; seq sorts that out).
type mouseState struct {
	pressed  bool
	dragging bool
	anchor   [2]int
	clip     rect
	pending  *clickMsg // the click to run on release if there was no drag
	seq      int
}

// selection is a highlighted range: rows y0..y1 of clip, starting at x0
// on the first row and ending at x1 (inclusive) on the last.
type selection struct {
	clip           rect
	x0, y0, x1, y1 int
}

type selectMsg struct {
	sel   selection
	done  bool // released: copy it
	clear bool // a new press: drop the old highlight
	seq   int
}

type toastExpiredMsg struct{ seq int }

// press records a left press; the click itself waits for release.
func (s *mouseState) press(x, y int, c *clickMsg, boxes []rect, width, height int) {
	s.pressed, s.dragging, s.pending = true, false, c
	s.clip = rect{0, 0, width, height}
	for _, b := range boxes { // the smallest box around the press
		in := rect{b.x + 1, b.y + 1, b.w - 2, b.h - 2}
		if in.w > 0 && in.h > 0 && x >= b.x && x < b.x+b.w && y >= b.y && y < b.y+b.h && in.w*in.h < s.clip.w*s.clip.h {
			s.clip = in
		}
	}
	s.anchor = s.clip.clamp(x, y)
}

func (s *mouseState) span(x, y int) selection {
	p := s.clip.clamp(x, y)
	a := s.anchor
	if p[1] < a[1] || p[1] == a[1] && p[0] < a[0] {
		a, p = p, a
	}
	return selection{clip: s.clip, x0: a[0], y0: a[1], x1: p[0], y1: p[1]}
}

func (r rect) clamp(x, y int) [2]int {
	return [2]int{min(max(x, r.x), r.x+r.w-1), min(max(y, r.y), r.y+r.h-1)}
}

// cols is the selected column range [from, to) on row y.
func (s selection) cols(y int) (int, int) {
	from, to := s.clip.x, s.clip.x+s.clip.w
	if y == s.y0 {
		from = s.x0
	}
	if y == s.y1 {
		to = s.x1 + 1
	}
	return from, to
}

// text is what the selection covers in frame, one line per row, with
// trailing spaces dropped.
func (s selection) text(frame string) string {
	lines := strings.Split(frame, "\n")
	var out []string
	for y := s.y0; y <= s.y1 && y < len(lines); y++ {
		from, to := s.cols(y)
		out = append(out, strings.TrimRight(ansi.Strip(ansi.Cut(lines[y], from, to)), " "))
	}
	return strings.Trim(strings.Join(out, "\n"), "\n")
}

var selStyle = lipgloss.NewStyle().Reverse(true)

// highlight draws the selection over frame.
func (s selection) highlight(frame string) string {
	lines := strings.Split(frame, "\n")
	for y := s.y0; y <= s.y1 && y < len(lines); y++ {
		from, to := s.cols(y)
		l := lines[y]
		w := ansi.StringWidth(l)
		if from >= w {
			continue
		}
		mid := ansi.Strip(ansi.Cut(l, from, to))
		lines[y] = ansi.Cut(l, 0, from) + selStyle.Render(mid) + ansi.Cut(l, min(to, w), w)
	}
	return strings.Join(lines, "\n")
}

func (m Model) onSelect(msg selectMsg) (tea.Model, tea.Cmd) {
	if msg.seq < m.selSeq {
		return m, nil // a stale drag update
	}
	m.selSeq = msg.seq
	if msg.clear {
		m.sel = nil
		return m, nil
	}
	sel := msg.sel
	m.sel = &sel
	if !msg.done {
		return m, nil
	}
	mm := m
	mm.sel, mm.zones = nil, nil
	text := sel.text(mm.render())
	if text == "" {
		m.sel = nil
		return m, nil
	}
	return m, m.copyText(text, describe(text))
}

// describe names copied text for the status line and toast.
func describe(s string) string {
	if n := strings.Count(s, "\n") + 1; n > 1 {
		return fmt.Sprintf("%d lines", n)
	}
	if ansi.StringWidth(s) > 40 {
		return fmt.Sprintf("%q", ansi.Truncate(s, 39, "…"))
	}
	return fmt.Sprintf("%q", s)
}

const toastFor = 2 * time.Second

func (m *Model) showToast(s string, isErr bool) tea.Cmd {
	m.toastSeq++
	m.toast, m.toastErr = s, isErr
	seq := m.toastSeq
	return tea.Tick(toastFor, func(time.Time) tea.Msg { return toastExpiredMsg{seq} })
}

func (m *Model) onToastExpired(msg toastExpiredMsg) {
	if msg.seq == m.toastSeq {
		m.toast, m.sel = "", nil
	}
}

// withToast draws the toast in the bottom-right corner of the body.
func (m Model) withToast(frame string, bodyH int) string {
	if m.toast == "" {
		return frame
	}
	st := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).BorderForeground(theme.Green)
	text := theme.OK.Render(theme.SymSuccess) + " " + m.toast
	if m.toastErr {
		st = st.BorderForeground(theme.Amber)
		text = theme.Warn.Render("!") + " " + m.toast
	}
	box := st.Render(ansi.Truncate(text, max(m.width-6, 1), "…"))
	x := max(m.width-lipgloss.Width(box)-2, 0)
	y := max(bodyH-lipgloss.Height(box)-1, 0)
	return lipgloss.NewCompositor(lipgloss.NewLayer(frame), lipgloss.NewLayer(box).X(x).Y(y).Z(2)).Render()
}
