// Package ui implements the lazyterminus Bubble Tea interface.
package ui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/rithlath/lazyterminus/internal/catalog"
	"github.com/rithlath/lazyterminus/internal/config"
	"github.com/rithlath/lazyterminus/internal/jobs"
	"github.com/rithlath/lazyterminus/internal/runs"
	"github.com/rithlath/lazyterminus/internal/terminus"
	"github.com/rithlath/lazyterminus/internal/ui/panels"
	"github.com/rithlath/lazyterminus/internal/ui/theme"
)

// Options configure the root model.
type Options struct {
	Runner terminus.Runner
	// Recorder holds the commands Runner has run, for the History view.
	Recorder *terminus.Recorder
	Config   config.Config
	Site     string // --site: initially selected site
	Env      string // --env site.env: initially focused environment
	Mock     bool
	DryRun   bool
	// Now overrides the clock (tests).
	Now func() time.Time
	// CatalogDir overrides where the command catalog is cached (tests).
	CatalogDir string
	// OpenURL overrides opening a URL in the browser (tests).
	OpenURL func(url string) error
	// Clipboard overrides the system copy tool (tests).
	Clipboard func(s string) error
	// HistoryLog is the history log file, read back for each environment's
	// local command history.
	HistoryLog string
	// RunsDir is where background runs keep their output and exit code.
	RunsDir string
	// NoExecPause skips "press Enter to return" after an interactive
	// command (tests).
	NoExecPause bool
}

// Panel identifiers, in tab order.
const (
	panelSites = iota
	panelEnvs
	panelDetail
	panelWorkflows
	numPanels
)

var panelNames = [numPanels]string{"Sites", "Environments", "Detail", "Workflows"}

// loadingSites is the status until site:list returns.
const loadingSites = "Loading sites…"

// Model is the root model.
type Model struct {
	opts   Options
	cfg    config.Config
	client *terminus.Client
	keys   KeyMap

	width, height int
	focus         int

	sites       []terminus.Site
	sitesErr    error
	sitesLoaded bool
	data        map[string]*siteState

	siteList, envList, wfList panels.List
	detailOffset              int
	pendingEnv                string // from --env, applied once envs load

	pollSeq  int
	spinner  spinner.Model
	bar      progress.Model
	spinning bool
	pulse    int // spinner ticks so far; drives the shimmer on running lines

	status      string
	statusErr   bool
	account     string // logged-in Terminus account, from auth:whoami
	loggedOut   bool
	authPending bool // a recheckAuth whoami is in flight

	zones       *zoneList // where this frame drew clickable things (set by View)
	lastClick   string    // last click's target, for double clicks
	lastClickAt time.Time

	showHelp   bool
	showOutput bool
	output     []string
	outputView viewport.Model

	modal        modal
	jobs         *jobs.Queue
	afterJob     map[int]func(*Model) tea.Cmd
	afterJobWhat map[int]string // the command afterJob runs, for the quit prompt
	runs         []*runs.Run    // background runs, oldest first

	showHistory        bool
	historyWindow      int // index into historyWindows
	historyChangesOnly bool
	historyOffset      int

	showPipeline bool
	frame        int
	animating    bool
	landed       map[string]map[string]landing
	started      time.Time

	localLog []terminus.Logged // the history log, oldest first

	catalog         *catalog.Catalog
	catalogLoading  bool
	paletteWanted   bool
	showOutputOnJob bool
}

// New returns the root model.
func New(opts Options) Model {
	keys, errs := NewKeyMap(opts.Config.Keymap)
	m := Model{
		catalogLoading: true,
		opts:           opts,
		cfg:            opts.Config,
		client:         terminus.NewClient(opts.Runner),
		keys:           keys,
		focus:          panelSites,
		data:           map[string]*siteState{},
		spinner:        spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(theme.Warn)),
		bar: progress.New(progress.WithColors(theme.Amber, theme.Green), progress.WithoutPercentage(),
			progress.WithFillCharacters('█', '░')),
		outputView: viewport.New(),
		status:     loadingSites,
		jobs:       jobs.New(opts.Runner),
		afterJob:   map[int]func(*Model) tea.Cmd{}, afterJobWhat: map[int]string{},
		landed: map[string]map[string]landing{},
	}
	m.started = m.now()
	m.jobs.Now = m.now
	if opts.Env != "" {
		_, m.pendingEnv, _ = terminus.SplitSiteEnv(opts.Env)
		m.focus = panelEnvs
	}
	for _, e := range errs {
		m.logOutput("config: " + e.Error())
	}
	return m
}

func (m *Model) now() time.Time {
	if m.opts.Now != nil {
		return m.opts.Now()
	}
	return time.Now()
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	// The catalog loads in the background at startup so ":" opens instantly.
	return tea.Batch(checkAuth(m.client), loadSites(m.client, false), m.spinner.Tick, clockTick(),
		loadCatalog(m.opts.Runner, m.catalogDir(), false), loadLocalLog(m.opts.HistoryLog), loadRuns(m.opts.RunsDir))
}

// ---- selection helpers ----------------------------------------------------

func (m *Model) site() string {
	if len(m.sites) == 0 {
		return ""
	}
	return m.sites[min(m.siteList.Cursor, len(m.sites)-1)].Name
}

func (m *Model) state(site string) *siteState {
	s, ok := m.data[site]
	if !ok {
		s = newSiteState()
		m.data[site] = s
	}
	return s
}

// envRows returns environment rows for the selected site, with a divider
// before multidevs.
func (m *Model) envRows() []panels.EnvRow {
	site := m.site()
	st := m.state(site)
	var rows []panels.EnvRow
	divided := false
	for _, e := range st.envs {
		if e.IsMultidev() && !divided {
			rows = append(rows, panels.EnvRow{Name: "multidevs", Divider: true})
			divided = true
		}
		rows = append(rows, m.envRow(site, st, e))
	}
	return rows
}

func selectable(rows []panels.EnvRow) []bool {
	s := make([]bool, len(rows))
	for i, r := range rows {
		s[i] = !r.Divider
	}
	return s
}

// selectedEnv returns the selected environment ID or "".
func (m *Model) selectedEnv() string {
	rows := m.envRows()
	if m.envList.Cursor < len(rows) && !rows[m.envList.Cursor].Divider {
		return rows[m.envList.Cursor].Name
	}
	return ""
}

func (m *Model) protected(env string) bool { return slices.Contains(m.cfg.ProtectedEnvs, env) }

// ahead returns the commits relevant to env's "ahead" badge and what they
// are relative to. known is false until the needed code logs have loaded.
func (m *Model) ahead(st *siteState, env string) (commits []terminus.Commit, target, label string, known bool) {
	switch env {
	case "test", "live":
		src := terminus.SourceEnv(env)
		log, ok := st.codeLogs[src]
		return terminus.DeployableCommits(log, src, env), src, "ready to deploy from " + src, ok
	case "dev":
		log, ok := st.codeLogs["dev"]
		return terminus.DeployableCommits(log, "dev", "test"), "test", "not yet on test", ok
	default:
		mdLog, ok1 := st.codeLogs[env]
		devLog, ok2 := st.codeLogs["dev"]
		return terminus.CommitsNotIn(mdLog, devLog), "dev", "ahead of dev", ok1 && ok2
	}
}

func (m *Model) envRow(site string, st *siteState, e terminus.Env) panels.EnvRow {
	r := panels.EnvRow{Name: e.ID, Mode: e.ConnectionMode, Locked: e.Locked, Uninit: !e.Initialized,
		Ahead: -1, Changes: -1, State: panels.EnvOK, Protected: m.protected(e.ID)}
	if e.ID != "dev" { // dev's pending commits show on test as "ready to deploy"
		if commits, _, _, known := m.ahead(st, e.ID); known {
			r.Ahead = len(commits)
		}
	}
	if err := st.codeLogErr[e.ID]; err != nil {
		r.Err = err.Error()
	}
	if e.ConnectionMode == "sftp" {
		if ds, ok := st.diffstat[e.ID]; ok {
			r.Changes = len(ds)
		}
	}
	if r.Ahead > 0 || r.Changes > 0 || r.Uninit {
		r.State = panels.EnvAttention
	}
	if _, failed := terminus.LastFailed(st.wfs, e.ID); failed {
		r.State = panels.EnvFailed
	}
	for _, wf := range st.wfs {
		if wf.EnvName() == e.ID && wf.Running() {
			r.State = panels.EnvRunning
			break
		}
	}
	_ = site
	return r
}

// busy reports whether anything is loading or running, which drives the
// spinner and the faster poll interval.
func (m *Model) busy() bool {
	if len(m.jobs.Running()) > 0 || len(m.runningRuns()) > 0 {
		return true
	}
	for _, st := range m.data {
		if len(st.inflight) > 0 {
			return true
		}
	}
	if st, ok := m.data[m.site()]; ok {
		for _, wf := range st.wfs {
			if wf.Running() {
				return true
			}
		}
	}
	return !m.sitesLoaded
}

func (m *Model) pollInterval() time.Duration {
	d := time.Duration(m.cfg.PollInterval)
	if m.busy() && d > terminus.TTLWorkflowsBusy {
		return terminus.TTLWorkflowsBusy
	}
	return d
}

// ---- loading orchestration --------------------------------------------------

// ensureSite starts any loads the selected site still needs.
func (m *Model) ensureSite(site string, force bool) tea.Cmd {
	if site == "" {
		return nil
	}
	st := m.state(site)
	var cmds []tea.Cmd
	if (force || !st.envsLoaded) && st.begin("envs") {
		cmds = append(cmds, loadEnvs(m.client, site, force))
	}
	if (force || !st.wfLoaded) && st.begin("wfs") {
		cmds = append(cmds, loadWorkflows(m.client, site, false, force))
	}
	if !st.drupalLoaded && st.begin("drupal") {
		cmds = append(cmds, loadDrupal(site, m.cfg.RepoFor(site)))
	}
	if st.envsLoaded {
		cmds = append(cmds, m.ensureEnvData(site, force))
	}
	return tea.Batch(cmds...)
}

// ensureEnvData loads code logs (for deploy/ahead counts) and diffstats
// (for SFTP environments) once a site's env list is known.
func (m *Model) ensureEnvData(site string, force bool) tea.Cmd {
	st := m.state(site)
	var cmds []tea.Cmd
	need := func(env string) {
		if _, ok := st.codeLogs[env]; (force || !ok) && st.begin("codelog:"+env) {
			cmds = append(cmds, loadCodeLog(m.client, site, env, m.cfg.RepoFor(site), force))
		}
	}
	need("dev")
	need("test")
	need("live") // current commit on live, for the pipeline view
	for _, e := range st.envs {
		if e.IsMultidev() {
			need(e.ID)
		}
		if e.ConnectionMode == "sftp" {
			if _, ok := st.diffstat[e.ID]; (force || !ok) && st.begin("diffstat:"+e.ID) {
				cmds = append(cmds, loadDiffstat(m.client, site, e.ID, force))
			}
		}
	}
	return tea.Batch(cmds...)
}

// ensureOps loads operations for running workflows (progress) and for the
// selected environment's latest failure (reason).
func (m *Model) ensureOps(site string) tea.Cmd {
	st := m.state(site)
	var cmds []tea.Cmd
	for _, wf := range st.wfs {
		if wf.Running() && st.begin("ops:"+wf.ID) {
			cmds = append(cmds, loadOps(m.client, site, wf.ID, false))
		}
	}
	if env := m.selectedEnv(); env != "" {
		if wf, ok := terminus.LastFailed(st.wfs, env); ok {
			if _, have := st.ops[wf.ID]; !have && st.begin("ops:"+wf.ID) {
				cmds = append(cmds, loadOps(m.client, site, wf.ID, true))
			}
		}
	}
	return tea.Batch(cmds...)
}

// selectSite switches the active site and restarts polling.
func (m *Model) selectSite(i int) tea.Cmd {
	if i == m.siteList.Cursor && m.pollSeq > 0 {
		return nil
	}
	m.siteList.Cursor = i
	m.envList = panels.List{}
	m.wfList = panels.List{}
	m.detailOffset = 0
	m.pollSeq++
	site := m.site()
	return tea.Batch(m.ensureSite(site, false), pollAfter(m.pollInterval(), site, m.pollSeq), m.startSpinner())
}

func (m *Model) startSpinner() tea.Cmd {
	if m.spinning {
		return nil
	}
	m.spinning = true
	return m.spinner.Tick
}

func (m *Model) setStatus(s string, isErr bool) {
	m.status, m.statusErr = s, isErr
}

func (m *Model) logOutput(lines ...string) {
	ts := m.now().Format("15:04:05")
	for _, l := range lines {
		m.output = append(m.output, theme.MutedText.Render(ts)+" "+l)
	}
	if len(m.output) > 2000 {
		m.output = m.output[len(m.output)-2000:]
	}
	m.outputView.SetContent(strings.Join(m.output, "\n"))
	m.outputView.GotoBottom()
}

// recheckAuth re-runs auth:whoami when a terminus call succeeds while the
// logged-out badge is up, so logging in elsewhere clears it without r.
func (m *Model) recheckAuth() tea.Cmd {
	if !m.loggedOut || m.authPending {
		return nil
	}
	m.authPending = true
	return checkAuth(m.client)
}

func (m *Model) reportErr(context string, err error) {
	if terminus.IsNotLoggedIn(err) {
		// Session missing or expired: one clear instruction instead of
		// Terminus's error repeated for every call.
		m.loggedOut, m.account = true, ""
		msg := "Run terminus auth:login --machine-token=<token> in another pane, then press r on Sites."
		if m.status != msg {
			m.logOutput(theme.Fail.Render(fmt.Sprintf("%s %s: %v", theme.SymFailed, context, err)))
		}
		m.setStatus(msg, true)
		return
	}
	msg := fmt.Sprintf("%s %s: %v", theme.SymFailed, context, err)
	m.setStatus(msg, true)
	m.logOutput(theme.Fail.Render(msg))
}

// ---- update -------------------------------------------------------------------

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.outputView.SetWidth(max(msg.Width-4, 1))
		m.outputView.SetHeight(max(msg.Height-4, 1))
		return m, nil

	case tea.KeyPressMsg:
		if cur := m.modal; cur != nil && msg.String() != "ctrl+c" {
			done, cmd := cur.update(&m, msg)
			if done && m.modal == cur { // a form may have opened its confirmation
				m.modal = nil
			}
			return m, cmd
		}
		return m.handleKey(msg)

	case jobs.StepMsg, jobs.LineMsg, jobs.DoneMsg:
		return m, m.onJobMsg(msg)

	case clickMsg:
		return m.handleClick(msg)

	case deployPreviewMsg:
		return m, m.onDeployPreview(msg)

	case diffPreviewMsg:
		return m, m.onDiffPreview(msg)

	case upstreamMsg:
		m.onUpstream(msg)
		return m, nil

	case catalogMsg:
		return m, m.onCatalog(msg)

	case branchMsg:
		st := m.state(msg.site)
		delete(st.inflight, "branch:"+msg.branch)
		st.branches[msg.branch] = msg.status
		return m, nil

	case lazygitDoneMsg:
		return m, m.onLazygitDone(msg)

	case paletteResultMsg:
		m.onPaletteResult(msg)
		return m, nil

	case execDoneMsg:
		if msg.err != nil {
			m.reportErr(msg.title, msg.err)
		} else {
			m.setStatus(theme.SymSuccess+" "+msg.title+" exited", false)
		}
		m.client.InvalidateSite(msg.site)
		m.state(msg.site).inflight = map[string]bool{}
		return m, tea.Batch(m.ensureSite(msg.site, true), loadLocalLog(m.opts.HistoryLog))

	case localLogMsg:
		m.localLog = msg.entries
		return m, nil

	case spinner.TickMsg:
		if !m.busy() {
			m.spinning = false
			return m, nil
		}
		m.pulse++
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case authMsg:
		m.authPending = false
		if msg.err != nil {
			m.reportErr("auth:whoami", msg.err)
			return m, nil
		}
		wasOut := m.loggedOut
		m.account, m.loggedOut = msg.account, false
		if wasOut {
			m.setStatus("Logged in as "+msg.account, false)
		}
		return m, nil

	case animMsg:
		return m, m.onAnim()

	case clockMsg:
		return m, tea.Batch(clockTick(), m.pollRuns()) // re-render relative times

	case runsLoadedMsg:
		m.onRunsLoaded(msg)
		return m, tea.Batch(m.startSpinner(), m.startAnim())

	case sitesLoadedMsg:
		first := !m.sitesLoaded
		m.sitesLoaded = true
		if msg.err != nil {
			m.sitesErr = msg.err
			m.reportErr("site:list", msg.err)
			return m, nil
		}
		m.sitesErr = nil
		var sites []terminus.Site
		for _, s := range msg.sites {
			if m.cfg.Allowed(s.Name) {
				sites = append(sites, s)
			}
		}
		prev := m.site()
		m.sites = sites
		if len(sites) == 0 {
			m.setStatus("No sites found (check the sites allowlist in "+m.cfg.Path+")", true)
			return m, nil
		}
		if !first || m.status == "" || m.status == loadingSites { // at startup, keep a notice already showing (runs that ended while closed)
			m.setStatus(fmt.Sprintf("%d sites", len(sites)), false)
		}
		want := prev
		if want == "" {
			want = m.opts.Site
		}
		idx := 0
		for i, s := range sites {
			if s.Name == want {
				idx = i
			}
		}
		if m.opts.Site != "" && prev == "" && sites[idx].Name != m.opts.Site {
			m.reportErr("--site", fmt.Errorf("site %q not found", m.opts.Site))
		}
		m.pollSeq = 0
		cmds := []tea.Cmd{m.selectSite(idx)}
		// Load env lists for every site so the Sites panel can show counts.
		for _, s := range sites {
			if st := m.state(s.Name); !st.envsLoaded && st.begin("envs") {
				cmds = append(cmds, loadEnvs(m.client, s.Name, false))
			}
		}
		return m, tea.Batch(cmds...)

	case envsLoadedMsg:
		st := m.state(msg.site)
		delete(st.inflight, "envs")
		st.envsLoaded = true
		if msg.err != nil {
			st.envsErr = msg.err
			m.reportErr("env:list "+msg.site, msg.err)
			return m, nil
		}
		st.envsErr = nil
		prevEnv := ""
		if msg.site == m.site() {
			prevEnv = m.selectedEnv()
		}
		st.envs = msg.envs
		if msg.site != m.site() {
			return m, nil
		}
		m.restoreEnvCursor(prevEnv)
		return m, tea.Batch(m.ensureEnvData(msg.site, false), m.ensureOps(msg.site), m.ensureBranch(), m.ensureDomains())

	case workflowsMsg:
		st := m.state(msg.site)
		delete(st.inflight, "wfs")
		st.wfLoaded = true
		if msg.err != nil {
			st.wfErr = msg.err
			m.reportErr("workflow:list "+msg.site, msg.err)
			return m, nil
		}
		st.wfErr = nil
		finished := finishedSince(st.wfs, msg.wfs)
		if st.wfSeen { // landings only for changes watched after the first list
			m.recordLandings(msg.site, st.wfs, msg.wfs)
		}
		st.wfs, st.wfSeen = msg.wfs, true
		var cmds []tea.Cmd
		if len(finished) > 0 {
			// Something completed on Pantheon: env data may have changed.
			for _, wf := range finished {
				sym := theme.OK.Render(theme.SymSuccess)
				if wf.Status == terminus.StatusFailed {
					sym = theme.Fail.Render(theme.SymFailed)
				}
				m.logOutput(fmt.Sprintf("%s %s: %s (%s)", sym, msg.site, wf.Workflow, wf.Status))
			}
			m.client.InvalidateSite(msg.site)
			cmds = append(cmds, m.ensureSite(msg.site, true))
		}
		cmds = append(cmds, m.ensureOps(msg.site), m.startSpinner(), m.startAnim(), m.recheckAuth())
		return m, tea.Batch(cmds...)

	case codeLogMsg:
		st := m.state(msg.site)
		delete(st.inflight, "codelog:"+msg.env)
		if msg.err != nil {
			st.codeLogErr[msg.env] = msg.err
			m.reportErr("env:code-log "+msg.site+"."+msg.env, msg.err)
			return m, nil
		}
		delete(st.codeLogErr, msg.env)
		st.codeLogs[msg.env] = msg.commits
		return m, nil

	case diffstatMsg:
		st := m.state(msg.site)
		delete(st.inflight, "diffstat:"+msg.env)
		if msg.err != nil {
			st.diffErr[msg.env] = msg.err
			m.reportErr("env:diffstat "+msg.site+"."+msg.env, msg.err)
			return m, nil
		}
		delete(st.diffErr, msg.env)
		st.diffstat[msg.env] = msg.entries
		return m, nil

	case opsMsg:
		st := m.state(msg.site)
		delete(st.inflight, "ops:"+msg.id)
		if msg.err != nil {
			m.logOutput(theme.Fail.Render(fmt.Sprintf("%s workflow:info:operations %s: %v", theme.SymFailed, msg.id, msg.err)))
			return m, nil
		}
		st.ops[msg.id] = msg.ops
		return m, nil

	case domainsMsg:
		m.onDomains(msg)
		return m, nil

	case openedMsg:
		m.onOpened(msg)
		return m, nil

	case copiedMsg:
		m.onCopied(msg)
		return m, nil

	case drupalMsg:
		st := m.state(msg.site)
		delete(st.inflight, "drupal")
		st.drupal, st.drupalNote, st.drupalLoaded = msg.version, msg.note, true
		return m, nil

	case pollMsg:
		if msg.seq != m.pollSeq || msg.site != m.site() {
			return m, nil // superseded by a site switch
		}
		st := m.state(msg.site)
		var cmds []tea.Cmd
		if st.begin("wfs") {
			cmds = append(cmds, loadWorkflows(m.client, msg.site, m.busy(), true))
		}
		cmds = append(cmds, pollAfter(m.pollInterval(), msg.site, m.pollSeq))
		return m, tea.Batch(cmds...)

	case errMsg:
		m.reportErr(msg.context, msg.err)
		return m, nil
	}
	return m, nil
}

// finishedSince returns workflows that were running in prev and are finished
// in next.
func finishedSince(prev, next []terminus.Workflow) []terminus.Workflow {
	running := map[string]bool{}
	for _, w := range prev {
		if w.Running() {
			running[w.ID] = true
		}
	}
	var out []terminus.Workflow
	for _, w := range next {
		if running[w.ID] && !w.Running() {
			out = append(out, w)
		}
	}
	return out
}

// restoreEnvCursor keeps the selection on the same environment after a
// reload, or applies --env on first load.
func (m *Model) restoreEnvCursor(prev string) {
	want := prev
	if m.pendingEnv != "" {
		want, m.pendingEnv = m.pendingEnv, ""
		if m.state(m.site()).env(want) == nil {
			m.reportErr("--env", fmt.Errorf("environment %q not found on %s", want, m.site()))
		}
	}
	rows := m.envRows()
	for i, r := range rows {
		if !r.Divider && r.Name == want {
			m.envList.Cursor = i
		}
	}
	m.envList.Clamp(selectable(rows))
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if m.showHelp {
		if k == "esc" || m.keys.Action(k) == ActHelp || m.keys.Action(k) == ActQuit && k != "ctrl+c" {
			m.showHelp = false
			return m, nil
		}
		if k == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}
	if m.showOutput {
		switch {
		case k == "esc" || m.keys.Action(k) == ActOutput || m.keys.Action(k) == ActQuit && k != "ctrl+c":
			m.showOutput = false
			return m, nil
		case k == "ctrl+c":
			return m, tea.Quit
		}
		var cmd tea.Cmd
		m.outputView, cmd = m.outputView.Update(msg)
		return m, cmd
	}

	if m.showHistory {
		return m, m.historyKey(k)
	}
	if m.showPipeline {
		if cmd, ok := m.pipelineKey(k); ok {
			return m, cmd
		}
	}
	switch action := m.keys.Action(k); action {
	case ActQuit:
		if lines, ask := m.quitPrompt(); ask && k != "ctrl+c" {
			m.modal = &promptModal{title: "Quit", question: "Still running. If you quit now:", detail: lines,
				outro: "Quit anyway?", onYes: func(m *Model) tea.Cmd { m.jobs.CancelAll(); return tea.Quit }}
			return m, nil
		}
		m.jobs.CancelAll()
		return m, tea.Quit
	case ActDeploy, ActClearCache, ActBackup, ActCloneContent, ActConnectionMode, ActWake,
		ActDrush, ActCommit, ActCreateMultidev, ActDeleteMultidev, ActUpstream:
		return m, m.runAction(action)
	case ActOpenURL:
		return m, m.openEnvURL()
	case ActCopyURL:
		return m, m.copyEnvURL()
	case ActHelp:
		m.showHelp = true
	case ActOutput:
		m.showOutput = true
	case ActHistory:
		m.showHistory, m.historyOffset = true, 0
	case ActPipeline:
		m.showPipeline = !m.showPipeline
		if m.showPipeline && m.selectedEnv() == "" {
			return m, tea.Batch(m.selectEnv("dev"), m.startAnim())
		}
		return m, m.startAnim()
	case ActNextPanel:
		m.focus = (m.focus + 1) % numPanels
	case ActPrevPanel:
		m.focus = (m.focus + numPanels - 1) % numPanels
	case ActPanel1:
		m.focus = panelSites
	case ActPanel2:
		m.focus = panelEnvs
	case ActPanel3:
		m.focus = panelDetail
	case ActPanel4:
		m.focus = panelWorkflows
	case ActUp:
		return m.move(-1)
	case ActDown:
		return m.move(1)
	case ActSelect:
		switch m.focus {
		case panelSites:
			m.focus = panelEnvs
		case panelEnvs, panelDetail:
			return m, m.openEnvMenu()
		}
	case ActRefresh:
		return m, m.refresh()
	case ActPalette:
		return m, m.openPalette()
	case ActLazygit:
		return m, m.openLazygit()
	}
	return m, nil
}

func (m Model) move(delta int) (tea.Model, tea.Cmd) {
	switch m.focus {
	case panelSites:
		if len(m.sites) == 0 {
			return m, nil
		}
		i := min(max(m.siteList.Cursor+delta, 0), len(m.sites)-1)
		return m, m.selectSite(i)
	case panelEnvs:
		rows := m.envRows()
		m.envList.Move(selectable(rows), delta)
		m.detailOffset = 0
		return m, tea.Batch(m.ensureOps(m.site()), m.ensureBranch(), m.ensureDomains())
	case panelDetail:
		m.detailOffset = max(m.detailOffset+delta, 0)
	case panelWorkflows:
		m.wfList.Cursor = max(m.wfList.Cursor+delta, 0)
	}
	return m, nil
}

// refresh forces a reload of the focused panel's data.
func (m *Model) refresh() tea.Cmd {
	site := m.site()
	m.setStatus("Refreshing "+strings.ToLower(panelNames[m.focus])+"…", false)
	switch m.focus {
	case panelSites:
		m.client.Cache.DeletePrefix("sites|")
		cmds := []tea.Cmd{checkAuth(m.client), loadSites(m.client, true)}
		for _, s := range m.sites {
			if st := m.state(s.Name); st.begin("envs") {
				cmds = append(cmds, loadEnvs(m.client, s.Name, true))
			}
		}
		return tea.Batch(append(cmds, m.startSpinner())...)
	case panelEnvs:
		if site == "" {
			return nil
		}
		m.client.InvalidateSite(site)
		st := m.state(site)
		st.inflight = map[string]bool{}
		st.codeLogs, st.diffstat = map[string][]terminus.Commit{}, map[string][]terminus.DiffstatEntry{}
		st.domains = map[string][]terminus.Domain{}
		return tea.Batch(m.ensureSite(site, true), m.startSpinner())
	case panelDetail:
		env := m.selectedEnv()
		if site == "" || env == "" {
			return nil
		}
		st := m.state(site)
		m.client.InvalidateEnv(site, env)
		var cmds []tea.Cmd
		if st.begin("codelog:" + env) {
			cmds = append(cmds, loadCodeLog(m.client, site, env, m.cfg.RepoFor(site), true))
		}
		if src := terminus.SourceEnv(env); src != "" && st.begin("codelog:"+src) {
			cmds = append(cmds, loadCodeLog(m.client, site, src, m.cfg.RepoFor(site), true))
		}
		if e := st.env(env); e != nil && e.ConnectionMode == "sftp" && st.begin("diffstat:"+env) {
			cmds = append(cmds, loadDiffstat(m.client, site, env, true))
		}
		delete(st.domains, env)
		st.drupalLoaded = false
		cmds = append(cmds, m.ensureSite(site, false), m.ensureDomains(), m.startSpinner())
		return tea.Batch(cmds...)
	case panelWorkflows:
		if site == "" {
			return nil
		}
		st := m.state(site)
		if !st.begin("wfs") {
			return nil
		}
		return tea.Batch(loadWorkflows(m.client, site, m.busy(), true), m.startSpinner())
	}
	return nil
}
