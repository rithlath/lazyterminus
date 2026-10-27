package terminus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os/exec"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
)

// MockCall records one invocation of the mock runner.
type MockCall struct {
	Args     []string
	Mutating bool
	At       time.Time
}

// MockRunner serves Terminus output from fixtures and simulates mutating
// commands: each creates a workflow that progresses over WorkflowDuration
// and then applies its effect (a deploy moves commit labels, connection:set
// flips the mode, multidev:create adds an environment, ...).
type MockRunner struct {
	// MinLatency and MaxLatency bound the simulated delay of every call.
	MinLatency, MaxLatency time.Duration
	// WorkflowDuration is how long a simulated mutating command takes.
	WorkflowDuration time.Duration
	// Now is the clock; defaults to time.Now.
	Now func() time.Time
	// NoPause makes interactive commands exit immediately instead of
	// waiting for enter (tests).
	NoPause bool

	mu        sync.Mutex
	calls     []MockCall
	failing   map[string]string // command → error message for next call
	loggedOut bool
	w         *mockWorld
	catalog   []byte
}

type mockSite struct {
	site      Site
	envs      []Env
	commits   []mockCommit // newest first
	workflows []*mockWorkflow
	diffstat  map[string][]DiffstatEntry
	backups   map[string][]Backup
	upstream  []UpstreamUpdate
}

type mockCommit struct {
	Commit
	labels []string
}

type mockWorkflow struct {
	wf       Workflow
	ops      []WorkflowOp
	duration time.Duration // 0 for already-finished fixture workflows
}

type mockWorld struct {
	sites  []*mockSite
	asleep map[string]bool // site.env
}

// MockScenario is the overlay in testdata/mock/scenario.json that adds the
// states real fixtures don't happen to contain (pending commits, SFTP mode,
// failures).
type MockScenario struct {
	Commits        map[string][]Commit                  `json:"commits"`
	EnvOverrides   map[string]map[string]map[string]any `json:"env_overrides"`
	Diffstat       map[string][]DiffstatEntry           `json:"diffstat"`
	Workflows      map[string][]Workflow                `json:"workflows"`
	WorkflowOps    map[string]string                    `json:"workflow_ops"`
	Asleep         []string                             `json:"asleep"`
	Upstream       map[string][]UpstreamUpdate          `json:"upstream"`
	PluginCommands []json.RawMessage                    `json:"plugin_commands"`
}

// NewMockRunner loads fixtures from fsys, which must contain real/ (scrubbed
// captures) and mock/scenario.json.
func NewMockRunner(fsys fs.FS) (*MockRunner, error) {
	m := &MockRunner{
		MinLatency:       300 * time.Millisecond,
		MaxLatency:       1500 * time.Millisecond,
		WorkflowDuration: 8 * time.Second,
		failing:          map[string]string{},
	}
	if err := m.load(fsys); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *MockRunner) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func readJSON(fsys fs.FS, name string, v any) (bool, error) {
	b, err := fs.ReadFile(fsys, name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(b, v)
}

func (m *MockRunner) load(fsys fs.FS) error {
	b, err := fs.ReadFile(fsys, "real/site_list.json")
	if err != nil {
		return err
	}
	sites, err := ParseSites(b)
	if err != nil {
		return err
	}
	var sc MockScenario
	if _, err := readJSON(fsys, "mock/scenario.json", &sc); err != nil {
		return fmt.Errorf("scenario: %w", err)
	}
	w := &mockWorld{asleep: map[string]bool{}}
	for _, s := range sites {
		ms := &mockSite{site: s, diffstat: map[string][]DiffstatEntry{}, backups: map[string][]Backup{}}
		if b, err := fs.ReadFile(fsys, "real/env_list_"+s.Name+".json"); err == nil {
			if ms.envs, err = ParseEnvs(b); err != nil {
				return fmt.Errorf("%s envs: %w", s.Name, err)
			}
		} else {
			ms.envs = defaultEnvs(s.Name, s.Created)
		}
		if err := m.loadCommits(fsys, ms, sc.Commits[s.Name]); err != nil {
			return err
		}
		if b, err := fs.ReadFile(fsys, "real/workflow_list_"+s.Name+".json"); err == nil {
			wfs, err := ParseWorkflows(b)
			if err != nil {
				return fmt.Errorf("%s workflows: %w", s.Name, err)
			}
			for _, wf := range wfs {
				ms.workflows = append(ms.workflows, &mockWorkflow{wf: wf})
			}
		}
		for _, wf := range sc.Workflows[s.Name] {
			ms.workflows = append(ms.workflows, &mockWorkflow{wf: wf})
		}
		slices.SortStableFunc(ms.workflows, func(a, b *mockWorkflow) int {
			return cmpEpochDesc(a.wf.StartedAt, b.wf.StartedAt)
		})
		for env, ov := range sc.EnvOverrides[s.Name] {
			for i := range ms.envs {
				if ms.envs[i].ID == env {
					applyEnvOverride(&ms.envs[i], ov)
				}
			}
		}
		for _, e := range ms.envs {
			var bs []Backup
			if ok, err := readJSON(fsys, "real/backup_list_"+s.Name+"_"+e.ID+".json", &json.RawMessage{}); ok && err == nil {
				raw, _ := fs.ReadFile(fsys, "real/backup_list_"+s.Name+"_"+e.ID+".json")
				bs, _ = ParseBackups(raw)
			}
			ms.backups[e.ID] = bs
		}
		ms.upstream = sc.Upstream[s.Name]
		w.sites = append(w.sites, ms)
	}
	for key, ds := range sc.Diffstat {
		site, env, _ := SplitSiteEnv(key)
		if ms := w.site(site); ms != nil {
			ms.diffstat[env] = ds
		}
	}
	// Attach operations to fixture workflows: scenario-mapped files first,
	// then a generic "all succeeded" list for everything else.
	for _, ms := range w.sites {
		for _, mw := range ms.workflows {
			if file, ok := sc.WorkflowOps[mw.wf.ID]; ok {
				raw, err := fs.ReadFile(fsys, path.Join("real", file))
				if err != nil {
					return err
				}
				if mw.ops, err = ParseWorkflowOps(raw); err != nil {
					return err
				}
			} else {
				mw.ops = finishedOps(mw.wf.Status)
			}
		}
	}
	for _, se := range sc.Asleep {
		w.asleep[se] = true
	}
	m.w = w

	cat, err := fs.ReadFile(fsys, "real/list.json")
	if err != nil {
		return err
	}
	if len(sc.PluginCommands) > 0 {
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(cat, &doc); err != nil {
			return err
		}
		var cmds []json.RawMessage
		if err := json.Unmarshal(doc["commands"], &cmds); err != nil {
			return err
		}
		cmds = append(cmds, sc.PluginCommands...)
		doc["commands"], _ = json.Marshal(cmds)
		cat, _ = json.Marshal(doc)
	}
	m.catalog = cat
	return nil
}

func cmpEpochDesc(a, b Epoch) int {
	switch {
	case a > b:
		return -1
	case a < b:
		return 1
	}
	return 0
}

func defaultEnvs(site string, created Epoch) []Env {
	var envs []Env
	for _, id := range []string{"dev", "test", "live"} {
		envs = append(envs, Env{ID: id, Created: created, Domain: id + "-" + site + ".pantheonsite.io",
			ConnectionMode: "git", Initialized: true, PHPVersion: "8.3", PHPRuntimeGeneration: 2})
	}
	return envs
}

func applyEnvOverride(e *Env, ov map[string]any) {
	if v, ok := ov["connection_mode"].(string); ok {
		e.ConnectionMode = v
	}
	if v, ok := ov["locked"].(bool); ok {
		e.Locked = v
	}
	if v, ok := ov["php_version"].(string); ok {
		e.PHPVersion = v
	}
	if v, ok := ov["initialized"].(bool); ok {
		e.Initialized = v
	}
}

// loadCommits merges every env's code log into one labeled commit list, then
// prepends scenario commits.
func (m *MockRunner) loadCommits(fsys fs.FS, ms *mockSite, extra []Commit) error {
	byHash := map[string]*mockCommit{}
	var order []*mockCommit
	add := func(c Commit) {
		if mc, ok := byHash[c.Hash]; ok {
			for l := range c.LabelSet() {
				if !slices.Contains(mc.labels, l) {
					mc.labels = append(mc.labels, l)
				}
			}
			return
		}
		mc := &mockCommit{Commit: c}
		for l := range c.LabelSet() {
			mc.labels = append(mc.labels, l)
		}
		byHash[c.Hash] = mc
		order = append(order, mc)
	}
	found := false
	var branched []string // multidevs without a captured log
	for _, e := range ms.envs {
		b, err := fs.ReadFile(fsys, "real/code_log_"+ms.site.Name+"_"+e.ID+".json")
		if err != nil {
			if e.IsMultidev() {
				branched = append(branched, e.ID)
			}
			continue
		}
		cs, err := ParseCodeLog(b)
		if err != nil {
			return fmt.Errorf("%s %s code log: %w", ms.site.Name, e.ID, err)
		}
		found = true
		for _, c := range cs {
			add(c)
		}
	}
	if !found {
		base := ms.site.Created.Time()
		for i := range 5 {
			add(Commit{Datetime: base.Add(time.Duration(i) * 24 * time.Hour).UTC().Format("2006-01-02T15:04:05"),
				Author: "Developer A", Labels: "dev, test, live", Hash: fmt.Sprintf("%040x", i+1+len(ms.site.Name)*1000),
				Message: fmt.Sprintf("Initial commit %d", i+1)})
		}
	}
	// Multidevs without a captured log look as if branched from dev.
	for _, mc := range order {
		if slices.Contains(mc.labels, "dev") {
			mc.labels = append(mc.labels, branched...)
		}
	}
	for _, c := range extra {
		add(c)
	}
	slices.SortStableFunc(order, func(a, b *mockCommit) int { return strings.Compare(b.Datetime, a.Datetime) })
	for _, mc := range order {
		ms.commits = append(ms.commits, *mc)
	}
	return nil
}

func finishedOps(status string) []WorkflowOp {
	res := status
	if res == StatusRunning {
		res = ""
	}
	descs := []string{"Set target Git commit", "Bring application container to desired configuration state", "Clear CMS cache"}
	ops := make([]WorkflowOp, len(descs))
	for i, d := range descs {
		r := res
		ops[i] = WorkflowOp{ID: fmt.Sprintf("op-%d", i), Type: "platform", Result: &r, Description: d}
	}
	return ops
}

func (w *mockWorld) site(name string) *mockSite {
	for _, s := range w.sites {
		if s.site.Name == name || s.site.ID == name {
			return s
		}
	}
	return nil
}

func (ms *mockSite) env(id string) *Env {
	for i := range ms.envs {
		if ms.envs[i].ID == id {
			return &ms.envs[i]
		}
	}
	return nil
}

// Calls returns every recorded call.
func (m *MockRunner) Calls() []MockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.calls)
}

// MutatingCalls returns recorded calls that would change state.
func (m *MockRunner) MutatingCalls() []MockCall {
	var out []MockCall
	for _, c := range m.Calls() {
		if c.Mutating {
			out = append(out, c)
		}
	}
	return out
}

// FailNext makes the next invocation of command fail with msg.
func (m *MockRunner) FailNext(command, msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failing[command] = msg
}

// SetLoggedIn simulates having (or not having) a Terminus session. Logged
// out, auth:whoami prints only a notice and every command that talks to
// Pantheon fails the way Terminus does.
func (m *MockRunner) SetLoggedIn(in bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loggedOut = !in
}

// Asleep reports whether the mock considers site.env asleep.
func (m *MockRunner) Asleep(siteEnv string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.w.asleep[siteEnv]
}

func (m *MockRunner) latency(ctx context.Context) error {
	if m.MaxLatency <= 0 {
		return ctx.Err()
	}
	d := m.MinLatency
	if m.MaxLatency > m.MinLatency {
		d += time.Duration(rand.Int64N(int64(m.MaxLatency - m.MinLatency)))
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Run implements Runner.
func (m *MockRunner) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	var stderr strings.Builder
	out, err := m.exec(ctx, args, func(line string) { stderr.WriteString(line + "\n") })
	return out, []byte(stderr.String()), err
}

// Stream implements Runner.
func (m *MockRunner) Stream(ctx context.Context, args ...string) (<-chan string, <-chan error) {
	lines, errc := make(chan string, 64), make(chan error, 1)
	go func() {
		defer close(errc)
		out, err := m.exec(ctx, args, func(line string) {
			select {
			case lines <- line:
			case <-ctx.Done():
			}
		})
		for l := range strings.SplitSeq(strings.TrimRight(string(out), "\n"), "\n") {
			if l != "" {
				lines <- l
			}
		}
		close(lines)
		errc <- err
	}()
	return lines, errc
}

// Command implements Execer: a shell command that prints what would have run.
func (m *MockRunner) Command(ctx context.Context, args ...string) (*exec.Cmd, error) {
	m.record(args)
	msg := "[mock] would run: " + Args(args).String()
	if m.NoPause {
		return exec.CommandContext(ctx, "sh", "-c", `printf '%s\n' "$1"`, "sh", msg), nil
	}
	return exec.CommandContext(ctx, "sh", "-c", `printf '%s\n\nPress enter to return to lazyterminus...' "$1"; read -r _`, "sh", msg), nil
}

func (m *MockRunner) record(args []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, MockCall{Args: slices.Clone(args), Mutating: IsMutating(args), At: m.now()})
}

func mockErr(args []string, msg string) error {
	return &CommandError{Args: args, ExitCode: 1, Message: msg}
}

func (m *MockRunner) exec(ctx context.Context, args []string, emit func(string)) ([]byte, error) {
	m.record(args)
	if err := m.latency(ctx); err != nil {
		return nil, err
	}
	a := Args(args)
	cmd := a.Command()
	if len(args) > 0 && args[0] == "--version" {
		return []byte("Terminus 4.2.0\n"), nil
	}
	m.mu.Lock()
	if m.loggedOut && !a.HasFlag("help") {
		switch cmd {
		case "list", "help", "auth:login", "auth:logout":
		case "auth:whoami":
			m.mu.Unlock()
			emit("[notice] You are not logged in.")
			return nil, nil
		default:
			m.mu.Unlock()
			msg := "You are not logged in. Run `auth:login` to authenticate or `help auth:login` for more info."
			emit("[error] " + msg)
			return nil, mockErr(args, msg)
		}
	}
	if msg, ok := m.failing[cmd]; ok {
		delete(m.failing, cmd)
		m.mu.Unlock()
		emit("[error] " + msg)
		return nil, mockErr(args, msg)
	}
	m.mu.Unlock()
	if IsMutating(args) {
		return m.mutate(ctx, a, emit)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.read(a, emit)
}

func jsonOut(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "    ")
	return append(b, '\n'), err
}

// keyed renders rows as a JSON object keyed by key(row), preserving order,
// or `[]` when empty, as Terminus does.
func keyed[T any](rows []T, key func(T) string) ([]byte, error) {
	if len(rows) == 0 {
		return []byte("[]\n"), nil
	}
	var b strings.Builder
	b.WriteString("{\n")
	for i, r := range rows {
		k, _ := json.Marshal(key(r))
		v, err := json.MarshalIndent(r, "    ", "    ")
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "    %s: %s", k, v)
		if i < len(rows)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return []byte(b.String()), nil
}

func (m *MockRunner) siteEnv(a Args, emit func(string)) (*mockSite, *Env, error) {
	pos := a.Positionals()
	if len(pos) == 0 {
		return nil, nil, mockErr(a, `Not enough arguments (missing: "site_env").`)
	}
	site, env, _ := SplitSiteEnv(pos[0])
	ms := m.w.site(site)
	if ms == nil {
		emit(fmt.Sprintf("[error] Could not locate a site your user may access identified by %s.", site))
		return nil, nil, mockErr(a, fmt.Sprintf("Could not locate a site your user may access identified by %s.", site))
	}
	e := ms.env(env)
	if e == nil {
		return ms, nil, mockErr(a, fmt.Sprintf("Could not find an environment identified by %s.", env))
	}
	return ms, e, nil
}

func (m *MockRunner) siteArg(a Args) (*mockSite, error) {
	pos := a.Positionals()
	if len(pos) == 0 {
		return nil, mockErr(a, `Not enough arguments (missing: "site").`)
	}
	ms := m.w.site(pos[0])
	if ms == nil {
		return nil, mockErr(a, fmt.Sprintf("Could not locate a site your user may access identified by %s.", pos[0]))
	}
	return ms, nil
}

func (ms *mockSite) codeLog(env string) []Commit {
	out := []Commit{}
	for _, mc := range ms.commits {
		if slices.Contains(mc.labels, env) {
			c := mc.Commit
			c.Labels = strings.Join(mc.labels, ", ")
			out = append(out, c)
			if len(out) == 50 {
				break
			}
		}
	}
	return out
}

// refresh advances simulated running workflows to match the clock.
func (m *MockRunner) refresh(ms *mockSite) {
	now := m.now()
	for _, mw := range ms.workflows {
		if mw.duration == 0 || mw.wf.FinishedAt != nil {
			continue
		}
		elapsed := now.Sub(mw.wf.StartedAt.Time())
		frac := float64(elapsed) / float64(mw.duration)
		done := min(int(frac*float64(len(mw.ops))), len(mw.ops))
		for i := range mw.ops {
			if i < done {
				ok := StatusSucceeded
				mw.ops[i].Result = &ok
			}
		}
		mw.wf.Time = fmt.Sprintf("%ds", int(elapsed.Seconds()))
	}
}

func (m *MockRunner) findWorkflow(ms *mockSite, id string) *mockWorkflow {
	for _, mw := range ms.workflows {
		if mw.wf.ID == id || id == "" {
			return mw
		}
	}
	return nil
}

func (m *MockRunner) read(a Args, emit func(string)) ([]byte, error) {
	switch cmd := a.Command(); cmd {
	case "list":
		return m.catalog, nil
	case "help":
		return m.help(a)
	case "auth:whoami":
		return []byte("dev@example.com\n"), nil
	case "site:list":
		var sites []Site
		for _, ms := range m.w.sites {
			sites = append(sites, ms.site)
		}
		return keyed(sites, func(s Site) string { return s.ID })
	case "site:info":
		ms, err := m.siteArg(a)
		if err != nil {
			return nil, err
		}
		return jsonOut(ms.site)
	case "env:list":
		ms, err := m.siteArg(a)
		if err != nil {
			return nil, err
		}
		return keyed(ms.envs, func(e Env) string { return e.ID })
	case "multidev:list":
		ms, err := m.siteArg(a)
		if err != nil {
			return nil, err
		}
		var mds []Multidev
		for _, e := range ms.envs {
			if e.IsMultidev() {
				mds = append(mds, Multidev{ID: e.ID, Created: e.Created, Domain: e.Domain, Locked: e.Locked, Initialized: e.Initialized})
			}
		}
		return keyed(mds, func(md Multidev) string { return md.ID })
	case "env:info":
		_, e, err := m.siteEnv(a, emit)
		if err != nil {
			return nil, err
		}
		info := *e
		info.DrushVersion = 10
		return jsonOut(info)
	case "env:code-log":
		ms, e, err := m.siteEnv(a, emit)
		if err != nil {
			return nil, err
		}
		return jsonOut(ms.codeLog(e.ID))
	case "env:diffstat":
		ms, e, err := m.siteEnv(a, emit)
		if err != nil {
			return nil, err
		}
		if !IsMultidevName(e.ID) && e.ID != "dev" {
			return nil, mockErr(a, "The environment must be a development environment.")
		}
		ds := ms.diffstat[e.ID]
		if len(ds) == 0 {
			emit("[notice] No changes on server.")
		}
		if ds == nil {
			ds = []DiffstatEntry{}
		}
		return jsonOut(ds)
	case "workflow:list":
		ms, err := m.siteArg(a)
		if err != nil {
			return nil, err
		}
		m.refresh(ms)
		var wfs []Workflow
		for i, mw := range ms.workflows {
			if i == 100 {
				break
			}
			wfs = append(wfs, mw.wf)
		}
		return keyed(wfs, func(w Workflow) string { return w.ID })
	case "workflow:info:status", "workflow:info:operations", "workflow:info:logs":
		ms, err := m.siteArg(a)
		if err != nil {
			return nil, err
		}
		m.refresh(ms)
		id, _ := a.Flag("id")
		mw := m.findWorkflow(ms, id)
		if mw == nil {
			return nil, mockErr(a, fmt.Sprintf("Workflow %s not found.", id))
		}
		switch cmd {
		case "workflow:info:status":
			return jsonOut(mw.wf)
		case "workflow:info:logs":
			emit("[notice] Workflow operations did not contain any logs.")
			return nil, nil
		}
		return keyed(mw.ops, func(op WorkflowOp) string { return op.ID })
	case "backup:list":
		ms, e, err := m.siteEnv(a, emit)
		if err != nil {
			return nil, err
		}
		bs := ms.backups[e.ID]
		return keyed(bs, func(b Backup) string { return fmt.Sprintf("%d_%s", int64(b.Date), b.File) })
	case "upstream:updates:status":
		ms, _, err := m.siteEnv(a, emit)
		if err != nil {
			return nil, err
		}
		if len(ms.upstream) > 0 {
			return []byte("outdated\n"), nil
		}
		return []byte("current\n"), nil
	case "upstream:updates:list":
		ms, _, err := m.siteEnv(a, emit)
		if err != nil {
			return nil, err
		}
		if len(ms.upstream) == 0 {
			emit("[warning] There are no available updates for this site.")
		}
		return keyed(ms.upstream, func(u UpstreamUpdate) string { return u.Hash })
	case "connection:info":
		_, e, err := m.siteEnv(a, emit)
		if err != nil {
			return nil, err
		}
		return jsonOut(map[string]string{
			"sftp_command": "sftp -o Port=2222 " + e.ID + ".00000000@appserver." + e.ID + ".example.drush.in",
			"git_command":  "git clone ssh://codeserver.dev.00000000@codeserver.dev.example.drush.in:2222/~/repository.git",
		})
	default:
		if a.HasFlag("help") {
			return []byte("Help for " + cmd + " (mock)\n"), nil
		}
		// Other read-only commands: a small, valid, empty result.
		if f, _ := a.Flag("format"); f == "json" {
			return []byte("[]\n"), nil
		}
		return []byte(""), nil
	}
}

func (m *MockRunner) help(a Args) ([]byte, error) {
	pos := a.Positionals()
	if len(pos) == 0 {
		return []byte("Usage: help [<command_name>]\n"), nil
	}
	var doc struct {
		Commands []json.RawMessage `json:"commands"`
	}
	if err := json.Unmarshal(m.catalog, &doc); err != nil {
		return nil, err
	}
	for _, raw := range doc.Commands {
		var c struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(raw, &c)
		if c.Name == pos[0] {
			return append(raw, '\n'), nil
		}
	}
	return nil, mockErr(a, fmt.Sprintf("Command %q is not defined.", pos[0]))
}

// mutate simulates a state-changing command: it registers a running
// workflow, blocks for WorkflowDuration (like Terminus does while it waits on
// the workflow), then applies the effect.
func (m *MockRunner) mutate(ctx context.Context, a Args, emit func(string)) ([]byte, error) {
	cmd := a.Command()
	m.mu.Lock()
	if cmd == "remote:drush" || cmd == "remote:wp" {
		defer m.mu.Unlock()
		return []byte(fmt.Sprintf("[mock] %s output for: %s\n", cmd, strings.Join(a.Positionals()[1:], " "))), nil
	}
	ms, env, desc, effect, err := m.plan(a, emit)
	if err != nil {
		m.mu.Unlock()
		emit("[error] " + errMessage(err))
		return nil, err
	}
	if desc == "" { // no workflow: immediate
		effect()
		m.mu.Unlock()
		return nil, nil
	}
	mw := m.startWorkflow(ms, env, desc)
	dur := mw.duration
	m.mu.Unlock()

	emit(fmt.Sprintf("[notice] %s", desc))
	start := m.now()
	ticker := time.NewTicker(max(dur/4, time.Millisecond))
	defer ticker.Stop()
	for m.now().Sub(start) < dur {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			if left := dur - m.now().Sub(start); left > 0 {
				emit(fmt.Sprintf("[notice] Workflow running... (%s remaining)", left.Round(time.Second)))
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refresh(ms)
	fin := EpochOf(m.now())
	mw.wf.FinishedAt = &fin
	for i := range mw.ops {
		ok := StatusSucceeded
		mw.ops[i].Result = &ok
	}
	mw.wf.Status = StatusSucceeded
	effect()
	emit(fmt.Sprintf("[notice] Workflow succeeded: %s", desc))
	return nil, nil
}

func errMessage(err error) string {
	var ce *CommandError
	if errors.As(err, &ce) {
		return ce.Message
	}
	return err.Error()
}

func (m *MockRunner) startWorkflow(ms *mockSite, env, desc string) *mockWorkflow {
	now := m.now()
	e := env
	mw := &mockWorkflow{
		wf: Workflow{
			ID: fmt.Sprintf("mock-%d", now.UnixNano()), Env: &e, Workflow: desc,
			User: "dev@example.com", Status: StatusRunning, StartedAt: EpochOf(now), Time: "0s",
		},
		duration: m.WorkflowDuration,
	}
	if env == "" {
		mw.wf.Env = nil
	}
	for i, d := range []string{"Set target Git commit", "Deploy code", "Bring application container to desired configuration state",
		"Run Quicksilver hooks", "Clear CMS cache", "Take Screenshot"} {
		mw.ops = append(mw.ops, WorkflowOp{ID: fmt.Sprintf("%s-op%d", mw.wf.ID, i), Type: "platform", Description: d})
	}
	ms.workflows = append([]*mockWorkflow{mw}, ms.workflows...)
	return mw
}

// plan validates a mutating command against mock state the way Terminus
// would and returns the workflow description plus the state change.
func (m *MockRunner) plan(a Args, emit func(string)) (ms *mockSite, env, desc string, effect func(), err error) {
	noop := func() {}
	cmd := a.Command()
	pos := a.Positionals()
	switch cmd {
	case "env:deploy", "env:clear-cache", "backup:create", "env:clone-content", "connection:set",
		"env:wake", "env:commit", "multidev:create", "multidev:delete", "env:wipe", "lock:enable", "lock:disable":
		ms, e, err := m.siteEnv(a, emit)
		if err != nil {
			return nil, "", "", nil, err
		}
		se := SiteEnv(ms.site.Name, e.ID)
		switch cmd {
		case "env:deploy":
			src := SourceEnv(e.ID)
			if src == "" {
				return nil, "", "", nil, mockErr(a, "The environment must be test or live.")
			}
			if len(DeployableCommits(ms.codeLog(src), src, e.ID)) == 0 {
				emit(fmt.Sprintf("[warning] There is nothing to deploy to %s.", e.ID))
				return ms, e.ID, "", noop, nil
			}
			return ms, e.ID, fmt.Sprintf("Deploy code to %q", e.ID), func() {
				for i := range ms.commits {
					if slices.Contains(ms.commits[i].labels, src) && !slices.Contains(ms.commits[i].labels, e.ID) {
						ms.commits[i].labels = append(ms.commits[i].labels, e.ID)
					}
				}
			}, nil
		case "env:clear-cache":
			return ms, e.ID, fmt.Sprintf("Clear cache for %q", e.ID), noop, nil
		case "backup:create":
			return ms, e.ID, fmt.Sprintf("Backup the %q environment", e.ID), func() {
				now := m.now()
				el, _ := a.Flag("element")
				if el == "" {
					el = "all"
				}
				ms.backups[e.ID] = append([]Backup{{File: fmt.Sprintf("%s_%s_%s_%s.tar.gz", ms.site.Name, e.ID, now.UTC().Format("2006-01-02T15-04-05_UTC"), el),
					Size: "1.0MB", Date: EpochOf(now), Expiry: EpochOf(now.Add(365 * 24 * time.Hour)), Initiator: "manual"}}, ms.backups[e.ID]...)
			}, nil
		case "env:clone-content":
			if len(pos) < 2 {
				return nil, "", "", nil, mockErr(a, `Not enough arguments (missing: "target_env").`)
			}
			if ms.env(pos[1]) == nil {
				return nil, "", "", nil, mockErr(a, fmt.Sprintf("Could not find an environment identified by %s.", pos[1]))
			}
			what := "database and files"
			if a.HasFlag("db-only") {
				what = "database"
			} else if a.HasFlag("files-only") {
				what = "files"
			}
			return ms, pos[1], fmt.Sprintf("Clone %s from %q to %q", what, e.ID, pos[1]), noop, nil
		case "connection:set":
			if len(pos) < 2 || (pos[1] != "git" && pos[1] != "sftp") {
				return nil, "", "", nil, mockErr(a, "You must specify the mode as either sftp or git.")
			}
			if !IsMultidevName(e.ID) && e.ID != "dev" {
				return nil, "", "", nil, mockErr(a, "Connection mode cannot be set on the test or live environments.")
			}
			if e.ConnectionMode == pos[1] {
				emit(fmt.Sprintf("[notice] The connection mode is already set to %s.", pos[1]))
				return ms, e.ID, "", noop, nil
			}
			return ms, e.ID, fmt.Sprintf("Enable on-server development (%s) for %q", pos[1], e.ID), func() {
				e.ConnectionMode = pos[1]
				if pos[1] == "git" {
					delete(ms.diffstat, e.ID) // uncommitted changes are discarded
				}
			}, nil
		case "env:wake":
			wasAsleep := m.w.asleep[se]
			return ms, e.ID, "", func() {
				delete(m.w.asleep, se)
				if wasAsleep {
					emit(fmt.Sprintf("[notice] OK >> %s responded after waking", e.Domain))
				} else {
					emit(fmt.Sprintf("[notice] OK >> %s responded", e.Domain))
				}
			}, nil
		case "env:commit":
			if e.ConnectionMode != "sftp" {
				return nil, "", "", nil, mockErr(a, "The environment must be in SFTP mode to commit.")
			}
			if len(ms.diffstat[e.ID]) == 0 && !a.HasFlag("force") {
				emit("[warning] There is no code to commit.")
				return ms, e.ID, "", noop, nil
			}
			msg, _ := a.Flag("message")
			if msg == "" {
				msg = "Terminus commit."
			}
			return ms, e.ID, fmt.Sprintf("Commit changes to %q", e.ID), func() {
				now := m.now()
				c := mockCommit{Commit: Commit{Datetime: now.UTC().Format("2006-01-02T15:04:05"), Author: "dev@example.com",
					Hash: fmt.Sprintf("%040x", now.UnixNano()), Message: msg}, labels: []string{e.ID}}
				ms.commits = append([]mockCommit{c}, ms.commits...)
				delete(ms.diffstat, e.ID)
			}, nil
		case "multidev:create":
			if len(pos) < 2 {
				return nil, "", "", nil, mockErr(a, `Not enough arguments (missing: "multidev").`)
			}
			name := pos[1]
			if ms.env(name) != nil {
				return nil, "", "", nil, mockErr(a, fmt.Sprintf("The environment %q already exists.", name))
			}
			return ms, "", "Create a Multidev environment", func() {
				ms.envs = append(ms.envs, Env{ID: name, Created: EpochOf(m.now()), Domain: name + "-" + ms.site.Name + ".pantheonsite.io",
					ConnectionMode: "git", Initialized: true, PHPVersion: e.PHPVersion, PHPRuntimeGeneration: e.PHPRuntimeGeneration})
				SortEnvs(ms.envs)
				for i := range ms.commits {
					if slices.Contains(ms.commits[i].labels, e.ID) {
						ms.commits[i].labels = append(ms.commits[i].labels, name)
					}
				}
			}, nil
		case "multidev:delete":
			if !e.IsMultidev() {
				return nil, "", "", nil, mockErr(a, "You cannot delete the dev, test, or live environments.")
			}
			return ms, "", fmt.Sprintf("Delete a Multidev environment (%s)", e.ID), func() {
				ms.envs = slices.DeleteFunc(ms.envs, func(x Env) bool { return x.ID == e.ID })
			}, nil
		case "env:wipe":
			return ms, e.ID, fmt.Sprintf("Wipe the %q environment", e.ID), noop, nil
		default:
			return ms, e.ID, fmt.Sprintf("%s on %q", cmd, e.ID), noop, nil
		}
	}
	// Unknown mutating command (palette, plugins): succeed with a generic
	// workflow on the site if the first positional names one.
	if len(pos) > 0 {
		site, env, _ := SplitSiteEnv(pos[0])
		if ms := m.w.site(site); ms != nil {
			if ms.env(env) == nil {
				env = ""
			}
			return ms, env, fmt.Sprintf("Run %s", cmd), noop, nil
		}
	}
	return nil, "", "", func() { emit("[notice] [mock] " + Args(a).String()) }, nil
}
