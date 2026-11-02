package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rithlath/lazyterminus/internal/config"
)

// ferryStub writes a script that records its arguments, one per line, in
// the returned marker file, then exits with code.
func ferryStub(t *testing.T, code int) (stub, marker string) {
	t.Helper()
	dir := t.TempDir()
	marker = filepath.Join(dir, "args")
	stub = filepath.Join(dir, "ferry")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + marker + "'\nexit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return stub, marker
}

// ferryArgs is what the stub recorded.
func ferryArgs(t *testing.T, marker string) []string {
	t.Helper()
	b, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("ferry wasn't run: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// siteID is the loaded site's ID.
func (h *harness) siteID(site string) string {
	h.t.Helper()
	for _, s := range h.root().sites {
		if s.Name == site {
			return s.ID
		}
	}
	h.t.Fatalf("site %s not loaded", site)
	return ""
}

func ferryConfig(stub, repo string) config.Config {
	cfg := testConfig()
	cfg.FerryPath = stub
	if repo != "" {
		cfg.Sites = []config.Site{{Name: "acme-web", Repo: repo}}
	}
	return cfg
}

func TestPantheonSFTPTarget(t *testing.T) {
	if got := pantheonSFTPTarget("abc-123", "dev"); got != "dev.abc-123@appserver.dev.abc-123.drush.in:2222" {
		t.Fatalf("target = %q", got)
	}
}

func TestFerryKeyLaunches(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "my repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	stub, marker := ferryStub(t, 0)
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev", Config: ferryConfig(stub, repo)}, 130, 40)
	h.waitText("feedback")
	before := len(h.mock.Calls())
	h.keys("f")
	h.waitText("Back from ferry; refreshing acme-web.dev")
	id := h.siteID("acme-web")
	want := []string{"--local-dir", repo, "--remote-dir", "files", "--label", "acme-web.dev", pantheonSFTPTarget(id, "dev")}
	if got := ferryArgs(t, marker); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("ferry args = %q, want %q", got, want)
	}
	h.waitFor("refresh after ferry", func(string) bool { return len(h.mock.Calls()) > before })
	h.noCalls()
}

func TestFerryOnLiveNoConfirm(t *testing.T) {
	stub, marker := ferryStub(t, 0)
	h := start(t, Options{Site: "acme-web", Env: "acme-web.live", Config: ferryConfig(stub, "")}, 130, 40)
	h.waitText("feedback")
	h.keys("f")
	h.waitText("Back from ferry")
	if got := ferryArgs(t, marker); got[len(got)-1] != pantheonSFTPTarget(h.siteID("acme-web"), "live") {
		t.Fatalf("ferry args = %q", got)
	}
}

func TestFerryNoRepo(t *testing.T) {
	stub, marker := ferryStub(t, 0)
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev", Config: ferryConfig(stub, "")}, 130, 40)
	h.waitText("feedback")
	h.keys("f")
	h.waitText("No local repo mapped for acme-web")
	for _, a := range ferryArgs(t, marker) {
		if a == "--local-dir" {
			t.Fatal("--local-dir passed with no repo mapped")
		}
	}
}

func TestFerryMock(t *testing.T) {
	repo := t.TempDir()
	stub, marker := ferryStub(t, 0)
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev", Mock: true, Config: ferryConfig(stub, repo)}, 130, 40)
	h.waitText("feedback")
	h.keys("f")
	h.waitText("Back from ferry")
	if got := ferryArgs(t, marker); strings.Join(got, "|") != "--mock|--local-dir|"+repo {
		t.Fatalf("ferry args under mock = %q", got)
	}
}

func TestFerryReadOnly(t *testing.T) {
	stub, marker := ferryStub(t, 0)
	cfg := ferryConfig(stub, "")
	cfg.ReadOnly = true
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev", Config: cfg}, 130, 40)
	h.waitText("feedback")
	h.keys("f")
	h.waitText("read-only: ferry can change files")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("ferry ran in read-only mode")
	}

	stub2, marker2 := ferryStub(t, 0)
	d := startWith(t, nil, Options{Site: "acme-web", Env: "acme-web.dev", Config: ferryConfig(stub2, "")}, true, 130, 40)
	d.waitText("feedback")
	d.keys("f")
	d.waitText("read-only: ferry can change files")
	if _, err := os.Stat(marker2); err == nil {
		t.Fatal("ferry ran in dry-run mode")
	}
}

func TestFerryNotFound(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope", "ferry")
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev", Config: ferryConfig(missing, "")}, 200, 40)
	h.waitText("feedback")
	h.keys("f")
	h.waitText(missing + " not found: install it (in the ferry repo: make install), or set ferry_path in /tmp/lazyterminus-test.yaml")

	t.Setenv("PATH", t.TempDir())
	d := start(t, Options{Site: "acme-web", Env: "acme-web.dev", Config: ferryConfig("ferry", "")}, 200, 40)
	d.waitText("feedback")
	d.keys("f")
	d.waitText("ferry not found: install it (in the ferry repo: make install), or set ferry_path in")
}

func TestFerryExitError(t *testing.T) {
	stub, _ := ferryStub(t, 3)
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev", Config: ferryConfig(stub, "")}, 130, 40)
	h.waitText("feedback")
	before := len(h.mock.Calls())
	h.keys("f")
	h.waitText("ferry")
	h.waitFor("an error and a refresh", func(s string) bool {
		return strings.Contains(s, "exit status 3") && len(h.mock.Calls()) > before
	})
}

func TestFerrySiteIDUnknown(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev"}, 130, 40)
	h.waitText("feedback")
	m := h.root()
	e := m.state("acme-web").env("dev")
	if e == nil {
		t.Fatal("no dev env loaded")
	}
	if _, _, err := m.ferryCommand("not-a-site", e); !errors.Is(err, errSiteLoading) {
		t.Fatalf("err = %v, want errSiteLoading", err)
	}
}

func TestFerryInHelpAndMenu(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev"}, 160, 50)
	h.waitText("feedback")
	h.keys("?")
	h.waitText("files (ferry)")
	h.keys("esc", "enter")
	h.waitText("Open in ferry (upload files over SFTP)")
}
