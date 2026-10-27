package ui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/rithlath/lazyterminus/internal/ui/theme"
)

// renderTable lays out rows as aligned text columns; the first row is the
// header. Cells are single-line.
func renderTable(rows [][]string) []string {
	if len(rows) == 0 {
		return nil
	}
	widths := make([]int, 0)
	for _, r := range rows {
		for i, c := range r {
			c = oneLine(c)
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], min(lipgloss.Width(c), 60))
		}
	}
	out := make([]string, 0, len(rows))
	for ri, r := range rows {
		var b strings.Builder
		for i, c := range r {
			c = oneLine(c)
			if lipgloss.Width(c) > widths[i] {
				c = c[:max(widths[i]-1, 0)] + "…"
			}
			b.WriteString(c + strings.Repeat(" ", max(widths[i]-lipgloss.Width(c), 0)))
			if i < len(r)-1 {
				b.WriteString("  ")
			}
		}
		line := strings.TrimRight(b.String(), " ")
		if ri == 0 {
			line = theme.Bold.Render(line)
		}
		out = append(out, line)
	}
	return out
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
