package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/rithlath/lazyterminus/internal/config"
)

// gitRepo creates a repo with a Pantheon-looking remote and branches:
// "synced" (same as remote), "ahead" (1 local commit), "behind" (remote has
// 1 more), "localonly" (no remote ref).
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "master")
	run("remote", "add", "origin", "git@github.com:example/site.git")
	run("remote", "add", "pan", "ssh://codeserver.dev.x@codeserver.dev.x.drush.in:2222/~/repository.git")
	run("commit", "-q", "--allow-empty", "-m", "one")
	base := run("rev-parse", "HEAD")
	for _, b := range []string{"synced", "ahead", "behind", "localonly"} {
		run("branch", b)
	}
	run("update-ref", "refs/remotes/pan/synced", base)
	run("update-ref", "refs/remotes/pan/ahead", base)
	run("checkout", "-q", "ahead")
	run("commit", "-q", "--allow-empty", "-m", "local work")
	run("checkout", "-q", "behind")
	run("commit", "-q", "--allow-empty", "-m", "pushed from elsewhere")
	run("update-ref", "refs/remotes/pan/behind", run("rev-parse", "HEAD"))
	run("reset", "-q", "--hard", base)
	run("checkout", "-q", "master")
	return dir
}

func TestBranchStatus(t *testing.T) {
	repo := gitRepo(t)
	if r := PantheonRemote(repo); r != "pan" {
		t.Fatalf("remote = %q (should find the drush.in URL, not origin)", r)
	}
	cases := map[string]string{
		"synced":    "✓ in sync with pan/synced (as of last fetch)",
		"ahead":     "↑1 ahead of pan/ahead (push to deploy) (as of last fetch)",
		"behind":    "↓1 behind pan/behind (as of last fetch)",
		"localonly": "local branch exists (not on pan as of last fetch)",
		"missing":   "no local branch missing",
	}
	for branch, want := range cases {
		if got := ansi.Strip(BranchStatus(repo, branch)); got != want {
			t.Errorf("%s: got %q want %q", branch, got, want)
		}
	}
	if got := ansi.Strip(BranchStatus("", "x")); got != "no local repo mapped" {
		t.Errorf("unmapped: %q", got)
	}
	if got := ansi.Strip(BranchStatus(t.TempDir(), "x")); !strings.HasPrefix(got, "not a git repo") {
		t.Errorf("non-repo: %q", got)
	}
}

func TestMultidevShowsLocalBranch(t *testing.T) {
	repo := gitRepo(t)
	run := exec.Command("git", "-C", repo, "branch", "feedback")
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	cfg := testConfig()
	cfg.Sites = []config.Site{{Name: "acme-web", Repo: repo}}
	h := start(t, Options{Site: "acme-web", Env: "acme-web.feedback", Config: cfg}, 130, 40)
	h.waitText("Branch        feedback")
	h.waitText("Local         local branch exists (not on pan as of last fetch)")
}

func TestLazygitKey(t *testing.T) {
	h := start(t, Options{Site: "acme-web"}, 130, 40)
	h.waitText("feedback")
	h.keys("g")
	h.waitText("No local repo mapped for acme-web")

	repo := gitRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	stub := filepath.Join(t.TempDir(), "lazygit")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho \"$@\" > "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.LazygitPath = stub
	cfg.Sites = []config.Site{{Name: "acme-web", Repo: repo}}
	h2 := start(t, Options{Site: "acme-web", Config: cfg}, 130, 40)
	h2.waitText("feedback")
	before := len(h2.mock.Calls())
	h2.keys("g")
	h2.waitText("Back from lazygit; refreshing acme-web")
	b, err := os.ReadFile(marker)
	if err != nil || strings.TrimSpace(string(b)) != "-p "+repo {
		t.Fatalf("lazygit args = %q err=%v", b, err)
	}
	h2.waitFor("refresh after lazygit", func(string) bool { return len(h2.mock.Calls()) > before })
	h2.noCalls()
}
