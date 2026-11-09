package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/rithlath/lazyterminus/internal/ui/theme"
)

func TestSelectionTextAndHighlight(t *testing.T) {
	frame := "│ " + theme.Key.Render("alpha") + " beta   │\n│ gamma delta  │\n│ eps          │"
	s := selection{clip: rect{1, 0, 14, 3}, x0: 2, y0: 0, x1: 5, y1: 2}
	if got, want := s.text(frame), "alpha beta\n gamma delta\n eps"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
	hl := s.highlight(frame)
	if ansi.Strip(hl) != ansi.Strip(frame) {
		t.Errorf("highlight changed the text:\n%s", ansi.Strip(hl))
	}
	if !strings.Contains(hl, "\x1b[7m") {
		t.Errorf("no reverse video in %q", hl)
	}
}

func TestDescribeCopied(t *testing.T) {
	for in, want := range map[string]string{
		"dev":                   `"dev"`,
		"a\nb\nc":               "3 lines",
		strings.Repeat("x", 60): `"` + strings.Repeat("x", 38) + `…"`,
	} {
		if got := describe(in); got != want {
			t.Errorf("describe(%q) = %q, want %q", in, got, want)
		}
	}
}
