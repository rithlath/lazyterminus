package terminus

import "testing"

func TestDeployableCommitsFromRealLogs(t *testing.T) {
	dev, _ := ParseCodeLog(fixture(t, "code_log_acme-web_dev.json"))
	test, _ := ParseCodeLog(fixture(t, "code_log_acme-web_test.json"))
	// The captured site was fully deployed: nothing pending either way.
	if n := len(DeployableCommits(dev, "dev", "test")); n != 0 {
		t.Errorf("dev→test deployable = %d, want 0", n)
	}
	if n := len(DeployableCommits(test, "test", "live")); n != 0 {
		t.Errorf("test→live deployable = %d, want 0", n)
	}
}

func TestDeployableCommits(t *testing.T) {
	log := []Commit{
		{Hash: "a", Labels: "dev"},
		{Hash: "b", Labels: "dev, test"},
		{Hash: "c", Labels: "test, live, dev"},
	}
	if got := DeployableCommits(log, "dev", "test"); len(got) != 1 || got[0].Hash != "a" {
		t.Errorf("dev→test = %+v", got)
	}
	if got := DeployableCommits(log, "test", "live"); len(got) != 1 || got[0].Hash != "b" {
		t.Errorf("test→live = %+v", got)
	}
}

func TestCommitsNotIn(t *testing.T) {
	md, _ := ParseCodeLog(fixture(t, "code_log_acme-web_d11.json"))
	dev, _ := ParseCodeLog(fixture(t, "code_log_acme-web_dev.json"))
	// In the capture, 11 of d11's 15 newest commits are not in dev's log.
	if got := CommitsNotIn(md, dev); len(got) != 11 {
		t.Errorf("d11 ahead of dev = %d of %d, want 11", len(got), len(md))
	}
	if got := CommitsNotIn(dev, dev); len(got) != 0 {
		t.Errorf("log vs itself = %d", len(got))
	}
}

func TestEnvRelations(t *testing.T) {
	cases := []struct{ env, source, next string }{
		{"dev", "", "test"}, {"test", "dev", "live"}, {"live", "test", ""}, {"feature", "", "dev"},
	}
	for _, c := range cases {
		if got := SourceEnv(c.env); got != c.source {
			t.Errorf("SourceEnv(%s) = %q", c.env, got)
		}
		if got := NextEnv(c.env); got != c.next {
			t.Errorf("NextEnv(%s) = %q", c.env, got)
		}
	}
}

func TestSplitSiteEnv(t *testing.T) {
	if s, e, ok := SplitSiteEnv("acme-intranet.live"); !ok || s != "acme-intranet" || e != "live" {
		t.Errorf("got %s %s %v", s, e, ok)
	}
	if _, _, ok := SplitSiteEnv("acme-web"); ok {
		t.Error("no dot should not split")
	}
}

func TestArgs(t *testing.T) {
	a := Args{"remote:drush", "s.dev", "--retry=2", "--", "cr", "--help"}
	if a.Command() != "remote:drush" {
		t.Error(a.Command())
	}
	if p := a.Positionals(); len(p) != 3 || p[0] != "s.dev" || p[2] != "--help" {
		t.Errorf("positionals = %q", p)
	}
	if a.HasFlag("help") {
		t.Error("--help after -- is a drush arg, not a terminus flag")
	}
	if v, _ := a.Flag("retry"); v != "2" {
		t.Error(v)
	}
	if got := (Args{"env:deploy", "s.live", "--note=it's done"}).String(); got != `terminus env:deploy s.live '--note=it'\''s done'` {
		t.Error(got)
	}
}

func TestIsMutating(t *testing.T) {
	for _, args := range [][]string{
		{"env:deploy", "s.test"}, {"deploy", "s.test"}, {"env:delete", "s.md"}, {"build:env:delete:pr", "s"},
		{"remote:drush", "s.dev", "--", "status"}, {"some:plugin:list", "s"},
	} {
		if !IsMutating(args) {
			t.Errorf("%v should be mutating", args)
		}
	}
	for _, args := range [][]string{
		{"site:list"}, {"--version"}, {"env:deploy", "--help"}, {"workflow:info:operations", "s", "--id=x"},
	} {
		if IsMutating(args) {
			t.Errorf("%v should be read-only", args)
		}
	}
}

func TestLastFailedSupersededBySuccess(t *testing.T) {
	env := "test"
	wfs := []Workflow{
		{ID: "4", Env: &env, Workflow: "Clear cache", Status: StatusFailed},
		{ID: "3", Env: &env, Workflow: "Deploy", Status: StatusSucceeded},
		{ID: "2", Env: &env, Workflow: "Deploy", Status: StatusFailed},
	}
	f, ok := LastFailed(wfs, "test")
	if !ok || f.ID != "4" {
		t.Errorf("got %+v %v", f, ok)
	}
	if f, ok := LastFailed(wfs[1:], "test"); ok {
		t.Errorf("superseded deploy failure reported: %+v", f)
	}
	if f, ok := LastFailed(wfs[2:], "test"); !ok || f.ID != "2" {
		t.Errorf("unresolved failure missed: %+v", f)
	}
}
