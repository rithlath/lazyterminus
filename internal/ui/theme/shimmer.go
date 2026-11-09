package theme

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// shimmerBand is the highlight that sweeps across a Shimmer line, brightest
// in the middle. Bold carries the motion when there is no color.
var shimmerBand = []lipgloss.Style{
	lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD98A")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#FFF1CF")).Bold(true),
	lipgloss.NewStyle().Foreground(White).Bold(true),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#FFF1CF")).Bold(true),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD98A")),
}

// shimmerGap is how many frames the band spends off the end of the line
// before it comes round again.
const shimmerGap = 6

// Shimmer renders s (plain text) in the running color with a bright band
// moving across it, one cell per frame, so a line that is in progress
// visibly is.
func Shimmer(s string, frame int) string {
	rs := []rune(s)
	n, bw := len(rs), len(shimmerBand)
	if n == 0 {
		return ""
	}
	start := frame%(n+bw+shimmerGap) - bw // band's left edge; may be off either end
	var b strings.Builder
	if start > 0 {
		b.WriteString(Warn.Render(string(rs[:min(start, n)])))
	}
	for i, st := range shimmerBand {
		if p := start + i; p >= 0 && p < n {
			b.WriteString(st.Render(string(rs[p])))
		}
	}
	if end := start + bw; end < n {
		b.WriteString(Warn.Render(string(rs[max(end, 0):])))
	}
	return b.String()
}
