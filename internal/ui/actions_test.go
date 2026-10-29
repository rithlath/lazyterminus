package ui

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/rithlath/lazyterminus/internal/terminus"
)

func (h *harness) mutating() [][]string {
	var out [][]string
	for _, c := range h.mock.MutatingCalls() {
		out = append(out, c.Args)
	}
	return out
}

func (h *harness) waitCalls(n int) [][]string {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if calls := h.mutating(); len(calls) >= n {
			return calls
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("expected %d mutating calls, got %v\n%s", n, h.mutating(), h.screen())
	return nil
}

// selectEnv presses j one step at a time, waiting for each move to render,
// until the Detail panel shows env.
func (h *harness) selectEnv(env string) {
	h.t.Helper()
	title := func() string {
		for _, l := range strings.Split(h.screen(), "\n") {
			if i := strings.Index(l, "[3] "); i >= 0 {
				return strings.Fields(l[i+4:])[0]
			}
		}
		return ""
	}
	for range 30 {
		cur := title()
		if strings.HasSuffix(cur, "."+env) {
			return
		}
		h.keys("j")
		h.waitFor("selection to move", func(string) bool { return title() != cur })
	}
	h.t.Fatalf("could not select %s", env)
}

func (h *harness) noCalls() {
	h.t.Helper()
	time.Sleep(50 * time.Millisecond)
	if calls := h.mutating(); len(calls) != 0 {
		h.t.Fatalf("unexpected mutating calls: %v", calls)
	}
}

func TestDeployFlowEndToEnd(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.test"}, 130, 40)
	h.waitText("↑2 ready to deploy from dev")
	h.keys("d")
	h.waitText("Deploying promotes ALL 2 commit(s) from dev to test.")
	h.waitText("a1c9e0f PROJ-77")
	h.waitText("Sync content")
	h.keys("enter") // note is required
	h.waitText("deploy note is required")
	h.typeText("Ship trip planner")
	h.keys("tab", "tab", "space") // turn off --updatedb
	h.waitText("terminus env:deploy acme-web.test '--note=Ship trip planner' --cc --yes")
	h.keys("enter")
	h.waitText("Type acme-web.test to confirm")
	h.waitText("test is a protected environment")
	h.typeText("acme-web.live")
	h.keys("enter")
	h.waitText("Type acme-web.test exactly to confirm.")
	h.noCalls()
	for range len("acme-web.live") {
		h.keys("backspace")
	}
	h.typeText("acme-web.test")
	h.keys("enter")
	h.waitText("Deploy 2 commit(s) to acme-web.test")
	calls := h.waitCalls(1)
	want := []string{"env:deploy", "acme-web.test", "--note=Ship trip planner", "--cc", "--yes"}
	if !slices.Equal(calls[0], want) {
		t.Fatalf("deploy args = %q, want %q", calls[0], want)
	}
	h.waitText("finished")
	// Confirming a deploy switches to the pipeline view to watch it land.
	h.waitText("Pipeline · acme-web")
	h.waitText("✓ landed 2 commits")
	h.keys("esc")
	h.waitText("in sync with dev")
	if len(h.mutating()) != 1 {
		t.Errorf("calls = %v", h.mutating())
	}
}

func TestDeployLiveTypedAndNoSync(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.live"}, 130, 40)
	h.waitText("↑1 ready to deploy from test")
	h.keys("d")
	h.waitText("Deploying promotes ALL 1 commit(s) from test to live.")
	if strings.Contains(h.screen(), "Sync content") {
		t.Error("sync content must never be offered for live")
	}
	h.typeText("hotfix")
	h.keys("enter")
	h.waitText("deploys to production")
	h.waitText("Type acme-web.live to confirm")
	h.keys("esc")
	h.waitText("Cancelled: nothing was run.")
	h.noCalls()
}

func TestDeployNothingToDeployAndWrongEnv(t *testing.T) {
	h := start(t, Options{Site: "acme-portal", Env: "acme-portal.test"}, 130, 40)
	h.waitText("[3] acme-portal.test")
	h.waitText("in sync with dev")
	h.keys("d")
	h.waitText("Nothing to deploy.")
	h.keys("esc")
	h.keys("k") // dev
	h.waitText("[3] acme-portal.dev")
	h.keys("d")
	h.waitText("Deploy is only available on test and live")
	h.noCalls()
}

func TestClearCacheYesNo(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev"}, 130, 40)
	h.waitText("[3] acme-web.dev")
	h.keys("c")
	h.waitText("terminus env:clear-cache acme-web.dev")
	h.waitText("Run it? y/n")
	h.keys("enter") // enter must not confirm
	h.keys("n")
	h.waitText("Cancelled: nothing was run.")
	h.noCalls()
	h.keys("c")
	h.waitText("Run it? y/n")
	h.keys("y")
	if calls := h.waitCalls(1); !slices.Equal(calls[0], []string{"env:clear-cache", "acme-web.dev"}) {
		t.Errorf("calls = %v", calls)
	}
	h.waitText("Clear cache on acme-web.dev finished")
}

func TestClearCacheOnLiveIsTyped(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.live"}, 130, 40)
	h.waitText("[3] acme-web.live")
	h.keys("c")
	h.waitText("Type acme-web.live to confirm")
	h.keys("y", "enter")
	h.waitText("Type acme-web.live exactly to confirm.")
	h.noCalls()
}

func TestCloneNeverOffersLiveAndTypedForTest(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.test"}, 130, 40)
	h.waitText("[3] acme-web.test")
	h.keys("l")
	h.waitText("Clone content · acme-web")
	h.waitText("terminus env:clone-content acme-web.live test --cc --yes")
	// Cycle through every target: live must never come up.
	h.keys("tab")
	for range 15 {
		h.keys("right")
		if strings.Contains(h.screen(), "env:clone-content acme-web.live live") || strings.Contains(h.screen(), "‹ live ›\n") {
			t.Fatal("live offered as clone target")
		}
	}
	m := h.root()
	if f, ok := m.modal.(*form); ok {
		if slices.Contains(f.fields[1].options, "live") {
			t.Fatalf("targets = %v", f.fields[1].options)
		}
	}
	h.keys("esc")
	h.keys("l")
	h.waitText("acme-web.live test --cc --yes")
	h.keys("enter")
	h.waitText("overwrites the target's database/files")
	h.waitText("Type acme-web.test to confirm")
	h.typeText("acme-web.test")
	h.keys("enter")
	calls := h.waitCalls(1)
	if !slices.Equal(calls[0], []string{"env:clone-content", "acme-web.live", "test", "--cc", "--yes"}) {
		t.Errorf("calls = %v", calls)
	}
}

func TestCloneIntoMultidevWakesFirst(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.proj-19"}, 130, 40)
	h.waitText("[3] acme-web.proj-19")
	h.keys("l")
	h.waitText("acme-web.live proj-19 --cc --yes")
	h.keys("tab", "tab", "tab", "space") // files off → --db-only
	h.waitText("terminus env:wake acme-web.proj-19")
	h.waitText("acme-web.live proj-19 --db-only --cc --yes")
	h.keys("enter")
	h.waitText("Run it? y/n")
	h.keys("y")
	calls := h.waitCalls(2)
	if calls[0][0] != "env:wake" || calls[1][0] != "env:clone-content" {
		t.Errorf("calls = %v", calls)
	}
	if h.mock.Asleep("acme-web.proj-19") {
		t.Error("env not woken")
	}
}

func TestConnectionModeWarnsAboutUncommitted(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.feedback"}, 130, 40)
	h.waitText("✎ 2 files")
	h.keys("m")
	h.waitText("has 2 uncommitted file(s). Switching to Git DISCARDS them.")
	h.waitText("uncommitted SFTP changes will be lost")
	h.waitText("Type acme-web.feedback to confirm")
	h.keys("esc")
	h.noCalls()
}

func TestConnectionModeNotOnTestOrLive(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.live"}, 130, 40)
	h.waitText("[3] acme-web.live")
	h.keys("m")
	h.waitText("Connection mode can only be changed on dev and multidevs")
	h.noCalls()
}

func TestCommitSFTPChanges(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.feedback"}, 130, 40)
	h.waitText("✎ 2 files")
	h.keys("o")
	h.waitText("Commit 2 changed file(s) on acme-web.feedback")
	h.typeText("Add contact feedback form")
	h.keys("enter")
	h.waitText("Run it? y/n")
	h.keys("y")
	calls := h.waitCalls(1)
	if !slices.Equal(calls[0], []string{"env:commit", "acme-web.feedback", "--message=Add contact feedback form", "--yes"}) {
		t.Errorf("calls = %v", calls)
	}
	h.waitText("Uncommitted   none")
}

func TestMultidevCreateAndDelete(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev"}, 130, 40)
	h.waitText("[3] acme-web.dev")
	h.keys("n")
	h.waitText("New multidev · acme-web")
	h.typeText("Bad_Name")
	h.waitText("use 1–11 lowercase letters")
	for range len("Bad_Name") {
		h.keys("backspace")
	}
	h.typeText("feedback")
	h.waitText(`"feedback" already exists`)
	for range len("feedback") {
		h.keys("backspace")
	}
	h.typeText("hotfix")
	h.waitText("terminus multidev:create acme-web.dev hotfix")
	h.keys("enter")
	h.waitText("Run it? y/n")
	h.keys("y")
	h.waitCalls(1)
	h.waitText("hotfix")
	// Delete it again: typed confirmation.
	h.waitFor("hotfix row", func(s string) bool { return strings.Contains(s, "● hotfix") || strings.Contains(s, "○ hotfix") })
	h.selectEnv("hotfix")
	h.keys("D")
	h.waitText("Deletes the hotfix environment")
	h.keys("enter")
	h.waitText("Type acme-web.hotfix to confirm")
	h.typeText("acme-web.hotfix")
	h.keys("enter")
	calls := h.waitCalls(2)
	if !slices.Equal(calls[1], []string{"multidev:delete", "acme-web.hotfix", "--yes"}) {
		t.Errorf("delete = %v", calls[1])
	}
}

func TestDeleteMultidevOnlyOnMultidevs(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev"}, 130, 40)
	h.waitText("[3] acme-web.dev")
	h.keys("D")
	h.waitText("Only multidevs can be deleted.")
	h.noCalls()
}

func TestDrushRunsInBackground(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.proj-19"}, 130, 40)
	h.waitText("[3] acme-web.proj-19")
	h.keys("x")
	h.waitText("Drush · acme-web.proj-19")
	h.typeText(`drush cget system.site name --format="json"`)
	h.waitText("terminus env:wake acme-web.proj-19")
	h.waitText("terminus remote:drush acme-web.proj-19 -- cget system.site name --format=json")
	h.keys("enter")
	h.waitText("Run it? y/n")
	h.keys("y")
	calls := h.waitCalls(2)
	if calls[0][0] != "env:wake" {
		t.Fatalf("calls = %v", calls)
	}
	want := []string{"remote:drush", "acme-web.proj-19", "--", "cget", "system.site", "name", "--format=json"}
	if !slices.Equal(calls[1], want) {
		t.Errorf("drush = %q", calls[1])
	}
	// The UI stays usable while it runs, and the result lands in Output.
	h.waitText("✓ drush cget system.site name --format=\"json\" on acme-web.proj-19 finished")
	h.keys("O")
	h.waitText("[mock] remote:drush output for: cget system.site name --format=json")
}

func TestBackupForm(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev"}, 130, 40)
	h.waitText("[3] acme-web.dev")
	h.keys("b", "right", "right") // all → code → files
	h.waitText("terminus backup:create acme-web.dev --element=files")
	h.keys("tab")
	h.typeText("abc")
	h.waitText("must be a whole number of days")
	for range 3 {
		h.keys("backspace")
	}
	h.typeText("30")
	h.waitText("--element=files --keep-for=30")
	h.keys("enter", "y")
	if calls := h.waitCalls(1); !slices.Equal(calls[0], []string{"backup:create", "acme-web.dev", "--element=files", "--keep-for=30"}) {
		t.Errorf("calls = %v", calls)
	}
}

func TestReadOnlyBlocksActions(t *testing.T) {
	cfg := testConfig()
	cfg.ReadOnly = true
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev", Config: cfg}, 130, 40)
	h.waitText("READ-ONLY")
	h.waitText("[3] acme-web.dev")
	h.keys("c")
	h.waitText("read_only is enabled")
	h.keys("w")
	h.noCalls()
}

func TestDryRunPrintsInsteadOfRunning(t *testing.T) {
	h := startWith(t, nil, Options{Site: "acme-web", Env: "acme-web.dev"}, true, 130, 40)
	h.waitText("DRY-RUN")
	h.waitText("[3] acme-web.dev")
	h.keys("c")
	h.waitText("Run it? y/n")
	h.keys("y")
	h.waitText("finished")
	h.noCalls()
	printed := h.dry.Printed()
	if len(printed) != 1 || !slices.Equal(printed[0], []string{"env:clear-cache", "acme-web.dev"}) {
		t.Errorf("printed = %v", printed)
	}
	h.keys("O")
	h.waitText("[dry-run] terminus env:clear-cache acme-web.dev")
}

func TestUpstreamUpdatesShowInOutput(t *testing.T) {
	h := start(t, Options{Site: "acme-intranet", Env: "acme-intranet.dev"}, 130, 40)
	h.waitText("[3] acme-intranet.dev")
	h.keys("u")
	h.waitText("Upstream updates for acme-intranet.dev")
	h.waitText("Update to Drupal 11.4.10.")
	h.noCalls()
}

func TestQuitWithRunningJobAsks(t *testing.T) {
	mock := testMock(t)
	mock.WorkflowDuration = 2 * time.Second
	h := startWith(t, mock, Options{Site: "acme-web", Env: "acme-web.dev"}, false, 130, 40)
	h.waitText("[3] acme-web.dev")
	h.keys("c", "y")
	h.waitText("Clear cache on acme-web.dev · job")
	h.keys("q")
	h.waitText("Clear cache on acme-web.dev: env:clear-cache keeps running on Pantheon")
	h.keys("n")
	h.waitFor("prompt closed", func(s string) bool { return !strings.Contains(s, "Still running") })
}

func TestBackgroundDrushSurvivesQuit(t *testing.T) {
	runsDir, histLog := filepath.Join(t.TempDir(), "runs"), filepath.Join(t.TempDir(), "history.log")
	mock := testMock(t)
	mock.WorkflowDuration = 1500 * time.Millisecond // drush "takes" 0.75s
	h := startWith(t, mock, Options{Site: "acme-web", Env: "acme-web.proj-19", RunsDir: runsDir, HistoryLog: histLog}, false, 140, 40)
	h.waitText("[3] acme-web.proj-19")
	h.keys("x")
	h.typeText("updb -y")
	h.keys("enter")
	h.waitText("Run it? y/n")
	h.keys("y")
	h.waitText("Started in the background: drush updb -y on acme-web.proj-19")
	h.keys("q") // nothing a quit would cut off, so no prompt
	h.final()

	time.Sleep(time.Second) // it finishes while lazyterminus is closed
	h2 := startWith(t, testMock(t), Options{Site: "acme-web", Env: "acme-web.proj-19", RunsDir: runsDir, HistoryLog: histLog}, false, 140, 40)
	h2.waitText("1 background command finished while lazyterminus was closed")
	h2.waitText("✓ just now drush updb -y") // its result in the local history
	h2.keys("O")
	h2.waitText("[mock] remote:drush output for: updb -y")
	h2.waitText("(ran while lazyterminus was closed)")
}

func TestSplitWords(t *testing.T) {
	got, err := splitWords(`cget 'system.site' "name x" a\ b --format=json`)
	if err != nil || !slices.Equal(got, []string{"cget", "system.site", "name x", "a b", "--format=json"}) {
		t.Errorf("got %q err %v", got, err)
	}
	if _, err := splitWords(`"open`); err == nil {
		t.Error("unterminated quote should fail")
	}
}

// Every mutating call made through the UI went through the Guard; a direct
// call without approval fails.
func TestRunnerRefusesUnapproved(t *testing.T) {
	h := start(t, Options{Site: "acme-web"}, 100, 30)
	h.waitText("acme-web")
	m := h.root()
	if _, _, err := m.opts.Runner.Run(t.Context(), "env:clear-cache", "acme-web.dev"); err == nil {
		t.Fatal("unapproved mutating command was allowed")
	}
	h.noCalls()
	if !terminus.IsMutating([]string{"env:clear-cache"}) {
		t.Fatal("classification changed")
	}
}

func TestPausedExecWaitsForEnter(t *testing.T) {
	for _, c := range []struct {
		cmd     *exec.Cmd
		wantErr bool
		want    string
	}{
		{exec.Command("sh", "-c", "echo drush says hi"), false, "Finished"},
		{exec.Command("sh", "-c", "echo boom; exit 3"), true, "exit status 3"},
	} {
		in := strings.NewReader("\n")
		var out strings.Builder
		p := &pausedExec{Cmd: c.cmd}
		p.SetStdin(in)
		p.SetStdout(&out)
		p.SetStderr(&out)
		err := p.Run()
		if (err != nil) != c.wantErr {
			t.Errorf("err = %v", err)
		}
		got := ansi.Strip(out.String())
		if !strings.Contains(got, c.want) || !strings.Contains(got, "press Enter to return to lazyterminus") {
			t.Errorf("output = %q", got)
		}
		if in.Len() != 0 {
			t.Error("didn't wait for Enter")
		}
	}
}
