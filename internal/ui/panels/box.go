// Package panels renders lazyterminus's panels. Renderers are pure
// functions of state and size; the root model owns all state.
package panels

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rithlath/lazyterminus/internal/ui/theme"
)

// Box draws a rounded border of exactly w×h cells with title set into the
// top edge and an optional footer in the bottom edge. Content lines are
// truncated and padded so nothing overflows.
func Box(title string, lines []string, w, h int, focused bool, footer string) string {
	if w < 4 || h < 2 {
		return ""
	}
	bs, ts := theme.Border, theme.Title
	if focused {
		bs, ts = theme.FocusBord, theme.FocusTitle
	}
	inner := w - 2

	title = ansi.Truncate(title, max(inner-3, 0), "…")
	top := bs.Render("╭─") + ts.Render(title) + bs.Render(strings.Repeat("─", max(inner-1-lipgloss.Width(title), 0))+"╮")

	footer = ansi.Truncate(footer, max(inner-3, 0), "…")
	fw := lipgloss.Width(footer)
	bottom := bs.Render("╰"+strings.Repeat("─", max(inner-1-fw, 0))) + theme.MutedText.Render(footer) + bs.Render("─╯")
	if fw == 0 {
		bottom = bs.Render("╰" + strings.Repeat("─", inner) + "╯")
	}

	var b strings.Builder
	b.WriteString(top)
	side := bs.Render("│")
	for i := range h - 2 {
		line := ""
		if i < len(lines) {
			line = Fit(lines[i], inner-2)
		} else {
			line = strings.Repeat(" ", max(inner-2, 0))
		}
		b.WriteString("\n" + side + " " + line + " " + side)
	}
	b.WriteString("\n" + bottom)
	return b.String()
}

// Fit truncates s to exactly width cells (padding with spaces).
func Fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = ansi.Truncate(s, width, "…")
	if pad := width - lipgloss.Width(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// List tracks a cursor and scroll offset over rows, some of which may be
// unselectable (dividers).
type List struct {
	Cursor int
	Offset int
}

// Clamp keeps the cursor in range and on a selectable row.
func (l *List) Clamp(selectable []bool) {
	n := len(selectable)
	if n == 0 {
		l.Cursor, l.Offset = 0, 0
		return
	}
	l.Cursor = min(max(l.Cursor, 0), n-1)
	if !selectable[l.Cursor] {
		if !l.step(selectable, 1) {
			l.step(selectable, -1)
		}
	}
}

// Move moves the cursor by delta over selectable rows.
func (l *List) Move(selectable []bool, delta int) {
	dir := 1
	if delta < 0 {
		dir, delta = -1, -delta
	}
	for range delta {
		l.step(selectable, dir)
	}
}

func (l *List) step(selectable []bool, dir int) bool {
	for i := l.Cursor + dir; i >= 0 && i < len(selectable); i += dir {
		if selectable[i] {
			l.Cursor = i
			return true
		}
	}
	return false
}

// Window adjusts Offset so the cursor is visible in height rows and returns
// the visible range [start, end).
func (l *List) Window(n, height int) (int, int) {
	if height <= 0 || n == 0 {
		return 0, 0
	}
	if l.Cursor < l.Offset {
		l.Offset = l.Cursor
	}
	if l.Cursor >= l.Offset+height {
		l.Offset = l.Cursor - height + 1
	}
	l.Offset = min(max(l.Offset, 0), max(n-height, 0))
	return l.Offset, min(l.Offset+height, n)
}

// Row renders one list row with a cursor marker. Selected rows in the
// focused panel are highlighted; in unfocused panels they are bold. The "›"
// marker shows selection without color.
func Row(text string, width int, selected, focused bool) string {
	if !selected {
		return "  " + Fit(text, width-2)
	}
	line := theme.SymCursor + " " + Fit(ansi.Strip(text), width-2)
	if focused {
		return theme.Selected.Render(line)
	}
	return theme.SelectedBl.Render(theme.SymCursor+" ") + Fit(text, width-2)
}
