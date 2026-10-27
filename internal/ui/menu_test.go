package ui

import (
	"strings"
	"testing"
)

func TestEnterOpensEnvMenu(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.test"}, 130, 40)
	h.waitText("↑2 ready to deploy from dev")
	h.keys("enter")
	h.waitText("─ acme-web.test ─")
	h.waitText("d  Deploy 2 commits from dev")
	h.waitText("── not available here")
	h.waitText("m  Git/SFTP mode · dev and multidevs only")
	h.waitText("o  Commit SFTP changes · SFTP mode only")
	h.waitText("D  Delete multidev · multidevs only")
	h.keys("enter") // first item: deploy
	h.waitText("Deploying promotes ALL 2 commit(s)")
	h.keys("esc")
	h.noCalls()
}

func TestEnvMenuMultidevAndShortcuts(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.feedback"}, 130, 40)
	h.waitText("✎ 2 files")
	h.keys("enter")
	h.waitText("m  Switch to Git mode")
	h.waitText("o  Commit 2 changed files")
	h.waitText("D  Delete multidev feedback")
	h.waitText("d  Deploy · test and live only")
	h.keys("d") // disabled item's key: explains, stays open
	h.waitText("Deploy: test and live only")
	h.keys("o") // enabled item's key runs it
	h.waitText("Commit 2 changed file(s) on acme-web.feedback")
	h.keys("esc")
	h.noCalls()
}

func TestEnvMenuNavigationSkipsDisabled(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.live"}, 130, 40)
	h.waitText("[3] acme-web.live")
	h.keys("enter")
	h.waitText("Clone content from live into another environment")
	for range 30 {
		h.keys("j")
	}
	h.keys("enter") // last enabled item: the palette, never a disabled one
	h.waitText("Context: acme-web.live")
	h.keys("esc")
	h.noCalls()
}

func TestEnvMenuRunsWithConfirmation(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev"}, 130, 40)
	h.waitText("[3] acme-web.dev")
	h.keys("enter")
	h.waitText("› c  Clear cache") // deploy is unavailable on dev, so this comes first
	h.keys("enter")
	h.waitText("terminus env:clear-cache acme-web.dev")
	h.waitText("Run it? y/n")
	h.noCalls()
	h.keys("y")
	h.waitCalls(1)
}

func TestEnvMenuReadOnly(t *testing.T) {
	cfg := testConfig()
	cfg.ReadOnly = true
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev", Config: cfg}, 130, 40)
	h.waitText("[3] acme-web.dev")
	h.keys("enter")
	h.waitText("c  Clear cache · read_only is on")
	h.waitText("› u  Upstream updates") // first (and only) mutating-free items stay available
	if strings.Contains(h.screen(), "› c") {
		t.Error("disabled item selected")
	}
}

func TestPipelineEnterOpensMenu(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.test"}, 140, 40)
	h.waitText("[3] acme-web.test")
	h.keys("p")
	h.waitText("Pipeline · acme-web")
	h.keys("enter")
	h.waitText("d  Deploy 2 commits from dev")
	h.keys("esc")
	h.waitText("Pipeline · acme-web")
	h.keys(":")
	h.waitText("Context: acme-web.test") // palette from the pipeline is env-scoped
}

func TestSitesEnterStillMovesFocus(t *testing.T) {
	h := start(t, Options{Site: "acme-web"}, 130, 40)
	h.waitText("feedback")
	h.keys("enter")
	h.waitFor("envs focus", func(string) bool { return h.root().focus == panelEnvs })
	if strings.Contains(h.screen(), "not available here") {
		t.Error("menu opened from the Sites panel")
	}
}
