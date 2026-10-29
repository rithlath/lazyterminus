package runs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func waitDone(t *testing.T, r *Run) []string {
	t.Helper()
	var lines []string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		l, ended := r.Poll()
		lines = append(lines, l...)
		if ended {
			return lines
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("run never ended")
	return nil
}

func TestStartPollAndList(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("sh", "-c", `echo one; echo two >&2; printf partial; exit 3`)
	r, err := Start(dir, cmd, Meta{Title: "drush cim -y on s.dev", Site: "s", Env: "dev", Command: "terminus remote:drush s.dev -- cim -y", Started: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	lines := waitDone(t, r)
	if got := strings.Join(lines, "|"); got != "one|two|partial" {
		t.Errorf("lines = %q", got)
	}
	if !r.Failed() || r.ExitCode != 3 || r.Ended.IsZero() {
		t.Errorf("run = %+v", r)
	}

	all, err := List(dir)
	if err != nil || len(all) != 1 {
		t.Fatalf("List = %v, %v", all, err)
	}
	got := all[0]
	if !got.Done || got.ExitCode != 3 || got.Title != "drush cim -y on s.dev" || got.Seen {
		t.Errorf("listed = %+v", got)
	}
	got.MarkSeen()
	if again, _ := List(dir); !again[0].Seen {
		t.Error("seen not persisted")
	}
	if !strings.Contains(got.Output(), "partial") {
		t.Errorf("output = %q", got.Output())
	}
}

func TestRunOutlivesItsContext(t *testing.T) {
	// The command passed in is only a template: cancelling its context or
	// never waiting on it doesn't touch the detached run.
	dir := t.TempDir()
	cmd := exec.Command("sh", "-c", "sleep 0.3; echo survived")
	r, err := Start(dir, cmd, Meta{Started: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if lines := waitDone(t, r); len(lines) != 1 || lines[0] != "survived" || r.Failed() {
		t.Errorf("lines = %q, exit %d", lines, r.ExitCode)
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	for i := range 4 {
		r, err := Start(dir, exec.Command("true"), Meta{Started: time.Now().Add(time.Duration(i) * time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		waitDone(t, r)
	}
	running := filepath.Join(dir, "00000000T000000-aaaaaa") // oldest, never finished
	if err := os.MkdirAll(running, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(running, "meta.json"), []byte(`{"title":"still going"}`), 0o600)
	if err := Prune(dir, 2); err != nil {
		t.Fatal(err)
	}
	all, _ := List(dir)
	if len(all) != 3 || all[0].Title != "still going" {
		t.Errorf("after prune: %d runs, first %+v", len(all), all[0])
	}
}
