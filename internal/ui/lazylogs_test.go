package ui

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/rithlath/lazyterminus/internal/terminus"
	"github.com/rithlath/lazyterminus/internal/ui/panels"
)

// lazyModel is a model on site "s" with two multidevs, feat and other, and
// the cursor on the named environment.
func lazyModel(t *testing.T, selected string) (Model, *siteState) {
	t.Helper()
	m := New(Options{Runner: testMock(t), Config: testConfig()})
	m.sites = []terminus.Site{{Name: "s"}, {Name: "t"}}
	for _, site := range []string{"s", "t"} {
		st := m.state(site)
		st.envsLoaded = true
		for _, id := range []string{"dev", "test", "live", "feat", "other"} {
			st.envs = append(st.envs, terminus.Env{ID: id, Initialized: true})
		}
	}
	for i, r := range m.envRows() {
		if r.Name == selected && !r.Divider {
			m.envList.Cursor = i
		}
	}
	if got := m.selectedEnv(); got != selected {
		t.Fatalf("selected env = %q, want %q", got, selected)
	}
	return m, m.state("s")
}

// loadingLogs lists the environments whose code log is in flight.
func loadingLogs(st *siteState) []string {
	var envs []string
	for k := range st.inflight {
		if env, ok := strings.CutPrefix(k, "codelog:"); ok {
			envs = append(envs, env)
		}
	}
	slices.Sort(envs)
	return envs
}

func TestEnvDataLoadsOnlyTheSelectedMultidevLog(t *testing.T) {
	for _, tc := range []struct {
		selected string
		want     []string
	}{
		{"dev", []string{"dev", "live", "test"}},
		{"feat", []string{"dev", "feat", "live", "test"}},
	} {
		m, st := lazyModel(t, tc.selected)
		m.ensureEnvData("s", false)
		if got := loadingLogs(st); !slices.Equal(got, tc.want) {
			t.Errorf("selected %s: loading logs = %v, want %v", tc.selected, got, tc.want)
		}
	}
}

// The pipeline draws every multidev's head commit and ahead count.
func TestEnvDataLoadsEveryMultidevLogForThePipeline(t *testing.T) {
	m, st := lazyModel(t, "dev")
	m.showPipeline = true
	m.ensureEnvData("s", false)
	if got, want := loadingLogs(st), []string{"dev", "feat", "live", "other", "test"}; !slices.Equal(got, want) {
		t.Errorf("loading logs = %v, want %v", got, want)
	}
}

// A forced reload refreshes the multidev logs already on screen and leaves
// the ones never visited alone.
func TestForcedReloadKeepsVisitedMultidevLogsFresh(t *testing.T) {
	m, st := lazyModel(t, "dev")
	st.codeLogs["other"] = []terminus.Commit{{Hash: "a"}}
	m.ensureEnvData("s", true)
	if got, want := loadingLogs(st), []string{"dev", "live", "other", "test"}; !slices.Equal(got, want) {
		t.Errorf("loading logs = %v, want %v", got, want)
	}
}

// The selection belongs to the selected site: loading another site's data
// must not pick up a multidev that happens to share the selected name.
func TestEnvDataForAnotherSiteSkipsMultidevLogs(t *testing.T) {
	m, _ := lazyModel(t, "feat")
	m.ensureEnvData("t", false)
	if got, want := loadingLogs(m.state("t")), []string{"dev", "live", "test"}; !slices.Equal(got, want) {
		t.Errorf("loading logs = %v, want %v", got, want)
	}
}

func TestMovingOntoMultidevLoadsItsLog(t *testing.T) {
	m, st := lazyModel(t, "live")
	m.focus = panelEnvs
	next, cmd := m.move(1)
	moved := next.(Model)
	if got := moved.selectedEnv(); got != "feat" {
		t.Fatalf("selected env = %q, want feat", got)
	}
	if !st.inflight["codelog:feat"] || cmd == nil {
		t.Errorf("moving onto feat did not start its code log load: in flight %v", st.inflight)
	}
	if st.inflight["codelog:other"] {
		t.Error("other's code log loaded without being selected")
	}
}

func TestOpeningPipelineLoadsMultidevLogs(t *testing.T) {
	m, st := lazyModel(t, "dev")
	next, _ := m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if !next.(Model).showPipeline {
		t.Fatal("p did not open the pipeline")
	}
	if !st.inflight["codelog:feat"] || !st.inflight["codelog:other"] {
		t.Errorf("opening the pipeline did not load multidev logs: in flight %v", st.inflight)
	}
}

// A multidev whose log was never requested is unknown, not healthy.
func TestUnvisitedMultidevRowIsUnknown(t *testing.T) {
	m, st := lazyModel(t, "dev")
	st.codeLogs["dev"] = []terminus.Commit{{Hash: "a"}}
	st.codeLogs["feat"] = []terminus.Commit{{Hash: "a"}}
	feat, other := st.envs[3], st.envs[4]
	if got := m.envRow("s", st, feat).State; got != panels.EnvOK {
		t.Errorf("loaded multidev state = %v, want EnvOK", got)
	}
	if got := m.envRow("s", st, other).State; got != panels.EnvUnknown {
		t.Errorf("unvisited multidev state = %v, want EnvUnknown", got)
	}
}

func codeLogCalls(mock *terminus.MockRunner, siteEnv string) int {
	n := 0
	for _, c := range mock.Calls() {
		if len(c.Args) > 1 && c.Args[0] == "env:code-log" && c.Args[1] == siteEnv {
			n++
		}
	}
	return n
}

// End to end: a multidev's code log is fetched when its row is selected,
// and its ahead badge appears then.
func TestMultidevLogLoadsWhenSelected(t *testing.T) {
	mock := testMock(t)
	h := startWith(t, mock, Options{Site: "acme-web", Env: "acme-web.test"}, false, 130, 40)
	h.waitText("[3] acme-web.test")
	settled := regexp.MustCompile(`● live +git ↑1`)
	unvisited := regexp.MustCompile(`feedback +sftp ✎2 uncommitted`)
	h.waitFor("settled rows", func(sc string) bool { return settled.MatchString(sc) && unvisited.MatchString(sc) })
	if n := codeLogCalls(mock, "acme-web.feedback"); n != 0 {
		t.Errorf("env:code-log acme-web.feedback ran %d times before it was selected", n)
	}
	if n := codeLogCalls(mock, "acme-web.dev"); n == 0 {
		t.Error("env:code-log acme-web.dev never ran")
	}
	for _, env := range []string{"live", "annual", "contact-us", "d11", "feedback"} {
		h.keys("j")
		h.waitText("[3] acme-web." + env)
	}
	h.waitText("sftp ↑1 ✎2 uncommitted")
}
