package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// clickAt sends a mouse click at screen cell x, y.
// Bubble Tea hit-tests against the last frame it flushed, which can trail
// h.screen() by a frame, so let one flush happen first.
func (h *harness) clickAt(x, y int, b tea.MouseButton) {
	time.Sleep(100 * time.Millisecond)
	h.tm.Send(tea.MouseClickMsg{X: x, Y: y, Button: b})
	h.tm.Send(tea.MouseReleaseMsg{X: x, Y: y, Button: b})
	time.Sleep(20 * time.Millisecond)
}

// drag presses at (x0, y0), moves to (x1, y1) and releases there.
func (h *harness) drag(x0, y0, x1, y1 int) {
	time.Sleep(100 * time.Millisecond)
	h.tm.Send(tea.MouseClickMsg{X: x0, Y: y0, Button: tea.MouseLeft})
	h.tm.Send(tea.MouseMotionMsg{X: x1, Y: y1, Button: tea.MouseLeft})
	h.tm.Send(tea.MouseReleaseMsg{X: x1, Y: y1, Button: tea.MouseLeft})
	time.Sleep(20 * time.Millisecond)
}

// find is where text first appears on screen.
func (h *harness) find(text string) (x, y int) {
	h.t.Helper()
	for y, line := range strings.Split(h.screen(), "\n") {
		if i := strings.Index(line, text); i >= 0 {
			return ansi.StringWidth(line[:i]), y
		}
	}
	h.t.Fatalf("%q not on screen:\n%s", text, h.screen())
	return 0, 0
}

func TestDragSelectsAndCopiesText(t *testing.T) {
	copied := &opened{}
	h := start(t, Options{Site: "acme-web", Env: "acme-web.live", Clipboard: copied.open}, 140, 44)
	h.waitText("(primary)")
	x, y := h.find("https://www.acme-web.example.com (primary)")
	h.drag(x, y, x+len("https://www.acme-web.example.com")-1, y)
	h.waitText(`Copied "https://www.acme-web.example.com" to the clipboard`)
	if got := copied.last(); got != "https://www.acme-web.example.com" {
		t.Errorf("copied %q", got)
	}
	if h.root().focus != panelEnvs {
		t.Error("a drag should not run the click under it")
	}

	// Down a row, past the right edge: clipped to the Detail box (no
	// border, nothing from the panel beside it).
	h.drag(x, y, 139, y+1)
	h.waitFor("two lines copied", func(string) bool { return strings.Count(copied.last(), "\n") == 1 })
	for _, l := range strings.Split(copied.last(), "\n") {
		if strings.ContainsAny(l, "│╭╮╰╯") {
			t.Errorf("selection crossed a border: %q", l)
		}
	}
	if !strings.HasPrefix(copied.last(), "https://www.acme-web.example.com (primary)") {
		t.Errorf("copied %q", copied.last())
	}
	h.waitText("Copied 2 lines")
}

func TestClickWithoutDragStillClicks(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev"}, 130, 40)
	h.waitText("[3] acme-web.dev")
	x, y := h.find("live        git")
	h.tm.Send(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	time.Sleep(50 * time.Millisecond)
	if strings.Contains(h.screen(), "[3] acme-web.live") {
		t.Fatal("a left click acted before release")
	}
	h.tm.Send(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	h.waitText("[3] acme-web.live")
}

// clickOn clicks text on the first screen line that contains both anchor
// and text.
func (h *harness) clickOn(anchor, text string, b tea.MouseButton) {
	h.t.Helper()
	for y, line := range strings.Split(h.screen(), "\n") {
		if !strings.Contains(line, anchor) {
			continue
		}
		if i := strings.Index(line, text); i >= 0 {
			h.clickAt(ansi.StringWidth(line[:i]), y, b)
			return
		}
	}
	h.t.Fatalf("%q not on screen:\n%s", text, h.screen())
}

func (h *harness) click(text string) { h.t.Helper(); h.clickOn("", text, tea.MouseLeft) }

func TestClickRowSelectsDoubleClickOpensMenu(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev"}, 130, 40)
	h.waitText("[3] acme-web.dev")
	h.click("live        git")
	h.waitText("[3] acme-web.live")
	if h.root().focus != panelEnvs {
		t.Error("clicking an env row should focus Environments")
	}
	if strings.Contains(h.screen(), "─ acme-web.live ─") {
		t.Error("a single click opened the menu")
	}
	h.click("live        git")
	h.waitText("─ acme-web.live ─") // second click within the double-click window
	h.click("Clear cache")
	h.waitText("terminus env:clear-cache acme-web.live")
	h.noCalls()
	h.clickAt(0, 39, tea.MouseLeft) // outside the modal: cancel
	h.waitText("Cancelled: nothing was run.")
	h.noCalls()
}

func TestRightClickOpensMenuAndFooterConfirms(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev"}, 130, 40)
	h.waitText("[3] acme-web.dev")
	h.clickOn("", "dev         git", tea.MouseRight)
	h.waitText("─ acme-web.dev ─")
	h.click("Clear cache")
	h.waitText("Run it? y/n")
	h.noCalls()
	h.click("y confirm")
	h.waitCalls(1)
}

func TestClickSitesAndPanels(t *testing.T) {
	h := start(t, Options{Site: "acme-web"}, 130, 40)
	h.waitText("feedback")
	h.click("acme-portal")
	h.waitText("[2] Environments · acme-portal")
	h.click("[4] Workflows")
	h.waitFor("workflows focus", func(string) bool { return h.root().focus == panelWorkflows })
}

func TestClickKeyHint(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev"}, 130, 40)
	h.waitText("[3] acme-web.dev")
	h.click("c cache")
	h.waitText("Run it? y/n")
	h.click("n/esc cancel")
	h.waitText("Cancelled: nothing was run.")
	h.noCalls()
}

func TestClickPipelineBoxes(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.test"}, 140, 40)
	h.waitText("[3] acme-web.test")
	h.keys("p")
	h.waitText("╔═ test")
	h.click("─ live ─")
	h.waitText("Pipeline · acme-web · live")
	h.waitText("╔═ live")
	h.clickOn("", "═ live ═", tea.MouseRight)
	h.waitText("─ acme-web.live ─")
	h.keys("esc")
	h.waitText("╔═ live")
	h.click("more (←/→")
	h.waitFor("hidden multidev selected", func(s string) bool {
		m := h.root()
		e := m.selectedEnv()
		return e != "live" && e != "test" && e != "dev" && strings.Contains(s, "╔═ "+e)
	})
	h.click("p/esc back")
	h.waitText("[1] Sites")
}

func TestClickFormToggleAndPaletteEntry(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.test"}, 130, 40)
	h.waitText("↑2 ready to deploy from dev")
	h.keys("d")
	h.waitText("Deploying promotes ALL 2 commit(s)")
	h.waitText("[x] --cc")
	h.clickOn("Clear cache", "[x]", tea.MouseLeft)
	h.waitFor("cache toggle off", func(s string) bool { return strings.Contains(s, "[ ] --cc") })
	h.click("esc cancel")
	h.waitText("Cancelled.")
	h.keys(":")
	h.typeText("domain:add")
	h.waitText("domain:add")
	h.clickOn("◆", "domain:add", tea.MouseLeft)
	h.waitText("Run domain:add")
	h.noCalls()
}

func TestHintKey(t *testing.T) {
	for seg, want := range map[string]string{
		" enter run ": "enter", " n/esc cancel ": "n", " p/esc back ": "p", " any key to close ": "esc",
		" y yes ": "y", " 3/14 ": "", " ◆ fills dev ": "", " ←/→ choose ": "", " key runs directly ": "",
	} {
		if got := hintKey(seg); got != want {
			t.Errorf("hintKey(%q) = %q, want %q", seg, got, want)
		}
	}
}

func TestClickNarrowLayout(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev"}, 80, 50)
	h.waitText("[3] acme-web.dev")
	h.click("live        git")
	h.waitText("[3] acme-web.live")
	h.keys("p")
	h.waitText("MULTIDEVS → dev")
	h.click("─ dev ─")
	h.waitText("╔═ dev")
	h.click("feedback")
	h.waitText("Pipeline · acme-web · feedback")
}
