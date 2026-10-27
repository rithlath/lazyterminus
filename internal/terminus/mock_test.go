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

// NewTestMock returns a mock with no latency and short workflows.
func newTestMock(t *testing.T) *MockRunner {
	t.Helper()
	m, err := NewMockRunner(os.DirFS(filepath.Join("..", "..", "testdata")))
	if err != nil {
		t.Fatal(err)
	}
	m.MinLatency, m.MaxLatency = 0, 0
	m.WorkflowDuration = 40 * time.Millisecond
	return m
}

func run(t *testing.T, m *MockRunner, args ...string) []byte {
	t.Helper()
	out, stderr, err := m.Run(context.Background(), args...)
	if err != nil {
		t.Fatalf("%v: %v (%s)", args, err, stderr)
	}
	return out
}

func TestMockServesParseableReads(t *testing.T) {
	m := newTestMock(t)
	sites, err := ParseSites(run(t, m, "site:list", "--format=json"))
	if err != nil || len(sites) != 4 {
		t.Fatalf("sites = %v err=%v", sites, err)
	}
	envs, err := ParseEnvs(run(t, m, "env:list", "acme-web", "--format=json", "--fields=*"))
	if err != nil || envs[0].ID != "dev" {
		t.Fatalf("envs err=%v", err)
	}
	if _, err := ParseWorkflows(run(t, m, "workflow:list", "acme-web", "--format=json")); err != nil {
		t.Fatal(err)
	}
	if len(m.MutatingCalls()) != 0 {
		t.Error("reads recorded as mutating")
	}
}

func TestMockScenarioPendingCommits(t *testing.T) {
	m := newTestMock(t)
	dev, _ := ParseCodeLog(run(t, m, "env:code-log", "acme-web.dev", "--format=json"))
	test, _ := ParseCodeLog(run(t, m, "env:code-log", "acme-web.test", "--format=json"))
	if n := len(DeployableCommits(dev, "dev", "test")); n != 2 {
		t.Errorf("dev→test = %d, want 2", n)
	}
	if n := len(DeployableCommits(test, "test", "live")); n != 1 {
		t.Errorf("test→live = %d, want 1", n)
	}
}

func TestMockDeployProgressesAndApplies(t *testing.T) {
	m := newTestMock(t)
	m.WorkflowDuration = 300 * time.Millisecond
	done := make(chan error)
	go func() {
		_, _, err := m.Run(context.Background(), "env:deploy", "acme-web.test", "--note=x", "--yes")
		done <- err
	}()
	time.Sleep(120 * time.Millisecond)
	wfs, _ := ParseWorkflows(run(t, m, "workflow:list", "acme-web", "--format=json"))
	if !wfs[0].Running() || wfs[0].Workflow != `Deploy code to "test"` {
		t.Fatalf("newest workflow = %+v", wfs[0])
	}
	ops, _ := ParseWorkflowOps(run(t, m, "workflow:info:operations", "acme-web", "--id="+wfs[0].ID, "--format=json"))
	if d, tot := Progress(ops); d == 0 || d == tot {
		t.Errorf("mid-run progress = %d/%d", d, tot)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	wfs, _ = ParseWorkflows(run(t, m, "workflow:list", "acme-web", "--format=json"))
	if wfs[0].Running() || wfs[0].Status != StatusSucceeded {
		t.Errorf("after run = %+v", wfs[0])
	}
	dev, _ := ParseCodeLog(run(t, m, "env:code-log", "acme-web.dev", "--format=json"))
	if n := len(DeployableCommits(dev, "dev", "test")); n != 0 {
		t.Errorf("still %d pending after deploy", n)
	}
	calls := m.MutatingCalls()
	if len(calls) != 1 || calls[0].Args[0] != "env:deploy" {
		t.Errorf("mutating calls = %+v", calls)
	}
}

func TestMockConnectionAndCommit(t *testing.T) {
	m := newTestMock(t)
	ds, _ := ParseDiffstat(run(t, m, "env:diffstat", "acme-web.feedback", "--format=json"))
	if len(ds) != 2 {
		t.Fatalf("diffstat = %+v", ds)
	}
	run(t, m, "env:commit", "acme-web.feedback", "--message=save")
	ds, _ = ParseDiffstat(run(t, m, "env:diffstat", "acme-web.feedback", "--format=json"))
	if len(ds) != 0 {
		t.Error("commit should clear diffstat")
	}
	run(t, m, "connection:set", "acme-web.feedback", "git")
	envs, _ := ParseEnvs(run(t, m, "env:list", "acme-web", "--format=json"))
	for _, e := range envs {
		if e.ID == "feedback" && e.ConnectionMode != "git" {
			t.Error("mode not switched")
		}
	}
	if _, _, err := m.Run(context.Background(), "connection:set", "acme-web.live", "sftp"); err == nil {
		t.Error("connection:set on live must fail like Terminus")
	}
}

func TestMockMultidevLifecycle(t *testing.T) {
	m := newTestMock(t)
	run(t, m, "multidev:create", "acme-web.dev", "newmd")
	envs, _ := ParseEnvs(run(t, m, "env:list", "acme-web", "--format=json"))
	if !hasEnv(envs, "newmd") {
		t.Fatal("multidev not created")
	}
	run(t, m, "multidev:delete", "acme-web.newmd", "--yes")
	envs, _ = ParseEnvs(run(t, m, "env:list", "acme-web", "--format=json"))
	if hasEnv(envs, "newmd") {
		t.Fatal("multidev not deleted")
	}
	if _, _, err := m.Run(context.Background(), "multidev:delete", "acme-web.live"); err == nil {
		t.Error("deleting live must fail")
	}
}

func hasEnv(envs []Env, id string) bool {
	for _, e := range envs {
		if e.ID == id {
			return true
		}
	}
	return false
}

func TestMockWakeAndFailInjection(t *testing.T) {
	m := newTestMock(t)
	if !m.Asleep("acme-web.proj-19") {
		t.Fatal("scenario env should start asleep")
	}
	_, stderr, err := m.Run(context.Background(), "env:wake", "acme-web.proj-19")
	if err != nil || m.Asleep("acme-web.proj-19") || !strings.Contains(string(stderr), "after waking") {
		t.Fatalf("wake err=%v stderr=%s", err, stderr)
	}
	m.FailNext("env:clear-cache", "Boom")
	if _, _, err := m.Run(context.Background(), "env:clear-cache", "acme-web.dev"); err == nil || !strings.Contains(err.Error(), "Boom") {
		t.Errorf("err = %v", err)
	}
}

func TestMockStream(t *testing.T) {
	m := newTestMock(t)
	lines, errc := m.Stream(context.Background(), "env:clear-cache", "acme-web.dev")
	var got []string
	for l := range lines {
		got = append(got, l)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 || !strings.Contains(got[len(got)-1], "succeeded") {
		t.Errorf("lines = %q", got)
	}
}

func TestMockCatalogIncludesPlugins(t *testing.T) {
	m := newTestMock(t)
	out := run(t, m, "list", "--format=json")
	if !strings.Contains(string(out), `"build:env:list"`) || !strings.Contains(string(out), `"env:deploy"`) {
		t.Error("catalog missing core or plugin commands")
	}
	help := run(t, m, "help", "env:deploy", "--format=json")
	if !strings.Contains(string(help), "sync-content") {
		t.Error("help output missing options")
	}
}

func TestMockFailedWorkflowReason(t *testing.T) {
	m := newTestMock(t)
	wfs, _ := ParseWorkflows(run(t, m, "workflow:list", "acme-web", "--format=json"))
	f, ok := LastFailed(wfs, "test")
	if !ok {
		t.Fatal("scenario should have a failed test deploy")
	}
	ops, _ := ParseWorkflowOps(run(t, m, "workflow:info:operations", "acme-web", "--id="+f.ID, "--format=json"))
	if FailureReason(ops) == "" {
		t.Error("no failure reason")
	}
}

func TestMockMultidevWithoutFixtureLogIsInSync(t *testing.T) {
	m := newTestMock(t)
	out := run(t, m, "env:code-log", "acme-web.annual", "--format=json")
	md, err := ParseCodeLog(out)
	if err != nil || len(md) == 0 {
		t.Fatalf("annual log = %s err=%v", out, err)
	}
	dev, _ := ParseCodeLog(run(t, m, "env:code-log", "acme-web.dev", "--format=json"))
	if n := len(CommitsNotIn(md, dev)); n != 0 {
		t.Errorf("annual ahead of dev = %d", n)
	}
	if out := run(t, m, "env:code-log", "acme-portal.dev", "--format=json"); string(out) == "null\n" {
		t.Error("empty logs must be [] like Terminus")
	}
}

func TestMockLoggedOutAndWhoAmI(t *testing.T) {
	m := newTestMock(t)
	c := NewClient(m)
	if who, err := c.WhoAmI(context.Background()); err != nil || who != "dev@example.com" {
		t.Fatalf("whoami = %q, %v", who, err)
	}
	m.SetLoggedIn(false)
	if _, err := c.WhoAmI(context.Background()); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("logged out whoami err = %v", err)
	}
	_, err := c.Sites(context.Background(), true)
	if !IsNotLoggedIn(err) {
		t.Errorf("site:list err = %v", err)
	}
	if _, _, err := m.Run(context.Background(), "list", "--format=json"); err != nil {
		t.Errorf("list should work logged out: %v", err)
	}
}
