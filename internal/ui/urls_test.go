package ui

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
)

type opened struct {
	mu   sync.Mutex
	urls []string
}

func (o *opened) open(url string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.urls = append(o.urls, url)
	return nil
}

func (o *opened) last() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.urls) == 0 {
		return ""
	}
	return o.urls[len(o.urls)-1]
}

func TestDetailShowsURLAndCustomDomains(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.live"}, 140, 40)
	h.waitText("https://live-acme-web.pantheonsite.io")
	h.waitText("https://www.acme-web.example.com (primary)")
	h.waitText("https://acme-web.example.com")
}

func TestOpenKeyOpensPrimaryURL(t *testing.T) {
	o := &opened{}
	h := start(t, Options{Site: "acme-web", Env: "acme-web.live", OpenURL: o.open, Config: testConfig()}, 140, 40)
	h.waitText("(primary)")
	h.keys("v")
	h.waitText("Opened https://www.acme-web.example.com")
	if got := o.last(); got != "https://www.acme-web.example.com" {
		t.Errorf("opened %q", got)
	}

	h.keys("up") // test: no custom domains, so the platform URL
	h.waitText("[3] acme-web.test")
	h.keys("v")
	h.waitText("Opened https://test-acme-web.pantheonsite.io")
}

func TestPipelineURLLineAndHistory(t *testing.T) {
	o := &opened{}
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev", OpenURL: o.open}, 140, 44)
	h.waitText("[3] acme-web.dev")
	h.keys("p")
	h.waitText("Open https://dev-acme-web.pantheonsite.io")
	h.waitText("Recent commits on dev:")
	h.waitText("PROJ-77: Add hero banner block to homepage")
	h.waitText("— Developer B")

	h.keys("right", "right") // live: primary custom domain first, platform after
	h.waitText("Pipeline · acme-web · live")
	h.waitText("Open https://www.acme-web.example.com · https://live-acme-web.pantheonsite.io")
	h.waitText("Recent commits on live:")
	h.clickOn("Open ", "https://live-acme-web.pantheonsite.io", tea.MouseLeft)
	h.waitText("Opened https://live-acme-web.pantheonsite.io")
	if got := o.last(); got != "https://live-acme-web.pantheonsite.io" {
		t.Errorf("click opened %q", got)
	}
}

func TestLocalCommandHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.log")
	earlier := "2026-10-20T13:54:48Z\tuser=me\texec\tterminus remote:drush acme-web.dev -- cim -y\n" +
		"2026-10-20T13:55:00Z\tuser=me\texec\tterminus remote:drush acme-web.test -- cr\n"
	if err := os.WriteFile(path, []byte(earlier), 0o600); err != nil {
		t.Fatal(err)
	}
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev", HistoryLog: path}, 140, 50)
	h.waitText("Your recent commands on dev (this machine only) · O shows output")
	h.waitText("drush cim -y")
	if strings.Contains(h.screen(), "drush cr") {
		t.Error("another environment's command shown")
	}

	h.keys("c")
	h.waitText("Run it? y/n")
	h.keys("y")
	h.waitText("✓ just now clear cache") // logged by this session, read back
	h.keys("p")
	h.waitText("Your recent commands on dev")
}

func TestCopyURL(t *testing.T) {
	copied := &opened{}
	h := start(t, Options{Site: "acme-web", Env: "acme-web.live", Clipboard: copied.open, OpenURL: (&opened{}).open}, 140, 44)
	h.waitText("(primary)")
	h.keys("y")
	h.waitText("Copied https://www.acme-web.example.com to the clipboard.")
	if got := copied.last(); got != "https://www.acme-web.example.com" {
		t.Errorf("copied %q", got)
	}

	h.keys("p") // right-click a URL in the pipeline copies that one
	h.waitText("Open https://www.acme-web.example.com · https://live-acme-web.pantheonsite.io")
	h.clickOn("Open ", "https://live-acme-web.pantheonsite.io", tea.MouseRight)
	h.waitText("Copied https://live-acme-web.pantheonsite.io to the clipboard.")
	if got := copied.last(); got != "https://live-acme-web.pantheonsite.io" {
		t.Errorf("right-click copied %q", got)
	}
}
