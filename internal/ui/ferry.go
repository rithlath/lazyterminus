package ui

import (
	"errors"
	"fmt"
	"os/exec"

	tea "charm.land/bubbletea/v2"

	"github.com/rithlath/lazyterminus/internal/config"
	"github.com/rithlath/lazyterminus/internal/terminus"
)

// ferryDoneMsg is sent when ferry, opened with f, exits.
type ferryDoneMsg struct {
	site, env string
	err       error
	note      string // shown on return, e.g. no repo mapped
}

// errSiteLoading means the site's ID isn't known yet: ferry can't be
// pointed at it.
var errSiteLoading = errors.New("site details are still loading")

// pantheonSFTPTarget is the SFTP address of a Pantheon environment, in
// Pantheon's documented form: user <env>.<site-id>, host
// appserver.<env>.<site-id>.drush.in, port 2222.
func pantheonSFTPTarget(siteID, env string) string {
	return env + "." + siteID + "@appserver." + env + "." + siteID + ".drush.in:2222"
}

// ferryCommand is ferry's arguments for env of site: the site's repo on the
// left, the environment's files/ on the right. Under --mock it opens ferry's
// own mock server instead, never a real one. note is set when no repo is
// mapped (ferry then opens where lazyterminus was started).
func (m *Model) ferryCommand(site string, e *terminus.Env) (args []string, note string, err error) {
	var local []string
	if repo := m.cfg.RepoFor(site); repo != "" {
		local = []string{"--local-dir", repo}
	} else {
		note = fmt.Sprintf("No local repo mapped for %s. Add it under sites: in %s", site, m.configPath())
	}
	if m.opts.Mock {
		return append([]string{"--mock"}, local...), note, nil
	}
	id := ""
	for _, s := range m.sites {
		if s.Name == site {
			id = s.ID
		}
	}
	if id == "" {
		return nil, "", errSiteLoading
	}
	// --tag shows the environment as a coloured tag in ferry's Remote pane.
	args = append(local, "--remote-dir", "files", "--label", site, "--tag", e.ID, pantheonSFTPTarget(id, e.ID))
	return args, note, nil
}

// configPath is where lazyterminus's config lives, for messages.
func (m *Model) configPath() string {
	if m.cfg.Path != "" {
		return m.cfg.Path
	}
	return config.DefaultPath()
}

// openFerry hands the terminal to ferry on env of site. It never asks
// first, even on live: opening is harmless. In read-only or dry-run mode it
// refuses, since ferry can change files.
func (m *Model) openFerry(site string, e *terminus.Env) tea.Cmd {
	if m.cfg.ReadOnly || m.opts.DryRun {
		m.setStatus("read-only: ferry can change files", true)
		return nil
	}
	path, err := exec.LookPath(m.cfg.FerryPath)
	if err != nil {
		m.setStatus(fmt.Sprintf("%s not found: install it (in the ferry repo: make install), or set ferry_path in %s",
			m.cfg.FerryPath, m.configPath()), true)
		return nil
	}
	args, note, err := m.ferryCommand(site, e)
	if err != nil {
		m.setStatus("Site details are still loading; try again in a moment.", true)
		return nil
	}
	env := e.ID
	return tea.ExecProcess(exec.Command(path, args...), func(err error) tea.Msg {
		return ferryDoneMsg{site: site, env: env, err: err, note: note}
	})
}

// onFerryDone reports how ferry ended and refreshes the site: uploads in
// SFTP mode change its pending changes.
func (m *Model) onFerryDone(msg ferryDoneMsg) tea.Cmd {
	if msg.err != nil {
		m.reportErr("ferry", msg.err)
	} else {
		s := "Back from ferry; refreshing " + msg.site + "." + msg.env + "…"
		if msg.note != "" {
			s += " " + msg.note
		}
		m.setStatus(s, false)
	}
	m.client.InvalidateSite(msg.site)
	m.state(msg.site).inflight = map[string]bool{}
	return tea.Batch(m.ensureSite(msg.site, true), m.startSpinner())
}
