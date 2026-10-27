package safety

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rithlath/lazyterminus/internal/terminus"
)

var defaultPolicy = Policy{ProtectedEnvs: []string{"live", "test"}}

func mock(t *testing.T) *terminus.MockRunner {
	t.Helper()
	m, err := terminus.NewMockRunner(os.DirFS(filepath.Join("..", "..", "testdata")))
	if err != nil {
		t.Fatal(err)
	}
	m.MinLatency, m.MaxLatency, m.WorkflowDuration = 0, 0, 5*time.Millisecond
	return m
}

func TestReadOnlyNeedsNothing(t *testing.T) {
	for _, args := range [][]string{
		{"site:list", "--format=json"}, {"env:info", "acme-web.live"}, {"workflow:list", "acme-web"},
		{"env:deploy", "--help"}, {"--version"},
	} {
		if d := defaultPolicy.Evaluate(args, Context{}); d.Level != None {
			t.Errorf("%v: level %v", args, d.Level)
		}
	}
}

// Every destructive command requires typed confirmation on every kind of
// environment, protected or not.
func TestDestructiveAlwaysTyped(t *testing.T) {
	envs := []string{"dev", "test", "live", "feature"}
	cmds := []func(env string) []string{
		func(e string) []string { return []string{"env:wipe", "acme-web." + e, "--yes"} },
		func(e string) []string { return []string{"multidev:delete", "acme-web." + e, "--yes"} },
		func(e string) []string { return []string{"import:database", "acme-web." + e, "https://x/db.sql.gz"} },
		func(e string) []string { return []string{"import:files", "acme-web." + e, "https://x/f.tgz"} },
		func(e string) []string { return []string{"backup:restore", "acme-web." + e} },
		// aliases resolve to the same rules
		func(e string) []string { return []string{"env:delete", "acme-web." + e} },
		func(e string) []string { return []string{"import:db", "acme-web." + e, "u"} },
	}
	for _, env := range envs {
		for _, mk := range cmds {
			args := mk(env)
			d := defaultPolicy.Evaluate(args, Context{})
			if d.Level != Typed || d.Phrase != "acme-web."+env {
				t.Errorf("%v: level=%v phrase=%q", args, d.Level, d.Phrase)
			}
		}
	}
	for _, args := range [][]string{{"site:delete", "acme-web"}, {"import:site", "acme-web", "https://x"}, {"site:import", "acme-web", "u"}} {
		d := defaultPolicy.Evaluate(args, Context{})
		if d.Level != Typed || d.Phrase != "acme-web" {
			t.Errorf("%v: level=%v phrase=%q", args, d.Level, d.Phrase)
		}
	}
}

// Ordinary mutating commands: typed on protected envs, yes/no elsewhere.
func TestProtectedEnvironments(t *testing.T) {
	cmds := []func(env string) []string{
		func(e string) []string { return []string{"env:clear-cache", "acme-web." + e} },
		func(e string) []string { return []string{"backup:create", "acme-web." + e, "--element=all"} },
		func(e string) []string { return []string{"env:wake", "acme-web." + e} },
		func(e string) []string { return []string{"remote:drush", "acme-web." + e, "--", "cr"} },
		func(e string) []string { return []string{"env:cc", "acme-web." + e} },
		func(e string) []string { return []string{"lock:enable", "acme-web." + e, "u", "p"} },
	}
	for _, tc := range []struct {
		policy Policy
		env    string
		want   Level
	}{
		{defaultPolicy, "dev", YesNo},
		{defaultPolicy, "feature", YesNo},
		{defaultPolicy, "test", Typed},
		{defaultPolicy, "live", Typed},
		{Policy{ProtectedEnvs: []string{"live"}}, "test", YesNo},
		{Policy{}, "live", Typed}, // live is protected even if unlisted
		{Policy{ProtectedEnvs: []string{"live", "test", "dev"}}, "dev", Typed},
	} {
		for _, mk := range cmds {
			args := mk(tc.env)
			d := tc.policy.Evaluate(args, Context{})
			if d.Level != tc.want {
				t.Errorf("protected=%v %v: level=%v want %v", tc.policy.ProtectedEnvs, args, d.Level, tc.want)
			}
			if d.Level == Typed && d.Phrase != "acme-web."+tc.env {
				t.Errorf("%v: phrase %q", args, d.Phrase)
			}
		}
	}
}

func TestDeployRules(t *testing.T) {
	p := Policy{ProtectedEnvs: []string{"live"}} // test unprotected to isolate the live rule
	if d := p.Evaluate([]string{"env:deploy", "acme-web.test", "--note=x"}, Context{}); d.Level != YesNo {
		t.Errorf("deploy test (unprotected) = %v", d.Level)
	}
	if d := p.Evaluate([]string{"env:deploy", "acme-web.live", "--note=x"}, Context{}); d.Level != Typed || d.Phrase != "acme-web.live" {
		t.Errorf("deploy live = %v %q", d.Level, d.Phrase)
	}
	if d := p.Evaluate([]string{"deploy", "acme-web.live"}, Context{}); d.Level != Typed {
		t.Errorf("alias deploy live = %v", d.Level)
	}
	if d := p.Evaluate([]string{"env:deploy", "acme-web.live", "--sync-content"}, Context{}); d.Level != Blocked {
		t.Errorf("deploy live --sync-content = %v", d.Level)
	}
	if d := defaultPolicy.Evaluate([]string{"env:deploy", "acme-web.test", "--sync-content"}, Context{}); d.Level != Typed {
		t.Errorf("deploy test --sync-content = %v", d.Level)
	}
}

func TestCloneContentRules(t *testing.T) {
	p := Policy{ProtectedEnvs: []string{"live"}}
	cases := []struct {
		args  []string
		level Level
		hint  string
	}{
		{[]string{"env:clone-content", "acme-web.live", "dev", "--db-only"}, YesNo, "acme-web.dev"},
		{[]string{"env:clone-content", "acme-web.live", "feature"}, YesNo, "acme-web.feature"},
		{[]string{"env:clone-content", "acme-web.live", "test"}, Typed, "acme-web.test"},
		{[]string{"env:clone-content", "acme-web.dev", "live"}, Blocked, "acme-web.live"},
		{[]string{"env:clone-content", "acme-web.test", "live", "--files-only"}, Blocked, "acme-web.live"},
	}
	for _, c := range cases {
		d := p.Evaluate(c.args, Context{})
		if d.Level != c.level || d.Target != c.hint {
			t.Errorf("%v: level=%v target=%q", c.args, d.Level, d.Target)
		}
	}
}

func TestConnectionSetRules(t *testing.T) {
	toGit := []string{"connection:set", "acme-web.feature", "git", "--yes"}
	if d := defaultPolicy.Evaluate(toGit, Context{}); d.Level != YesNo {
		t.Errorf("no changes: %v", d.Level)
	}
	if d := defaultPolicy.Evaluate(toGit, Context{UncommittedChanges: true}); d.Level != Typed || d.Phrase != "acme-web.feature" {
		t.Errorf("with changes: %v", d.Level)
	}
	toSFTP := []string{"connection:set", "acme-web.feature", "sftp"}
	if d := defaultPolicy.Evaluate(toSFTP, Context{UncommittedChanges: true}); d.Level != YesNo {
		t.Errorf("to sftp: %v", d.Level)
	}
}

func TestMultidevCreateTargetsNewEnv(t *testing.T) {
	d := defaultPolicy.Evaluate([]string{"multidev:create", "acme-web.live", "hotfix"}, Context{})
	if d.Level != YesNo || d.Target != "acme-web.hotfix" {
		t.Errorf("multidev from live: %v %q", d.Level, d.Target)
	}
}

func TestUnknownAndPluginCommands(t *testing.T) {
	d := defaultPolicy.Evaluate([]string{"build:env:delete:pr", "acme-web"}, Context{})
	if d.Level != YesNo {
		t.Errorf("plugin command: %v", d.Level)
	}
	p := Policy{ProtectedEnvs: []string{"live"}, ExtraDestructive: []string{"build:env:delete:*", "secret:*"}}
	if d := p.Evaluate([]string{"build:env:delete:pr", "acme-web"}, Context{}); d.Level != Typed || d.Phrase != "acme-web" {
		t.Errorf("extra destructive: %v %q", d.Level, d.Phrase)
	}
	if d := p.Evaluate([]string{"secret:site:set", "acme-web", "k", "v"}, Context{}); d.Level != Typed {
		t.Errorf("extra destructive glob: %v", d.Level)
	}
	if d := p.Evaluate([]string{"self:update"}, Context{}); d.Level != YesNo || d.Target != "self:update" {
		t.Errorf("no-arg mutating: %v %q", d.Level, d.Target)
	}
}

func TestReadOnlyBlocksAllMutating(t *testing.T) {
	p := Policy{ReadOnly: true}
	for _, args := range [][]string{{"env:clear-cache", "acme-web.dev"}, {"env:wake", "acme-web.dev"}, {"some:plugin", "x"}} {
		if d := p.Evaluate(args, Context{}); d.Level != Blocked {
			t.Errorf("%v: %v", args, d.Level)
		}
	}
	if d := p.Evaluate([]string{"site:list"}, Context{}); d.Level != None {
		t.Error("reads must still work in read_only")
	}
}

func TestApprove(t *testing.T) {
	cc := [][]string{{"env:clear-cache", "acme-web.live"}}
	if _, err := defaultPolicy.Approve(cc, Context{}, "acme-web.test", true); !errors.Is(err, ErrWrongPhrase) {
		t.Errorf("wrong phrase: %v", err)
	}
	if _, err := defaultPolicy.Approve(cc, Context{}, "", true); !errors.Is(err, ErrWrongPhrase) {
		t.Errorf("y/n is not enough for typed: %v", err)
	}
	if _, err := defaultPolicy.Approve(cc, Context{}, "acme-web.live", false); err != nil {
		t.Errorf("typed phrase: %v", err)
	}
	dev := [][]string{{"env:clear-cache", "acme-web.dev"}}
	if _, err := defaultPolicy.Approve(dev, Context{}, "", false); !errors.Is(err, ErrNotConfirmed) {
		t.Errorf("unconfirmed: %v", err)
	}
	if _, err := defaultPolicy.Approve([][]string{{"env:clone-content", "acme-web.dev", "live"}}, Context{}, "acme-web.live", true); !errors.Is(err, ErrBlocked) {
		t.Errorf("blocked: %v", err)
	}
	// A plan takes its strictest step: waking dev then drush on live.
	plan := [][]string{{"env:wake", "acme-web.dev"}, {"remote:drush", "acme-web.live", "--", "cr"}}
	if _, err := defaultPolicy.Approve(plan, Context{}, "", true); !errors.Is(err, ErrWrongPhrase) {
		t.Errorf("plan strictness: %v", err)
	}
}

// Nothing mutating reaches the runner without an approval for that exact
// argv.
func TestGuardRefusesUnconfirmed(t *testing.T) {
	m := mock(t)
	g := &Guard{Inner: m, Policy: defaultPolicy}
	ctx := context.Background()
	args := []string{"env:clear-cache", "acme-web.dev"}

	if _, _, err := g.Run(ctx, args...); !errors.Is(err, ErrNoApproval) {
		t.Errorf("Run without approval: %v", err)
	}
	lines, errc := g.Stream(ctx, args...)
	for range lines {
	}
	if err := <-errc; !errors.Is(err, ErrNoApproval) {
		t.Errorf("Stream without approval: %v", err)
	}
	if _, err := g.Command(ctx, "remote:drush", "acme-web.dev", "--", "cr"); !errors.Is(err, ErrNoApproval) {
		t.Errorf("Command without approval: %v", err)
	}

	a, err := defaultPolicy.Approve([][]string{args}, Context{}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	actx := WithApproval(ctx, a)
	if _, _, err := g.Run(actx, "env:clear-cache", "acme-web.test"); !errors.Is(err, ErrNoApproval) {
		t.Errorf("different argv: %v", err)
	}
	if _, _, err := g.Run(actx, append(args, "--extra")...); !errors.Is(err, ErrNoApproval) {
		t.Errorf("extra flag: %v", err)
	}
	if len(m.MutatingCalls()) != 0 {
		t.Fatalf("mutating calls leaked: %+v", m.MutatingCalls())
	}
	if _, _, err := g.Run(actx, args...); err != nil {
		t.Fatalf("approved run: %v", err)
	}
	if _, _, err := g.Run(actx, args...); !errors.Is(err, ErrNoApproval) {
		t.Errorf("approval reused: %v", err)
	}
	if n := len(m.MutatingCalls()); n != 1 {
		t.Errorf("mutating calls = %d, want 1", n)
	}
	if _, _, err := g.Run(ctx, "site:list", "--format=json"); err != nil {
		t.Errorf("read-only should pass: %v", err)
	}
}

// Approval made under one policy can't be stretched under a stricter one.
func TestGuardRechecksLevel(t *testing.T) {
	m := mock(t)
	loose := Policy{ProtectedEnvs: []string{"live"}}
	args := []string{"env:clear-cache", "acme-web.test"}
	a, err := loose.Approve([][]string{args}, Context{}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	g := &Guard{Inner: m, Policy: defaultPolicy} // test protected here
	if _, _, err := g.Run(WithApproval(context.Background(), a), args...); !errors.Is(err, ErrApprovalLevel) {
		t.Errorf("err = %v", err)
	}
	ro := &Guard{Inner: m, Policy: Policy{ReadOnly: true}}
	a2, _ := loose.Approve([][]string{{"env:wake", "acme-web.dev"}}, Context{}, "", true)
	if _, _, err := ro.Run(WithApproval(context.Background(), a2), "env:wake", "acme-web.dev"); !errors.Is(err, ErrBlocked) {
		t.Errorf("read_only guard: %v", err)
	}
	if len(m.MutatingCalls()) != 0 {
		t.Errorf("leaked: %+v", m.MutatingCalls())
	}
}

func TestGuardPlanSteps(t *testing.T) {
	m := mock(t)
	g := &Guard{Inner: m, Policy: defaultPolicy}
	plan := [][]string{{"env:wake", "acme-web.dev"}, {"env:clone-content", "acme-web.live", "dev", "--db-only", "--yes"}}
	a, err := defaultPolicy.Approve(plan, Context{}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithApproval(context.Background(), a)
	for _, s := range plan {
		if _, _, err := g.Run(ctx, s...); err != nil {
			t.Fatalf("%v: %v", s, err)
		}
	}
	if n := len(m.MutatingCalls()); n != 2 {
		t.Errorf("calls = %d", n)
	}
}
