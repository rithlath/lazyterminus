package terminus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHistoryLogsOnlyMutating(t *testing.T) {
	m := newTestMock(t)
	path := filepath.Join(t.TempDir(), "state", "history.log")
	h := &History{Inner: m, Path: path, Now: func() time.Time { return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC) }}
	ctx := context.Background()
	if _, _, err := h.Run(ctx, "site:list", "--format=json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("read-only command was logged")
	}
	if _, _, err := h.Run(ctx, "env:clear-cache", "acme-web.dev"); err != nil {
		t.Fatal(err)
	}
	m.FailNext("env:wake", "nope")
	_, _, _ = h.Run(ctx, "env:wake", "acme-web.dev")
	lines, errc := h.Stream(ctx, "env:deploy", "acme-web.test", "--note=ship it")
	for range lines {
	}
	<-errc
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(got) != 6 {
		t.Fatalf("log lines = %d:\n%s", len(got), b)
	}
	if !strings.HasPrefix(got[0], "2026-10-27T12:00:00Z\tuser=") || !strings.HasSuffix(got[0], "\tstart\tterminus env:clear-cache acme-web.dev") {
		t.Errorf("line 0 = %q", got[0])
	}
	if !strings.Contains(got[1], "\tok\t") || !strings.Contains(got[3], "failed(exit=1)") {
		t.Errorf("results = %q / %q", got[1], got[3])
	}
	if !strings.HasSuffix(got[4], `terminus env:deploy acme-web.test '--note=ship it'`) {
		t.Errorf("stream line = %q", got[4])
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v", fi.Mode().Perm())
	}
}

func TestDryRunInterceptsMutating(t *testing.T) {
	m := newTestMock(t)
	d := &DryRun{Inner: m}
	ctx := context.Background()
	if _, _, err := d.Run(ctx, "env:list", "acme-web", "--format=json"); err != nil {
		t.Fatal(err)
	}
	out, _, err := d.Run(ctx, "env:deploy", "acme-web.live", "--note=x", "--cc")
	if err != nil || string(out) != "[dry-run] terminus env:deploy acme-web.live --note=x --cc\n" {
		t.Fatalf("out = %q err=%v", out, err)
	}
	lines, errc := d.Stream(ctx, "env:wipe", "acme-web.dev")
	for range lines {
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if _, err := d.Command(ctx, "remote:drush", "acme-web.dev", "--", "cr"); err != nil {
		t.Fatal(err)
	}
	if len(m.MutatingCalls()) != 0 {
		t.Errorf("dry-run leaked to inner runner: %+v", m.MutatingCalls())
	}
	if len(d.Printed()) != 3 {
		t.Errorf("printed = %v", d.Printed())
	}
}

func TestHistoryFailureBlocksMutating(t *testing.T) {
	m := newTestMock(t)
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// The log's parent is a regular file, so the log can't be created.
	h := &History{Inner: m, Path: filepath.Join(blocker, "history.log")}
	var he *ErrHistory
	if _, _, err := h.Run(context.Background(), "env:clear-cache", "acme-web.dev"); !errors.As(err, &he) {
		t.Fatalf("err = %v", err)
	}
	if len(m.MutatingCalls()) != 0 {
		t.Error("command ran without being logged")
	}
	if _, _, err := h.Run(context.Background(), "site:list", "--format=json"); err != nil {
		t.Errorf("reads should still work: %v", err)
	}
}

func TestParseHistoryLog(t *testing.T) {
	log := strings.Join([]string{
		"2026-10-29T13:54:48Z\tuser=rlath\texec\tterminus remote:drush one-rta.live -- cst",
		"2026-10-29T14:03:20Z\tuser=rlath\tstart\tterminus env:clear-cache one-rta.live",
		"2026-10-29T14:03:21Z\tuser=rlath\tstart\tterminus backup:create one-rta.dev --element=all",
		"2026-10-29T14:03:48Z\tuser=rlath\tok\tterminus env:clear-cache one-rta.live",
		"2026-10-29T14:04:00Z\tuser=rlath\tfailed(exit=1)\tterminus backup:create one-rta.dev --element=all",
		"2026-10-29T14:05:00Z\tuser=rlath\tstart\tterminus env:wake one-rta.live",
		"garbage",
		"",
	}, "\n")
	got := ParseHistoryLog([]byte(log))
	if len(got) != 4 {
		t.Fatalf("got %d entries: %+v", len(got), got)
	}
	want := []string{"exec", "ok", "failed(exit=1)", ""}
	for i, w := range want {
		if got[i].Result != w {
			t.Errorf("entry %d (%s) result = %q, want %q", i, got[i].Command, got[i].Result, w)
		}
	}
	if !got[1].Involves("one-rta.live") || got[1].Involves("one-rta.dev") || got[1].User != "rlath" {
		t.Errorf("entry 1 = %+v", got[1])
	}
}

func TestLoggedDescribe(t *testing.T) {
	for _, c := range []struct{ cmd, want string }{
		{"terminus remote:drush one-rta.live -- cim -y", "drush cim -y"},
		{"terminus remote:drush one-rta.live -- cst", "drush cst"},
		{"terminus env:clear-cache one-rta.live", "clear cache"},
		{"terminus backup:create one-rta.live --element=all", "backup --element=all"},
		{"terminus env:deploy one-rta.live --cc --note=Ship", "deploy --cc --note=Ship"},
		{"terminus env:clone-content one-rta.live dev --db-only", "clone content to dev --db-only"},
		{"terminus multidev:create one-rta.live feature", "multidev:create feature"},
	} {
		if got := (Logged{Command: c.cmd}).Describe("one-rta.live"); got != c.want {
			t.Errorf("Describe(%q) = %q, want %q", c.cmd, got, c.want)
		}
	}
}
