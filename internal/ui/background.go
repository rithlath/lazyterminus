package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rithlath/lazyterminus/internal/runs"
	"github.com/rithlath/lazyterminus/internal/terminus"
	"github.com/rithlath/lazyterminus/internal/ui/theme"
)

// Background runs: drush and WP-CLI run detached (package runs), so they
// keep going if lazyterminus quits, and their output is saved. The clock
// tick polls running ones; the next lazyterminus reports the ones that
// ended while it was closed.

// keepRuns is how many finished runs' output is kept on disk.
const keepRuns = 50

// recentRunFor is how long a finished run stays in the Workflows panel.
const recentRunFor = 15 * time.Minute

type runsLoadedMsg struct {
	runs []*runs.Run
	err  error
}

func loadRuns(dir string) tea.Cmd {
	if dir == "" {
		return nil
	}
	return func() tea.Msg {
		_ = runs.Prune(dir, keepRuns)
		rs, err := runs.List(dir)
		return runsLoadedMsg{runs: rs, err: err}
	}
}

// onRunsLoaded reports runs that ended while lazyterminus was closed and
// picks up ones still running.
func (m *Model) onRunsLoaded(msg runsLoadedMsg) {
	if msg.err != nil {
		m.logOutput(theme.Fail.Render(theme.SymFailed + " reading background runs: " + msg.err.Error()))
		return
	}
	ended, running := 0, 0
	for _, r := range msg.runs {
		switch {
		case !r.Done:
			running++
			m.logOutput(theme.Bold.Render("▶ "+r.Title) + theme.MutedText.Render(" (still running from an earlier session)"))
			m.logRunLines(r)
		case !r.Seen:
			ended++
			m.logOutput(theme.Bold.Render("▶ "+r.Title) + theme.MutedText.Render(" (ran while lazyterminus was closed)"))
			m.logRunLines(r)
			m.reportRunEnd(r)
		default:
			r.SkipOutput()
		}
	}
	m.runs = msg.runs
	var parts []string
	if ended > 0 {
		parts = append(parts, plural(ended, "background command")+" finished while lazyterminus was closed")
	}
	if running > 0 {
		parts = append(parts, plural(running, "background command")+" still running")
	}
	if len(parts) > 0 {
		m.setStatus(strings.Join(parts, "; ")+" · "+m.keys.Key(ActOutput)+" shows output", false)
	}
}

func (m *Model) logRunLines(r *runs.Run) {
	lines, _ := r.Poll()
	for _, l := range lines {
		m.logOutput("  " + l)
	}
}

// reportRunEnd notes a finished run in Output, on its pipeline box, and
// marks it reported.
func (m *Model) reportRunEnd(r *runs.Run) {
	what := runWhat(r)
	if r.Failed() {
		m.logOutput(theme.Fail.Render(fmt.Sprintf("%s %s failed (exit %d)", theme.SymFailed, r.Title, r.ExitCode)))
		m.event(r.Site, r.Env, landing{what: what + " failed", failed: true, count: -1})
	} else {
		m.logOutput(theme.OK.Render(theme.SymSuccess + " " + r.Title + " finished"))
		m.event(r.Site, r.Env, landing{what: what + " done", count: -1, ttl: eventFor})
	}
	m.backdate(r)
	r.MarkSeen()
}

// backdate moves the landing to when the run ended, for runs that ended
// while lazyterminus was closed (so old ones don't show as fresh).
func (m *Model) backdate(r *runs.Run) {
	if l, ok := m.landed[r.Site][r.Env]; ok && !r.Ended.IsZero() && r.Ended.Before(l.at) {
		l.at = r.Ended
		m.landed[r.Site][r.Env] = l
	}
}

// runWhat is the run's title without " on site.env": "drush cim -y".
func runWhat(r *runs.Run) string {
	return strings.TrimSuffix(r.Title, " on "+terminus.SiteEnv(r.Site, r.Env))
}

// startDetached runs args in the background through the runner chain (so
// the approval, history log and recorder see it), detached from
// lazyterminus. Without a runs directory it falls back to the terminal.
func (m *Model) startDetached(actx context.Context, title, site, env string, args []string) tea.Cmd {
	if m.opts.RunsDir == "" {
		return m.execInteractive(actx, title, site, args)
	}
	cmd, err := terminus.CommandFor(m.opts.Runner, actx, args...)
	if err != nil {
		m.reportErr(title, err)
		return nil
	}
	r, err := runs.Start(m.opts.RunsDir, cmd, runs.Meta{Title: title, Site: site, Env: env,
		Command: terminus.Args(args).String(), Started: m.now()})
	if err != nil {
		m.reportErr(title, err)
		return nil
	}
	m.runs = append(m.runs, r)
	m.logOutput(theme.Bold.Render("▶ "+title), theme.MutedText.Render("  "+terminus.Args(args).String()+" (background; keeps running if you quit)"))
	m.setStatus(fmt.Sprintf("Started in the background: %s · keeps running if you quit · %s shows output", title, m.keys.Key(ActOutput)), false)
	return tea.Batch(m.startSpinner(), m.startAnim())
}

// pollRuns reads new output from running runs and reports the ones that
// ended. It runs on the clock tick; the files are local and small.
func (m *Model) pollRuns() tea.Cmd {
	var cmds []tea.Cmd
	for _, r := range m.runs {
		if r.Done && r.Seen {
			continue
		}
		lines, ended := r.Poll()
		for _, l := range lines {
			m.logOutput("  " + l)
		}
		if !ended {
			continue
		}
		m.reportRunEnd(r)
		if r.Failed() {
			m.setStatus(fmt.Sprintf("%s %s failed (exit %d) · %s shows output", theme.SymFailed, r.Title, r.ExitCode, m.keys.Key(ActOutput)), true)
		} else {
			m.setStatus(fmt.Sprintf("%s %s finished · %s shows output", theme.SymSuccess, r.Title, m.keys.Key(ActOutput)), false)
		}
		m.client.InvalidateSite(r.Site)
		m.state(r.Site).inflight = map[string]bool{}
		cmds = append(cmds, m.ensureSite(r.Site, true), loadLocalLog(m.opts.HistoryLog))
	}
	return tea.Batch(cmds...)
}

// runningRuns returns the background runs still going.
func (m *Model) runningRuns() []*runs.Run {
	var out []*runs.Run
	for _, r := range m.runs {
		if !r.Done {
			out = append(out, r)
		}
	}
	return out
}

// runOn returns the background run going on site.env, if any.
func (m *Model) runOn(site, env string) *runs.Run {
	for _, r := range m.runningRuns() {
		if r.Site == site && r.Env == env {
			return r
		}
	}
	return nil
}

// runFor finds the run behind a history log entry: same command, started
// within a few seconds of it.
func (m *Model) runFor(l terminus.Logged) *runs.Run {
	for _, r := range m.runs {
		if r.Command == l.Command && absDur(r.Started.Sub(l.At)) < 5*time.Second {
			return r
		}
	}
	return nil
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// quitPrompt explains what quitting does to each running job and
// background run. ok=false means nothing would be lost, so quit at once.
func (m *Model) quitPrompt() (lines []string, ok bool) {
	for _, j := range m.jobs.Running() {
		if j.Step >= len(j.Steps) {
			continue
		}
		cur := terminus.Args(j.Steps[j.Step])
		var fate string
		switch c := terminus.Canonical(cur.Command()); {
		case isWorkflow(c):
			fate = theme.OK.Render("keeps running on Pantheon") + "; lazyterminus just stops watching it"
		case c == "env:wake":
			fate = "is only waking the site; nothing is lost"
		default:
			fate = theme.Fail.Render("stops") + " (it runs from this machine)"
		}
		lines = append(lines, fmt.Sprintf("• %s: %s %s", j.Title, cur.Command(), fate))
		var left []string
		for _, s := range j.Steps[j.Step+1:] {
			left = append(left, terminus.Args(s).Command())
		}
		if next := m.afterJobWhat[j.ID]; next != "" {
			left = append(left, next)
		}
		if len(left) > 0 {
			lines = append(lines, "  "+theme.Fail.Render("then "+strings.Join(left, ", ")+" won't start"))
		}
	}
	if len(lines) == 0 {
		return nil, false
	}
	for _, r := range m.runningRuns() {
		lines = append(lines, "• "+r.Title+": "+theme.OK.Render("keeps running in the background")+"; its output is saved for next time")
	}
	return lines, true
}

// isWorkflow reports whether a Terminus command starts a Pantheon workflow,
// which runs on Pantheon whatever happens to the terminus that started it.
func isWorkflow(cmd string) bool {
	switch cmd {
	case "env:deploy", "env:clear-cache", "backup:create", "env:clone-content", "connection:set",
		"env:commit", "multidev:create", "multidev:delete", "multidev:merge-to-dev", "multidev:merge-from-dev",
		"env:wipe", "upstream:updates:apply", "site:create", "site:delete":
		return true
	}
	return false
}
