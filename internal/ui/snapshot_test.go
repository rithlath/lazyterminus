package ui

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/rithlath/lazyterminus/internal/config"
	"github.com/rithlath/lazyterminus/internal/terminus"
)

func envList(ids ...string) []terminus.Env {
	var envs []terminus.Env
	for _, id := range ids {
		envs = append(envs, terminus.Env{ID: id, ConnectionMode: "git", Initialized: true})
	}
	return envs
}

// snapModel is a model that has just received a snapshot of sites s and t,
// where only s has a saved environment list.
func snapModel(t *testing.T, opts Options) (Model, *siteState) {
	t.Helper()
	opts.Runner, opts.CatalogDir = testMock(t), t.TempDir()
	if opts.Config.MaxConcurrent == 0 {
		opts.Config = testConfig()
	}
	m := New(opts)
	m.width, m.height = 130, 40
	next, _ := m.Update(snapshotMsg{
		sites: []terminus.Site{{Name: "s"}, {Name: "t"}},
		envs:  map[string][]terminus.Env{"s": envList("dev", "test", "live", "feat", "other")},
	})
	m = next.(Model)
	return m, m.state("s")
}

func update(t *testing.T, m Model, msg any) Model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(Model)
}

func envNames(m Model) []string {
	var names []string
	for _, r := range m.envRows() {
		if !r.Divider {
			names = append(names, r.Name)
		}
	}
	return names
}

// The snapshot is drawn at once and the loads that follow from it start
// without waiting for site:list or env:list.
func TestSnapshotDrawsAndStartsLoads(t *testing.T) {
	m, st := snapModel(t, Options{})
	if m.site() != "s" || len(m.sites) != 2 {
		t.Fatalf("site = %q of %d sites, want s of 2", m.site(), len(m.sites))
	}
	if got, want := envNames(m), []string{"dev", "test", "live", "feat", "other"}; !slices.Equal(got, want) {
		t.Errorf("env rows = %v, want %v", got, want)
	}
	for _, key := range []string{"envs", "wfs", "codelog:dev", "codelog:test", "codelog:live"} {
		if !st.inflight[key] {
			t.Errorf("%s is not loading; in flight: %v", key, st.inflight)
		}
	}
	screen := ansi.Strip(m.render())
	if !strings.Contains(screen, "5 envs · refreshing") {
		t.Errorf("environments footer does not say the list is being refreshed:\n%s", screen)
	}
	if !strings.Contains(screen, "1 of 2 · refreshing") {
		t.Errorf("sites footer does not say the list is being refreshed:\n%s", screen)
	}
	if !regexp.MustCompile(`› s +5 envs`).MatchString(screen) {
		t.Errorf("sites panel does not show the saved env count:\n%s", screen)
	}
}

// If site:list fails, the saved sites stay on screen and say what they are.
func TestFailedSiteListKeepsTheSnapshot(t *testing.T) {
	m, _ := snapModel(t, Options{})
	m = update(t, m, sitesLoadedMsg{err: errors.New("boom")})
	if len(m.sites) != 2 || m.site() != "s" {
		t.Errorf("sites = %v, want the 2 saved sites", m.sites)
	}
	if screen := ansi.Strip(m.render()); !strings.Contains(screen, "1 of 2 · from last run") {
		t.Errorf("sites footer does not mark the list as saved:\n%s", screen)
	}
}

// Terminus answering must not undo what the user did while it was working.
func TestFreshSiteListKeepsTheSelection(t *testing.T) {
	m, _ := snapModel(t, Options{})
	m.focus = panelEnvs
	next, _ := m.move(1)
	m = next.(Model)
	if m.selectedEnv() != "test" {
		t.Fatalf("selected env = %q, want test", m.selectedEnv())
	}
	seq := m.pollSeq
	m = update(t, m, sitesLoadedMsg{sites: []terminus.Site{{Name: "a"}, {Name: "s"}, {Name: "t"}}})
	if m.site() != "s" || m.selectedEnv() != "test" {
		t.Errorf("after site:list: site %q env %q, want s test", m.site(), m.selectedEnv())
	}
	if m.pollSeq != seq {
		t.Error("polling restarted although the site did not change")
	}
}

func TestSnapshotIgnoredOnceTerminusAnswered(t *testing.T) {
	m := New(Options{Runner: testMock(t), Config: testConfig(), CatalogDir: t.TempDir()})
	m = update(t, m, sitesLoadedMsg{sites: []terminus.Site{{Name: "fresh"}}})
	m = update(t, m, snapshotMsg{sites: []terminus.Site{{Name: "s"}, {Name: "t"}}})
	if len(m.sites) != 1 || m.site() != "fresh" {
		t.Errorf("sites = %v, want only fresh", m.sites)
	}
}

func TestSnapshotRespectsTheAllowlist(t *testing.T) {
	cfg := testConfig()
	cfg.Sites = []config.Site{{Name: "t"}}
	m, _ := snapModel(t, Options{Config: cfg})
	if len(m.sites) != 1 || m.site() != "t" {
		t.Errorf("sites = %v, want only t", m.sites)
	}
}

// --env is applied from the snapshot once, so the cursor does not jump back
// when env:list answers.
func TestEnvFlagAppliesFromSnapshot(t *testing.T) {
	m, st := snapModel(t, Options{Site: "s", Env: "s.feat"})
	if m.selectedEnv() != "feat" {
		t.Fatalf("selected env = %q, want feat", m.selectedEnv())
	}
	if !st.inflight["codelog:feat"] {
		t.Errorf("feat's code log is not loading; in flight: %v", st.inflight)
	}
	next, _ := m.move(1)
	m = update(t, next.(Model), envsLoadedMsg{site: "s", envs: envList("dev", "test", "live", "feat", "other")})
	if m.selectedEnv() != "other" {
		t.Errorf("selected env = %q after env:list, want other", m.selectedEnv())
	}
}

// A --site the snapshot has never seen is still honored when site:list
// finds it.
func TestSiteFlagAppliesFromFreshListWhenSnapshotLacksIt(t *testing.T) {
	m, _ := snapModel(t, Options{Site: "z"})
	if m.site() != "s" {
		t.Fatalf("site = %q, want s until site:list answers", m.site())
	}
	m = update(t, m, sitesLoadedMsg{sites: []terminus.Site{{Name: "s"}, {Name: "t"}, {Name: "z"}}})
	if m.site() != "z" {
		t.Errorf("site = %q, want z", m.site())
	}
	if m.statusErr {
		t.Errorf("status reports an error: %q", m.status)
	}
}

func TestFreshEnvListReplacesTheSnapshot(t *testing.T) {
	m, st := snapModel(t, Options{})
	m = update(t, m, envsLoadedMsg{site: "s", envs: envList("dev", "test", "live")})
	if got, want := envNames(m), []string{"dev", "test", "live"}; !slices.Equal(got, want) {
		t.Errorf("env rows = %v, want %v", got, want)
	}
	if st.envsCached || !st.envsLoaded {
		t.Errorf("envsCached %v envsLoaded %v, want false true", st.envsCached, st.envsLoaded)
	}
	if screen := ansi.Strip(m.render()); strings.Contains(screen, "envs ·") {
		t.Errorf("environments footer still marks the list as saved:\n%s", screen)
	}
}

// If env:list fails, the saved list stays on screen and says what it is.
func TestFailedEnvListKeepsTheSnapshot(t *testing.T) {
	m, st := snapModel(t, Options{})
	m = update(t, m, envsLoadedMsg{site: "s", err: errors.New("boom")})
	if len(envNames(m)) != 5 || !st.envsCached {
		t.Errorf("env rows = %v, envsCached %v; want the 5 saved rows", envNames(m), st.envsCached)
	}
	if screen := ansi.Strip(m.render()); !strings.Contains(screen, "5 envs · from last run") {
		t.Errorf("footer does not mark the list as saved:\n%s", screen)
	}
}

func envListCalls(mock *terminus.MockRunner) []string {
	var sites []string
	for _, c := range mock.Calls() {
		if len(c.Args) > 1 && c.Args[0] == "env:list" {
			sites = append(sites, c.Args[1])
		}
	}
	slices.Sort(sites)
	return slices.Compact(sites)
}

// End to end: rows from the previous run show while Terminus is still
// working, then the fresh list replaces them.
func TestStartupDrawsSnapshotThenRefreshes(t *testing.T) {
	dir := t.TempDir()
	seed := terminus.NewClient(testMock(t))
	sites, err := seed.Sites(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	envs, err := seed.Envs(context.Background(), "acme-web", false)
	if err != nil {
		t.Fatal(err)
	}
	envs = append(envs, terminus.Env{ID: "only-in-snapshot", ConnectionMode: "git", Initialized: true})
	store := snapshotStore(dir)
	if err := errors.Join(store.SaveSites(sites), store.SaveEnvs("acme-web", envs)); err != nil {
		t.Fatal(err)
	}
	mock := testMock(t)
	mock.MinLatency, mock.MaxLatency = 400*time.Millisecond, 400*time.Millisecond
	h := startWith(t, mock, Options{Site: "acme-web", CatalogDir: dir}, false, 130, 40)
	h.waitFor("the saved rows, marked as refreshing", func(sc string) bool {
		return strings.Contains(sc, "only-in-snapshot") && strings.Contains(sc, "envs · refreshing")
	})
	h.waitFor("the fresh rows", func(sc string) bool {
		return !strings.Contains(sc, "only-in-snapshot") && !strings.Contains(sc, "refreshing") && strings.Contains(sc, "feedback")
	})
}

// End to end: a run saves what Terminus returned, and asks for the
// environment list of the selected site only.
func TestStartupSavesSnapshotAndListsOnlyTheSelectedSite(t *testing.T) {
	dir := t.TempDir()
	mock := testMock(t)
	h := startWith(t, mock, Options{Site: "acme-web", CatalogDir: dir}, false, 130, 40)
	h.waitText("[3] acme-web.dev")
	store := snapshotStore(dir)
	h.waitFor("the saved snapshot", func(string) bool {
		sites, ok := store.Sites()
		envs, ok2 := store.Envs("acme-web")
		return ok && ok2 && len(sites) == 4 && len(envs) == 12
	})
	if got := envListCalls(mock); !slices.Equal(got, []string{"acme-web"}) {
		t.Errorf("env:list ran for %v, want only acme-web", got)
	}
	h.keys("j") // sandbox-12
	h.waitText("Environments · sandbox-12")
	h.waitFor("env:list for the newly selected site", func(string) bool {
		return slices.Contains(envListCalls(mock), "sandbox-12")
	})
	h.waitFor("its saved snapshot", func(string) bool { _, ok := store.Envs("sandbox-12"); return ok })
}
