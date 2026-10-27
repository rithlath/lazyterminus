package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/rithlath/lazyterminus/internal/jobs"
	"github.com/rithlath/lazyterminus/internal/terminus"
	"github.com/rithlath/lazyterminus/internal/ui/panels"
	"github.com/rithlath/lazyterminus/internal/ui/theme"
)

// WideWidth is the minimum width for the two-column layout.
const WideWidth = 100

type rect struct{ w, h int }

// layout computes panel sizes. Wide: Sites over Environments on the left,
// Detail over Workflows on the right. Narrow: all four stacked, with the
// focused panel given the most room.
func (m *Model) layout() (wide bool, sizes [numPanels]rect) {
	bodyH := max(m.height-2, 4*3) // status + hints
	if m.width >= WideWidth {
		leftW := min(max(m.width*2/5, 34), 60)
		rightW := m.width - leftW
		sitesH := min(max(len(m.sites)+2, 4), bodyH/3)
		if len(m.sites) == 0 {
			sitesH = min(5, bodyH/3) // room for the not-logged-in hint
		}
		detailH := bodyH * 11 / 20
		sizes[panelSites] = rect{leftW, sitesH}
		sizes[panelEnvs] = rect{leftW, bodyH - sitesH}
		sizes[panelDetail] = rect{rightW, detailH}
		sizes[panelWorkflows] = rect{rightW, bodyH - detailH}
		return true, sizes
	}
	// Narrow: minimum 3 rows each (border + one line); sites is capped at its
	// content; the focused panel takes the remaining space.
	const minH = 3
	want := [numPanels]int{min(len(m.sites)+2, 6), 8, 8, 6}
	want[m.focus] = 1 << 20
	h := [numPanels]int{minH, minH, minH, minH}
	left := bodyH - 4*minH
	for left > 0 {
		grew := false
		for _, p := range []int{m.focus, panelEnvs, panelDetail, panelWorkflows, panelSites} {
			if left > 0 && h[p] < want[p] {
				h[p]++
				left--
				grew = true
			}
		}
		if !grew {
			h[m.focus] += left
			break
		}
	}
	for p := range numPanels {
		sizes[p] = rect{m.width, h[p]}
	}
	return false, sizes
}

// View implements tea.Model.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "lazyterminus"
	if s := m.site(); s != "" {
		v.WindowTitle = "lazyterminus · " + s
	}
	return v
}

func (m Model) render() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	status := m.statusBar()
	hints := m.hintLine()
	bodyH := max(m.height-2, 1)
	var body string
	switch {
	case m.showHelp:
		body = panels.Box(" Help ", m.helpLines(), m.width, bodyH, true, " esc close ")
	case m.showOutput:
		m.outputView.SetWidth(max(m.width-4, 1))
		m.outputView.SetHeight(max(bodyH-2, 1))
		lines := strings.Split(m.outputView.View(), "\n")
		if len(m.output) == 0 {
			lines = []string{theme.MutedText.Render("No output yet.")}
		}
		body = panels.Box(" Output ", lines, m.width, bodyH, true, fmt.Sprintf(" %d%% · esc close ", int(m.outputView.ScrollPercent()*100)))
	case m.showHistory:
		body = m.renderHistory(m.width, bodyH)
	case m.showPipeline:
		mm := m
		body = mm.renderPipeline(m.width, bodyH)
	default:
		body = m.renderPanels()
	}
	if m.modal != nil {
		body = m.overlay(body, bodyH)
	}
	out := lipgloss.JoinVertical(lipgloss.Left, body, status, hints)
	// Never exceed the terminal: clip any overflow rows.
	if lines := strings.Split(out, "\n"); len(lines) > m.height {
		out = strings.Join(lines[:m.height], "\n")
	}
	return out
}

func (m Model) renderPanels() string {
	mm := &m
	wide, sz := mm.layout()
	boxes := [numPanels]string{}
	for p := range numPanels {
		boxes[p] = mm.renderPanel(p, sz[p])
	}
	if wide {
		left := lipgloss.JoinVertical(lipgloss.Left, boxes[panelSites], boxes[panelEnvs])
		right := lipgloss.JoinVertical(lipgloss.Left, boxes[panelDetail], boxes[panelWorkflows])
		return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	}
	return lipgloss.JoinVertical(lipgloss.Left, boxes[:]...)
}

func (m *Model) renderPanel(p int, r rect) string {
	focused := m.focus == p
	innerW, innerH := max(r.w-4, 1), max(r.h-2, 0)
	title := fmt.Sprintf(" [%d] %s ", p+1, panelNames[p])
	footer := ""
	var lines []string
	site := m.site()
	st := m.state(site)

	switch p {
	case panelSites:
		rows := make([]panels.SiteRow, len(m.sites))
		for i, s := range m.sites {
			row := panels.SiteRow{Name: s.Name, Framework: s.Framework, EnvCount: -1}
			if ss, ok := m.data[s.Name]; ok && ss.envsLoaded {
				row.EnvCount, row.Err = len(ss.envs), ss.envsErr != nil
			}
			rows[i] = row
		}
		msg := m.spinner.View() + " loading sites…"
		switch {
		case m.loggedOut || terminus.IsNotLoggedIn(m.sitesErr):
			msg = theme.SymFailed + " Not logged in to Terminus. In another pane run\n  terminus auth:login --machine-token=<token>\nthen press r here."
		case m.sitesErr != nil:
			msg = theme.SymFailed + " " + m.sitesErr.Error()
		}
		lines = panels.Sites(rows, &m.siteList, innerW, innerH, focused, msg)
		if len(m.sites) > 0 {
			footer = fmt.Sprintf(" %d of %d ", m.siteList.Cursor+1, len(m.sites))
		}
	case panelEnvs:
		if site != "" {
			title = fmt.Sprintf(" [2] Environments · %s ", site)
		}
		rows := m.envRows()
		m.envList.Clamp(selectable(rows))
		msg := m.spinner.View() + " loading environments…"
		if site == "" {
			msg = "No site selected."
		}
		if st.envsErr != nil {
			msg = theme.SymFailed + " " + st.envsErr.Error()
		}
		lines = panels.Envs(rows, &m.envList, innerW, innerH, focused, msg)
		if n := len(st.envs); n > 0 {
			footer = fmt.Sprintf(" %d envs ", n)
		}
	case panelDetail:
		d := m.detailData()
		if d.Env != nil {
			title = fmt.Sprintf(" [3] %s.%s ", d.Site, d.EnvID)
		}
		all := panels.Detail(d, innerW, 1<<20)
		m.detailOffset = min(m.detailOffset, max(len(all)-innerH, 0))
		lines = all[m.detailOffset:]
		if len(all) > innerH {
			footer = fmt.Sprintf(" %d/%d ", m.detailOffset+1, max(len(all)-innerH+1, 1))
		}
	case panelWorkflows:
		d := panels.WorkflowsData{
			Workflows: st.wfs, Progress: map[string][2]int{}, Spinner: m.spinner.View(),
			Bar: m.bar, Now: m.now(), Jobs: m.jobRows(),
		}
		for id, ops := range st.ops {
			done, total := terminus.Progress(ops)
			d.Progress[id] = [2]int{done, total}
		}
		switch {
		case st.wfErr != nil:
			d.Status = theme.SymFailed + " " + st.wfErr.Error()
		case !st.wfLoaded:
			d.Status = m.spinner.View() + " loading workflows…"
		default:
			d.Status = "No recent workflows."
		}
		lines = panels.Workflows(d, &m.wfList, innerW, innerH, focused)
		running := 0
		for _, w := range st.wfs {
			if w.Running() {
				running++
			}
		}
		if running > 0 {
			footer = fmt.Sprintf(" %d running ", running)
		}
	}
	return panels.Box(title, lines, r.w, r.h, focused, footer)
}

// detailData assembles the Detail panel for the selected environment.
func (m *Model) detailData() panels.DetailData {
	site, envID := m.site(), m.selectedEnv()
	st := m.state(site)
	d := panels.DetailData{Site: site, EnvID: envID, Now: m.now(), UpstreamPending: -1}
	if st.envsErr != nil {
		d.Err = st.envsErr.Error()
	}
	e := st.env(envID)
	if e == nil {
		return d
	}
	d.Env = e
	d.Protected = m.protected(envID)
	if st.drupalLoaded {
		d.Drupal, d.DrupalNote = st.drupal, st.drupalNote
	} else {
		d.DrupalNote = "(reading local repo…)"
	}
	commits, target, label, known := m.ahead(st, envID)
	d.Ahead, d.AheadTarget, d.AheadLabel, d.AheadKnown = commits, target, label, known
	if ds, ok := st.diffstat[envID]; ok {
		d.Diffstat, d.DiffstatKnown = ds, true
	}
	if wf, ok := terminus.LastDeploy(st.wfs, envID); ok {
		d.LastDeploy = &wf
	}
	if wf, ok := terminus.LastFailed(st.wfs, envID); ok {
		d.Failed = &wf
		d.FailReason = terminus.FailureReason(st.ops[wf.ID])
		if _, loaded := st.ops[wf.ID]; loaded && d.FailReason == "" {
			d.FailReason = "no failure details reported"
		}
	}
	if e.IsMultidev() {
		d.Branch = e.ID
		d.LocalBranch = m.localBranchStatus(site, e.ID)
	}
	return d
}

// jobRows lists local background jobs for the Workflows panel.
func (m *Model) jobRows() []panels.JobRow {
	var rows []panels.JobRow
	for _, j := range m.jobs.Recent(3) {
		if j.Site != m.site() {
			continue
		}
		r := panels.JobRow{Title: j.Title, Running: j.State == jobs.Running, Failed: j.State == jobs.Failed,
			Started: j.Started, Ended: j.Ended}
		if r.Running && len(j.Steps) > 1 {
			r.Title += fmt.Sprintf(" (step %d/%d: %s)", j.Step+1, len(j.Steps), terminus.Args(j.Steps[j.Step]).Command())
		}
		if j.Err != nil {
			r.Err = j.Err.Error()
		}
		rows = append(rows, r)
	}
	return rows
}

func (m Model) statusBar() string {
	left := theme.AppBadge.Render("lazyterminus")
	switch {
	case m.opts.Mock:
		left += " " + theme.ModeBadge.Render("MOCK")
	}
	if m.opts.DryRun {
		left += " " + theme.ModeBadge.Render("DRY-RUN")
	}
	if m.cfg.ReadOnly {
		left += " " + theme.ModeBadge.Render("READ-ONLY")
	}
	switch {
	case m.loggedOut:
		left += " " + theme.Fail.Render(theme.SymFailed+" not logged in")
	case m.account != "":
		left += " " + theme.MutedText.Render(m.account)
	}
	st := m.status
	if m.spinning && m.busy() {
		st = m.spinner.View() + " " + st
	}
	style := theme.MutedText
	if m.statusErr {
		style = theme.Fail
	}
	right := ""
	if s := m.site(); s != "" {
		right = s
		if e := m.selectedEnv(); e != "" {
			right += "." + e
		}
	}
	avail := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	mid := style.Render(panels.Fit(" "+st, max(avail, 0)))
	return panels.Fit(left+mid+" "+theme.Bold.Render(right), m.width)
}

func (m Model) hintLine() string {
	type hint struct{ action, label string }
	var hs []hint
	switch {
	case m.showHelp:
		hs = []hint{{ActHelp, "close help"}, {ActQuit, "quit"}}
	case m.showOutput:
		hs = []hint{{ActOutput, "close output"}, {ActUp, "scroll"}}
	case m.showHistory:
		hs = []hint{{ActHistory, "close history"}, {ActUp, "scroll"}}
	case m.focus == panelEnvs || m.focus == panelDetail || m.showPipeline:
		hs = []hint{{ActSelect, "actions"}, {ActDeploy, "deploy"}, {ActClearCache, "cache"}, {ActBackup, "backup"}, {ActCloneContent, "clone"},
			{ActConnectionMode, "mode"}, {ActWake, "wake"}, {ActDrush, "drush"}, {ActCommit, "commit"},
			{ActCreateMultidev, "new md"}, {ActDeleteMultidev, "del md"}, {ActUpstream, "upstream"}}
	default:
		hs = []hint{{ActNextPanel, "panels"}, {ActUp, "up"}, {ActDown, "down"}, {ActSelect, "select"}, {ActRefresh, "refresh"}}
	}
	if !m.showHistory {
		hs = append(hs, hint{ActPipeline, "pipeline"}, hint{ActHistory, "history"})
	}
	hs = append(hs, hint{ActPalette, "palette"}, hint{ActLazygit, "lazygit"}, hint{ActHelp, "help"}, hint{ActQuit, "quit"})
	var parts []string
	for _, h := range hs {
		if k := m.keys.Key(h.action); k != "" && k != "—" {
			parts = append(parts, theme.Key.Render(k)+" "+theme.MutedText.Render(h.label))
		}
	}
	return panels.Fit(strings.Join(parts, "  "), m.width)
}

func (m Model) helpLines() []string {
	var out []string
	groups := m.keys.Groups()
	for _, g := range []string{"Global", "Environment"} {
		out = append(out, theme.Bold.Render(g))
		for _, a := range m.keys.Actions() {
			if a.Group != g {
				continue
			}
			b := m.keys.Binding(a.Action)
			out = append(out, fmt.Sprintf("  %s %s %s", theme.Key.Render(panels.Fit(strings.Join(b.Keys(), " "), 12)),
				panels.Fit(b.Help().Desc, 22), theme.MutedText.Render(a.Action)))
		}
		out = append(out, "")
		_ = groups
	}
	out = append(out,
		theme.Bold.Render("Symbols"),
		"  "+theme.OK.Render(theme.SymOK)+" healthy   "+theme.Warn.Render(theme.SymOK)+" needs attention   "+
			theme.Warn.Render(theme.SymRunning)+" running   "+theme.Fail.Render(theme.SymFailed)+" failed   "+theme.MutedText.Render(theme.SymUnknown)+" unknown",
		"  "+theme.Warn.Render(theme.SymAhead+"3")+" commits ready to deploy here (multidevs: ahead of dev)   "+theme.SFTP.Render("sftp")+" on-server development",
		"",
		theme.MutedText.Render("Rebind keys in "+m.cfg.Path+" under keymap: (action name → keys)."),
	)
	return out
}

// overlay draws the open modal centered over body.
func (m Model) overlay(body string, bodyH int) string {
	w := min(max(m.width*3/4, 50), 110, m.width)
	title, lines, footer := m.modal.view(w - 4)
	var wrapped []string
	for _, l := range lines {
		if lipgloss.Width(l) <= w-4 {
			wrapped = append(wrapped, l)
			continue
		}
		wrapped = append(wrapped, strings.Split(lipgloss.Wrap(l, w-4, " /"), "\n")...)
	}
	h := min(len(wrapped)+2, bodyH)
	if len(wrapped) > h-2 && h > 3 { // keep the tail (confirmation input) visible
		wrapped = append([]string{theme.MutedText.Render("…")}, wrapped[len(wrapped)-(h-3):]...)
	}
	box := panels.Box(" "+title+" ", wrapped, w, h, true, footer)
	x, y := (m.width-w)/2, max((bodyH-h)/2, 0)
	return lipgloss.NewCompositor(lipgloss.NewLayer(body), lipgloss.NewLayer(box).X(x).Y(y).Z(1)).Render()
}
