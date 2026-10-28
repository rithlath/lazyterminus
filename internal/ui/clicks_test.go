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
	time.Sleep(20 * time.Millisecond)
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
