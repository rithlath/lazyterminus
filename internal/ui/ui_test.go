package ui

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/rithlath/lazyterminus/internal/config"
	"github.com/rithlath/lazyterminus/internal/safety"
	"github.com/rithlath/lazyterminus/internal/terminus"
)

func testMock(t *testing.T) *terminus.MockRunner {
	t.Helper()
	m, err := terminus.NewMockRunner(os.DirFS(filepath.Join("..", "..", "testdata")))
	if err != nil {
		t.Fatal(err)
	}
	m.MinLatency, m.MaxLatency = 0, 0
	m.WorkflowDuration = 150 * time.Millisecond
	m.NoPause = true
	return m
}

func testConfig() config.Config {
	cfg := config.Default()
	cfg.Path = "/tmp/lazyterminus-test.yaml"
	return cfg
}

// probe wraps the root model and records the latest rendered frame (ANSI
// stripped) so tests can wait on screen content.
type probe struct {
	tea.Model
	frame *atomic.Value
	model *atomic.Value
}

func (p probe) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := p.Model.Update(msg)
	p.model.Store(m)
	return probe{m, p.frame, p.model}, cmd
}

func (p probe) View() tea.View {
	v := p.Model.View()
	p.frame.Store(ansi.Strip(v.Content))
	return v
}

type harness struct {
	t     *testing.T
	tm    *teatest.TestModel
	frame *atomic.Value
	model *atomic.Value
	mock  *terminus.MockRunner
	dry   *terminus.DryRun
}

// start runs the root model against a mock behind the same runner chain
// main uses (Guard → DryRun → History → Limited → Recorder), so every mutating command
// in these tests must pass the safety layer.
func start(t *testing.T, opts Options, w, h int) *harness {
	t.Helper()
	return startWith(t, nil, opts, false, w, h)
}

func startWith(t *testing.T, mock *terminus.MockRunner, opts Options, dryRun bool, w, h int) *harness {
	t.Helper()
	if mock == nil {
		mock = testMock(t)
	}
	if opts.Config.MaxConcurrent == 0 {
		opts.Config = testConfig()
	}
	var dry *terminus.DryRun
	opts.Runner, dry, opts.Recorder = safety.Chain(mock, opts.Config, filepath.Join(t.TempDir(), "history.log"), dryRun)
	opts.DryRun = dryRun
	if opts.CatalogDir == "" {
		opts.CatalogDir = t.TempDir()
	}
	frame, model := &atomic.Value{}, &atomic.Value{}
	frame.Store("")
	p := probe{New(opts), frame, model}
	tm := teatest.NewTestModel(t, p, teatest.WithInitialTermSize(w, h))
	hs := &harness{t: t, tm: tm, frame: frame, model: model, mock: mock, dry: dry}
	t.Cleanup(func() { _ = tm.Quit() })
	return hs
}

func (h *harness) screen() string { return h.frame.Load().(string) }

func (h *harness) root() Model {
	if m, ok := h.model.Load().(Model); ok {
		return m
	}
	return Model{}
}

// final stops the program and returns the last model, which is then safe to
// use from the test goroutine.
func (h *harness) final() Model {
	h.t.Helper()
	if err := h.tm.Quit(); err != nil {
		h.t.Fatal(err)
	}
	return h.tm.FinalModel(h.t, teatest.WithFinalTimeout(2*time.Second)).(probe).Model.(Model)
}

func (h *harness) waitFor(desc string, cond func(screen string) bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond(h.screen()) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %s; screen:\n%s", desc, h.screen())
}

func (h *harness) waitText(s string) {
	h.t.Helper()
	h.waitFor("text "+s, func(sc string) bool { return strings.Contains(sc, s) })
}

func (h *harness) keys(ks ...string) {
	for _, k := range ks {
		h.tm.Send(keyMsg(k))
		time.Sleep(5 * time.Millisecond)
	}
}

func (h *harness) typeText(s string) {
	for _, r := range s {
		h.tm.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	time.Sleep(5 * time.Millisecond)
}

// keyMsg builds a key press for a key string as KeyPressMsg.String()
// reports it.
func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(k)[0]
	if r >= 'A' && r <= 'Z' {
		return tea.KeyPressMsg{Code: r + ('a' - 'A'), ShiftedCode: r, Text: k, Mod: tea.ModShift}
	}
	return tea.KeyPressMsg{Code: r, Text: k}
}

func TestKeyMsgStrings(t *testing.T) {
	for _, k := range []string{"tab", "shift+tab", "enter", "esc", "D", "j", ":", "?", "ctrl+c", "1"} {
		if got := keyMsg(k).String(); got != k {
			t.Errorf("keyMsg(%q).String() = %q", k, got)
		}
	}
}

func TestStartsAndQuits(t *testing.T) {
	h := start(t, Options{}, 130, 36)
	h.waitText("acme-web")
	h.keys("q")
	h.tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}

func TestNavigationAndFocus(t *testing.T) {
	h := start(t, Options{Site: "acme-web"}, 130, 36)
	h.waitText("feedback")
	if h.root().focus != panelSites {
		t.Fatal("sites should be focused at start")
	}
	h.keys("tab")
	h.waitFor("envs focus", func(string) bool { return h.root().focus == panelEnvs })
	h.keys("j")
	h.waitText("[3] acme-web.test")
	h.waitText("ready to deploy from dev")
	h.keys("j", "j") // live, then skip the divider to the first multidev
	h.waitText("[3] acme-web.annual")
	h.keys("4")
	h.waitFor("workflows focus", func(string) bool { return h.root().focus == panelWorkflows })
	h.keys("shift+tab", "shift+tab", "shift+tab")
	h.waitFor("sites focus", func(string) bool { return h.root().focus == panelSites })
	h.keys("k") // acme-intranet ← acme-portal ← acme-web
	h.waitText("Environments · acme-portal")
	h.keys("?")
	h.waitText("delete_multidev")
	h.keys("esc")
	h.waitFor("help closed", func(s string) bool { return !strings.Contains(s, "delete_multidev") })
}

func TestEnvFlagFocusesEnvironment(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.feedback"}, 130, 36)
	h.waitText("[3] acme-web.feedback")
	h.waitText("✎ 2 files (+253 −3)")
	h.waitText("↑1 ahead of dev")
	if h.root().focus != panelEnvs {
		t.Error("--env should focus the environments panel")
	}
}

func TestEnvBadgesAndFailure(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.test"}, 130, 40)
	h.waitText("Bring application container to desired configuration state")
	for _, want := range []string{"test        git ↑2 failed", "live        git ↑1", "sftp ↑1 ✎2 uncommitted", "── multidevs"} {
		h.waitText(want)
	}
}

func TestResizeSwitchesLayout(t *testing.T) {
	h := start(t, Options{Site: "acme-web"}, 130, 36)
	h.waitText("feedback")
	firstLine := func() string { return strings.SplitN(h.screen(), "\n", 2)[0] }
	h.waitFor("wide layout", func(string) bool {
		return strings.Contains(firstLine(), "[1] Sites") && strings.Contains(firstLine(), "[3]")
	})
	h.tm.Send(tea.WindowSizeMsg{Width: 80, Height: 40})
	h.waitFor("narrow layout", func(string) bool {
		return strings.Contains(firstLine(), "[1] Sites") && !strings.Contains(firstLine(), "[3]")
	})
	h.tm.Send(tea.WindowSizeMsg{Width: WideWidth, Height: 30})
	h.waitFor("wide at exactly 100", func(string) bool { return strings.Contains(firstLine(), "[3]") })
	h.tm.Send(tea.WindowSizeMsg{Width: WideWidth - 1, Height: 30})
	h.waitFor("narrow at 99", func(string) bool { return !strings.Contains(firstLine(), "[3]") })
}

func TestErrorsShowInStatusAndOutput(t *testing.T) {
	mock := testMock(t)
	mock.FailNext("workflow:list", "Pantheon API unavailable")
	h := startWith(t, mock, Options{Site: "acme-web"}, false, 130, 36)
	h.waitText("Pantheon API unavailable")
	h.keys("O")
	h.waitText("─ Output ─")
	h.waitText("workflow:list acme-web: terminus workflow:list: Pantheon API unavailable")
	h.keys("esc", "4", "r")
	h.waitText("Automated backup")
}

// TestRenderNeverOverflows renders the fully loaded model at many sizes and
// checks every frame fits the terminal exactly.
func TestRenderNeverOverflows(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.test"}, 130, 36)
	h.waitText("Bring application container")
	h.waitText("sftp ↑1 ✎2")
	m := h.final()
	for _, sz := range [][2]int{{40, 12}, {60, 20}, {80, 24}, {99, 30}, {100, 30}, {120, 40}, {200, 60}, {30, 8}} {
		for focus := range numPanels {
			mm := m
			mm.width, mm.height, mm.focus = sz[0], sz[1], focus
			out := ansi.Strip(mm.render())
			lines := strings.Split(out, "\n")
			if len(lines) > sz[1] {
				t.Errorf("%dx%d focus %d: %d lines", sz[0], sz[1], focus, len(lines))
			}
			for i, l := range lines {
				if w := ansi.StringWidth(l); w > sz[0] {
					t.Errorf("%dx%d focus %d line %d is %d wide: %q", sz[0], sz[1], focus, i, w, l)
				}
			}
		}
	}
}

func TestPollingPicksUpNewWorkflows(t *testing.T) {
	mock := testMock(t)
	cfg := testConfig()
	cfg.PollInterval = config.Duration(50 * time.Millisecond) // normalize() is bypassed here on purpose
	h := startWith(t, mock, Options{Config: cfg, Site: "acme-web"}, false, 130, 36)
	h.waitText("Automated backup")
	go func() { _, _, _ = mock.Run(t.Context(), "env:clear-cache", "acme-web.dev") }()
	h.waitText(`Clear cache for "dev"`)
}
