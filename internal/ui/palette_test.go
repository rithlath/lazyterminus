package ui

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestPaletteEnvScopedFillsSiteEnv(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev"}, 130, 40)
	h.waitText("[3] acme-web.dev")
	h.keys(":")
	h.waitText("Context: acme-web.dev")
	h.typeText("env:info")
	h.waitText("◆ env:info")
	h.keys("enter")
	h.waitText("site_env = acme-web.dev")
	h.waitText("terminus env:info acme-web.dev --format=json")
	h.keys("enter")
	h.waitText("─ Output ─")
	h.waitText("connection_mode")
	h.waitText("dev-acme-web.pantheonsite.io")
	h.noCalls()
}

func TestPaletteSiteScopedTable(t *testing.T) {
	h := start(t, Options{Site: "acme-web"}, 130, 40)
	h.waitText("feedback")
	h.keys(":")
	h.waitText("Context: acme-web")
	h.typeText("env:list")
	h.keys("enter")
	h.waitText("site_id = acme-web")
	h.keys("enter")
	h.waitText("CONNECTION_MODE")
	h.waitText("proj-40-acme-web.pantheonsite.io")
}

func TestPaletteRequiredArgs(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev"}, 130, 40)
	h.waitText("[3] acme-web.dev")
	h.keys(":")
	h.typeText("domain:add")
	h.keys("enter")
	h.waitText("Run domain:add")
	h.keys("enter")
	h.waitText("domain is required")
	h.noCalls()
}

func TestPalettePluginCommands(t *testing.T) {
	h := start(t, Options{Site: "acme-web"}, 130, 40)
	h.waitText("feedback")
	h.keys(":")
	h.typeText("build:env:list")
	h.waitText("(plugin)")
	h.keys("enter", "enter")
	// Plugin commands aren't on the known read-only list, so even a
	// list-like one gets the standard yes/no confirmation.
	h.waitText("terminus build:env:list acme-web --yes")
	h.waitText("Run it? y/n")
	h.keys("y")
	h.waitCalls(1)
	h.waitText("─ Output ─")
	h.keys("esc", ":")
	h.typeText("build:env:delete:pr")
	h.keys("enter")
	h.waitText("terminus build:env:delete:pr acme-web --yes")
	h.keys("enter")
	h.waitText("Run it? y/n") // unknown mutating command: standard yes/no
	h.keys("y")
	calls := h.waitCalls(2)
	if !slices.Equal(calls[1], []string{"build:env:delete:pr", "acme-web", "--yes"}) {
		t.Errorf("calls = %v", calls)
	}
}

func TestPaletteExtraDestructiveIsTyped(t *testing.T) {
	cfg := testConfig()
	cfg.ExtraDestructiveCommands = []string{"build:env:delete:*"}
	h := start(t, Options{Site: "acme-web", Config: cfg}, 130, 40)
	h.waitText("feedback")
	h.keys(":")
	h.typeText("build:env:delete:pr")
	h.keys("enter", "enter")
	h.waitText("Type acme-web to confirm")
	h.keys("esc")
	h.noCalls()
}

func TestPaletteDeployLiveIsTyped(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.live"}, 130, 40)
	h.waitText("[3] acme-web.live")
	h.keys(":")
	h.typeText("deploy") // alias
	h.waitText("◆ env:deploy")
	h.keys("enter")
	h.waitText("site_env = acme-web.live")
	h.keys("enter")
	h.waitText("Type acme-web.live to confirm")
	h.waitText("deploys to production")
	h.keys("esc")
	h.noCalls()
}

func TestPaletteCloneIntoLiveBlocked(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev"}, 130, 40)
	h.waitText("[3] acme-web.dev")
	h.keys(":")
	h.typeText("env:clone-content")
	h.keys("enter")
	h.typeText("live")
	h.waitText("terminus env:clone-content acme-web.dev live --yes")
	h.keys("enter")
	h.waitText("cloning content into live is not allowed")
	h.noCalls()
}

func TestPaletteInteractiveDrush(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev"}, 130, 40)
	h.waitText("[3] acme-web.dev")
	h.keys(":")
	h.typeText("remote:drush")
	h.keys("enter")
	h.typeText("cr")
	h.waitText("terminus remote:drush acme-web.dev -- cr")
	h.keys("enter")
	h.waitText("terminus env:wake acme-web.dev")
	h.keys("y")
	calls := h.waitCalls(2)
	if calls[0][0] != "env:wake" || !slices.Equal(calls[1], []string{"remote:drush", "acme-web.dev", "--", "cr"}) {
		t.Errorf("calls = %v", calls)
	}
}

func TestPaletteSelectOptions(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.live"}, 130, 40)
	h.waitText("[3] acme-web.live")
	h.keys(":")
	h.typeText("backup:list")
	h.keys("enter")
	h.waitText("--element")
	h.keys("right", "right") // (default) → all → code
	h.waitText("terminus backup:list acme-web.live --element=code --format=json")
	h.keys("enter")
	h.waitText("FILE")
	h.noCalls()
}

// Every command in the mock catalog, including plugin commands, opens an
// argument form from the palette.
func TestEveryCommandReachableFromPalette(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev"}, 160, 50)
	h.waitText("[3] acme-web.dev")
	m := h.final()
	cat := m.catalog
	if cat == nil {
		// The catalog loads in the background; load it synchronously.
		msg := loadCatalog(m.opts.Runner, t.TempDir(), false)()
		mm, _ := m.Update(msg)
		m = mm.(Model)
		cat = m.catalog
	}
	if cat == nil {
		t.Fatal("catalog did not load")
	}
	seen := 0
	for _, c := range cat.Commands {
		if c.Hidden {
			continue
		}
		mm := m
		mm.openPalette()
		for _, r := range c.Name {
			next, _ := mm.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
			mm = next.(Model)
		}
		next, _ := mm.Update(keyMsg("enter"))
		mm = next.(Model)
		f, ok := mm.modal.(*form)
		if !ok || f.title != "Run "+c.Name {
			t.Errorf("%s: palette did not open its form (modal %T)", c.Name, mm.modal)
			continue
		}
		seen++
	}
	if seen < 159 {
		t.Errorf("only %d commands reachable", seen)
	}
	if _, ok := cat.Get("build:env:list"); !ok {
		t.Error("plugin commands missing")
	}
}
