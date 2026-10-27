package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/rithlath/lazyterminus/internal/terminus"
)

func TestPipelineViewOpensAndSelects(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.test"}, 140, 40)
	h.waitText("[3] acme-web.test")
	h.keys("p")
	h.waitText("Pipeline · acme-web · test")
	h.waitText("╔═ test")
	h.waitText("Waiting to deploy dev → test (2 commits)")
	h.waitText("↓3 behind dev")
	h.waitText(`✗ test: Deploy code to "test" failed`)
	h.keys("right")
	h.waitText("Pipeline · acme-web · live")
	h.waitText("╔═ live")
	h.waitText("Waiting to deploy test → live (1 commit)")
	h.keys("left", "left")
	h.waitText("╔═ dev")
	h.keys("esc")
	h.waitText("[3] acme-web.dev") // selection is shared with the panels
}

func TestDeploySwitchesToPipelineAndLands(t *testing.T) {
	mock := testMock(t)
	mock.WorkflowDuration = 900 * time.Millisecond
	h := startWith(t, mock, Options{Site: "acme-web", Env: "acme-web.test"}, false, 140, 40)
	h.waitText("↑2 ready to deploy from dev")
	h.keys("d")
	h.waitText("Deploying promotes ALL 2 commit(s)")
	h.typeText("Ship it")
	h.keys("enter")
	h.waitText("Type acme-web.test to confirm")
	h.typeText("acme-web.test")
	h.keys("enter")
	h.waitText("Pipeline · acme-web")
	h.waitText("Moving dev → test (2 commits)")
	// Dots flow on the active hop while the deploy runs.
	h.waitFor("flowing dots", func(s string) bool { return strings.Contains(s, "━●") || strings.Contains(s, "●━") })
	h.waitText("✓ landed 2 commits")
	h.waitText("✓ deploy of 2 commits landed on test")
	h.waitText("3 commits waiting") // now waiting on live
}

func TestPipelineNarrowLayout(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev"}, 80, 50)
	h.waitText("[3] acme-web.dev")
	h.keys("p")
	h.waitText("MULTIDEVS → dev")
	h.waitText("▼")
	h.waitText("╔═ dev")
}

func TestPipelineNeverOverflows(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.test"}, 140, 40)
	h.waitText("sftp ↑1 ✎2")
	m := h.final()
	m.showPipeline = true
	for _, sz := range [][2]int{{30, 8}, {60, 20}, {80, 24}, {99, 30}, {100, 30}, {140, 40}, {220, 60}} {
		mm := m
		mm.width, mm.height = sz[0], sz[1]
		lines := strings.Split(ansi.Strip(mm.render()), "\n")
		if len(lines) > sz[1] {
			t.Errorf("%v: %d lines", sz, len(lines))
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w > sz[0] {
				t.Errorf("%v line %d is %d wide", sz, i, w)
			}
		}
	}
}

// Landings are also detected from workflow polling, including deploys that
// started and finished between two polls.
func TestRecordLandingsFromPolling(t *testing.T) {
	m := New(Options{Runner: testMock(t), Config: testConfig()})
	m.started = time.Unix(1_000, 0)
	st := m.state("s")
	st.codeLogs["dev"] = []terminus.Commit{{Hash: "a", Labels: "dev"}, {Hash: "b", Labels: "dev, test"}}
	test, dev := "test", "dev"
	running := terminus.Workflow{ID: "1", Env: &test, Workflow: `Deploy code to "test"`, Status: terminus.StatusRunning, StartedAt: 2_000}
	fin := terminus.Epoch(2_050)
	done := running
	done.Status, done.FinishedAt = terminus.StatusSucceeded, &fin
	m.recordLandings("s", []terminus.Workflow{running}, []terminus.Workflow{done})
	if l, ok := m.recentLanding("s", "test"); !ok || l.count != 1 || l.what != "deploy" {
		t.Fatalf("landing = %+v %v", l, ok)
	}
	// A code sync that started and finished between polls.
	sync := terminus.Workflow{ID: "2", Env: &dev, Workflow: `Sync code on "dev"`, Status: terminus.StatusSucceeded, StartedAt: 2_100, FinishedAt: &fin}
	m.recordLandings("s", []terminus.Workflow{done}, []terminus.Workflow{sync, done})
	if l, ok := m.recentLanding("s", "dev"); !ok || l.what != "code sync" {
		t.Errorf("sync landing = %+v %v", l, ok)
	}
	// Old history (before startup) is not a landing.
	old := terminus.Workflow{ID: "3", Env: &test, Workflow: `Deploy code to "live"`, Status: terminus.StatusSucceeded, StartedAt: 500, FinishedAt: &fin}
	m.recordLandings("s", []terminus.Workflow{}, []terminus.Workflow{old})
	if _, ok := m.recentLanding("s", "live"); ok {
		t.Error("history before startup recorded as a landing")
	}
}

func openPipelineOn(t *testing.T, mock *terminus.MockRunner, env string) *harness {
	t.Helper()
	h := startWith(t, mock, Options{Site: "acme-web", Env: "acme-web." + env}, false, 140, 40)
	h.waitText("[3] acme-web." + env)
	h.keys("p")
	h.waitText("Pipeline · acme-web")
	return h
}

func TestPipelineShowsAnyAction(t *testing.T) {
	mock := testMock(t)
	mock.WorkflowDuration = 1500 * time.Millisecond
	h := openPipelineOn(t, mock, "dev")
	h.keys("enter")
	h.waitText("› c  Clear cache")
	h.keys("enter", "y")
	h.waitText("clearing cache")            // on the box while it runs
	h.waitText("◐ clearing cache on dev")   // and in the footer
	h.waitText("Pipeline · acme-web")       // stays in the pipeline
	h.waitText("cache cleared ·")           // ✓ on the box when done
	h.waitText("✓ cache cleared on dev at") // and in the footer
}

func TestPipelineCloneArc(t *testing.T) {
	mock := testMock(t)
	mock.WorkflowDuration = 1500 * time.Millisecond
	h := openPipelineOn(t, mock, "dev")
	h.keys("l") // action keys work in the pipeline too
	h.waitText("Clone content · acme-web")
	h.keys("enter", "y")
	h.waitText("copying database and files from live → dev") // arc label
	h.waitText("▼")
	h.waitFor("dots on the arc", func(s string) bool { return strings.Contains(s, "━●") || strings.Contains(s, "●━") })
	h.waitText("✓ database and files cloned from live on dev")
}

func TestPipelineShowsJobFailure(t *testing.T) {
	mock := testMock(t)
	mock.FailNext("backup:create", "Backup quota exceeded")
	h := openPipelineOn(t, mock, "dev")
	h.keys("b", "enter", "y")
	h.waitText("✗ creating backup failed")
	h.waitText("Backup quota exceeded")
}

func TestDescribeStep(t *testing.T) {
	cases := map[string][]string{
		"copying database from live|live|dev": {"env:clone-content", "s.live", "dev", "--db-only", "--yes"},
		"switching to git||dev":               {"connection:set", "s.dev", "git", "--yes"},
		"creating multidev hotfix||dev":       {"multidev:create", "s.dev", "hotfix"},
		"clearing cache||test":                {"env:cc", "s.test"},
		"running build:env:delete:pr||x":      {"build:env:delete:pr", "s.x"},
	}
	for want, args := range cases {
		i, ok := describeStep(args)
		got := i.doing + "|" + i.from + "|" + i.env
		if !ok || got != want {
			t.Errorf("%v: got %q want %q", args, got, want)
		}
	}
	if _, ok := describeStep([]string{"site:list"}); ok {
		t.Error("no site.env: not an environment step")
	}
}
