package ui

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rithlath/lazyterminus/internal/terminus"
)

// Messages returned by Terminus commands. Every Terminus call runs in a
// tea.Cmd and reports back with one of these; the UI never blocks on it.
type (
	sitesLoadedMsg struct {
		sites []terminus.Site
		err   error
	}
	envsLoadedMsg struct {
		site string
		envs []terminus.Env
		err  error
	}
	workflowsMsg struct {
		site string
		wfs  []terminus.Workflow
		err  error
	}
	codeLogMsg struct {
		site, env string
		commits   []terminus.Commit
		err       error
	}
	diffstatMsg struct {
		site, env string
		entries   []terminus.DiffstatEntry
		err       error
	}
	opsMsg struct {
		site, id string
		ops      []terminus.WorkflowOp
		err      error
	}
	drupalMsg struct {
		site, version, note string
	}
	pollMsg struct {
		site string
		seq  int
	}
	authMsg struct {
		account string
		err     error
	}
	localLogMsg struct {
		entries []terminus.Logged
	}
	clockMsg time.Time
	errMsg   struct {
		context string
		err     error
	}
)

// siteState is everything loaded for one site.
type siteState struct {
	envs       []terminus.Env
	envsErr    error
	envsLoaded bool

	wfs      []terminus.Workflow
	wfErr    error
	wfLoaded bool
	wfSeen   bool // a list has loaded successfully at least once

	codeLogs   map[string][]terminus.Commit
	codeLogErr map[string]error
	diffstat   map[string][]terminus.DiffstatEntry
	diffErr    map[string]error
	ops        map[string][]terminus.WorkflowOp

	drupal, drupalNote string
	drupalLoaded       bool

	branches map[string]string            // multidev → local git branch status
	domains  map[string][]terminus.Domain // env → domain:list (nil on error)

	inflight map[string]bool
}

func newSiteState() *siteState {
	return &siteState{
		codeLogs: map[string][]terminus.Commit{}, codeLogErr: map[string]error{},
		diffstat: map[string][]terminus.DiffstatEntry{}, diffErr: map[string]error{},
		ops: map[string][]terminus.WorkflowOp{}, inflight: map[string]bool{},
		branches: map[string]string{}, domains: map[string][]terminus.Domain{},
	}
}

// begin marks key in flight and reports whether a load should start.
func (s *siteState) begin(key string) bool {
	if s.inflight[key] {
		return false
	}
	s.inflight[key] = true
	return true
}

func (s *siteState) env(id string) *terminus.Env {
	for i := range s.envs {
		if s.envs[i].ID == id {
			return &s.envs[i]
		}
	}
	return nil
}

const loadTimeout = 2 * time.Minute

func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), loadTimeout)
}

func checkAuth(c *terminus.Client) tea.Cmd {
	return func() tea.Msg {
		cx, cancel := ctx()
		defer cancel()
		who, err := c.WhoAmI(cx)
		return authMsg{account: who, err: err}
	}
}

func loadSites(c *terminus.Client, force bool) tea.Cmd {
	return func() tea.Msg {
		cx, cancel := ctx()
		defer cancel()
		sites, err := c.Sites(cx, force)
		return sitesLoadedMsg{sites: sites, err: err}
	}
}

func loadEnvs(c *terminus.Client, site string, force bool) tea.Cmd {
	return func() tea.Msg {
		cx, cancel := ctx()
		defer cancel()
		envs, err := c.Envs(cx, site, force)
		return envsLoadedMsg{site: site, envs: envs, err: err}
	}
}

func loadWorkflows(c *terminus.Client, site string, busy, force bool) tea.Cmd {
	return func() tea.Msg {
		cx, cancel := ctx()
		defer cancel()
		wfs, err := c.Workflows(cx, site, busy, force)
		return workflowsMsg{site: site, wfs: wfs, err: err}
	}
}

func loadCodeLog(c *terminus.Client, site, env, repo string, force bool) tea.Cmd {
	return func() tea.Msg {
		cx, cancel := ctx()
		defer cancel()
		log, err := c.CodeLog(cx, site, env, force)
		return codeLogMsg{site: site, env: env, commits: withFullMessages(repo, log), err: err}
	}
}

func loadDiffstat(c *terminus.Client, site, env string, force bool) tea.Cmd {
	return func() tea.Msg {
		cx, cancel := ctx()
		defer cancel()
		ds, err := c.Diffstat(cx, site, env, force)
		return diffstatMsg{site: site, env: env, entries: ds, err: err}
	}
}

func loadOps(c *terminus.Client, site, id string, finished bool) tea.Cmd {
	return func() tea.Msg {
		cx, cancel := ctx()
		defer cancel()
		ops, err := c.WorkflowOps(cx, site, id, finished)
		return opsMsg{site: site, id: id, ops: ops, err: err}
	}
}

// loadLocalLog reads the history log (commands this machine ran through
// lazyterminus). A missing log is an empty history.
func loadLocalLog(path string) tea.Cmd {
	if path == "" {
		return nil
	}
	return func() tea.Msg {
		b, _ := os.ReadFile(path)
		return localLogMsg{entries: terminus.ParseHistoryLog(b)}
	}
}

// localCommands returns the newest n logged commands naming site.env,
// newest first.
func (m *Model) localCommands(site, env string, n int) []terminus.Logged {
	se := terminus.SiteEnv(site, env)
	var out []terminus.Logged
	for i := len(m.localLog) - 1; i >= 0 && len(out) < n; i-- {
		if m.localLog[i].Involves(se) {
			out = append(out, m.localLog[i])
		}
	}
	return out
}

func loadDrupal(site, repo string) tea.Cmd {
	return func() tea.Msg {
		v, note := DrupalVersion(repo)
		return drupalMsg{site: site, version: v, note: note}
	}
}

func pollAfter(d time.Duration, site string, seq int) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return pollMsg{site: site, seq: seq} })
}

func clockTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return clockMsg(t) })
}

// DrupalVersion reads drupal/core (or drupal/core-recommended) from
// repo/composer.lock. Terminus doesn't expose the Drupal version, so this is
// the local checkout's version, labeled as such.
func DrupalVersion(repo string) (version, note string) {
	if repo == "" {
		return "", "(no local repo mapped)"
	}
	b, err := os.ReadFile(filepath.Join(repo, "composer.lock"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", "(no composer.lock in repo)"
	}
	if err != nil {
		return "", "(composer.lock unreadable)"
	}
	var lock struct {
		Packages []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(b, &lock); err != nil {
		return "", "(composer.lock unreadable)"
	}
	var fallback string
	for _, p := range lock.Packages {
		switch p.Name {
		case "drupal/core":
			return p.Version, "(local repo)"
		case "drupal/core-recommended":
			fallback = p.Version
		}
	}
	if fallback != "" {
		return fallback, "(local repo)"
	}
	return "", "(drupal/core not in composer.lock)"
}
