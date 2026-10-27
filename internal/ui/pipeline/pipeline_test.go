package pipeline

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func sample(active bool) Graph {
	return Graph{
		Multidevs: []Node{
			{ID: "feedback", Mode: "sftp", State: Waiting, Status: "1 ahead", Extra: "✎2 uncommitted"},
			{ID: "d11", Mode: "git", State: Waiting, Status: "11 ahead"},
			{ID: "annual", Mode: "git", State: OK, Status: "in sync"},
		},
		Main: []Node{
			{ID: "dev", Mode: "git", State: OK, Status: "in sync", Head: "a1c9e0f PROJ-77 Add hero banner", When: "committed 2h ago", Progress: -1},
			{ID: "test", Mode: "git", State: Running, Status: "deploying 3/6", Head: "c3e1a2b PROJ-75 Bump core", When: "34s", Progress: 0.5},
			{ID: "live", Mode: "git", State: Landed, Status: "landed 1 commit", Head: "3963b4d build artifacts", When: "deployed just now", Progress: -1},
		},
		Edges: []Edge{
			{From: "feedback", To: "dev", Pending: 1},
			{From: "d11", To: "dev", Pending: 11},
			{From: "annual", To: "dev", Pending: 0},
			{From: "dev", To: "test", Pending: 2, Active: active},
			{From: "test", To: "live", Pending: 0},
		},
	}
}

func plain(lines []string) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Strip(l)
	}
	return strings.Join(out, "\n")
}

func TestRenderFitsExactly(t *testing.T) {
	for _, sz := range [][2]int{{140, 30}, {120, 20}, {96, 24}, {95, 40}, {80, 30}, {60, 12}, {30, 6}, {200, 50}} {
		for _, g := range []Graph{sample(true), {Main: sample(false).Main}, {}} {
			lines := Render(g, sz[0], sz[1], "test", 3)
			if len(lines) != sz[1] {
				t.Fatalf("%v: %d lines", sz, len(lines))
			}
			for i, l := range lines {
				if w := ansi.StringWidth(l); w != sz[0] {
					t.Fatalf("%v line %d width %d: %q", sz, i, w, ansi.Strip(l))
				}
			}
		}
	}
}

func TestWideLayout(t *testing.T) {
	out := plain(Render(sample(false), 140, 24, "test", 0))
	for _, want := range []string{"MULTIDEVS", "╭─ feedback", "╭─ dev ", "╔═ test ", "╭─ live ", "↑2", "✓", "▶", "↑ 1 ahead",
		"◐ deploying 3/6", "✓ landed 1 commit", "a1c9e0f PROJ-77", "✎2 uncommitted", "█", "░"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "─●") || strings.Contains(out, "●─") || strings.Contains(out, "━") {
		t.Errorf("no dots or heavy lines expected on idle hops:\n%s", out)
	}
}

func TestActiveHopAnimates(t *testing.T) {
	f0 := plain(Render(sample(true), 140, 24, "dev", 0))
	f1 := plain(Render(sample(true), 140, 24, "dev", 1))
	if !strings.Contains(f0, "━●") || f0 == f1 {
		t.Fatalf("dots should flow on the active hop:\n%s\n---\n%s", f0, f1)
	}
	if !strings.Contains(f0, "━") {
		t.Error("active hop should be drawn heavy (visible without color)")
	}
	if !sample(true).Animated() || (Graph{Main: []Node{{ID: "dev", State: OK}}}).Animated() {
		t.Error("Animated() wrong")
	}
}

func TestNarrowLayout(t *testing.T) {
	out := plain(Render(sample(false), 70, 40, "feedback", 0))
	for _, want := range []string{"MULTIDEVS → dev", "› ↑ feedback  1 ahead · ✎2 uncommitted", "╭─ dev ", "▼", "↑2", "┏━ test "} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestMultidevWindowKeepsSelectionVisible(t *testing.T) {
	var mds []Node
	for i := range 12 {
		mds = append(mds, Node{ID: fmt.Sprintf("md-%02d", i)})
	}
	shown, more := window(mds, 4, "md-10")
	if len(shown) != 3 || more != 9 || shown[len(shown)-1].ID != "md-10" {
		t.Errorf("shown=%v more=%d", shown, more)
	}
	if shown, more := window(mds[:3], 4, ""); len(shown) != 3 || more != 0 {
		t.Errorf("all fit: %v %d", shown, more)
	}
}

func TestContentFlowArc(t *testing.T) {
	g := sample(false)
	g.Flows = []Flow{{From: "live", To: "dev", Label: "copying database live → dev", Active: true}}
	out := plain(Render(g, 140, 24, "dev", 0))
	for _, want := range []string{"copying database live → dev", "▼"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if !g.Animated() {
		t.Error("an active flow should animate")
	}
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[3], "╭─ dev") && !strings.Contains(lines[3], "╔═ dev") {
		t.Errorf("main boxes should move down to make room for the arc:\n%s", out)
	}
	f0, f1 := plain(Render(g, 140, 24, "dev", 0)), plain(Render(g, 140, 24, "dev", 1))
	if f0 == f1 {
		t.Error("dots should flow along the arc")
	}
	// A flow into a multidev isn't drawn as an arc (the box says it instead).
	g.Flows = []Flow{{From: "live", To: "feedback", Label: "x", Active: true}}
	if out := plain(Render(g, 140, 24, "dev", 0)); strings.Contains(out, "▼") {
		t.Errorf("unexpected arc:\n%s", out)
	}
}
