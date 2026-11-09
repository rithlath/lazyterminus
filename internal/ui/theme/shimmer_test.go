package theme

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestShimmerKeepsText(t *testing.T) {
	const s = "running drush cr on dev"
	for f := range 60 {
		if got := ansi.Strip(Shimmer(s, f)); got != s {
			t.Fatalf("frame %d: got %q, want %q", f, got, s)
		}
	}
	if Shimmer("", 3) != "" {
		t.Fatal("empty text should render empty")
	}
}
