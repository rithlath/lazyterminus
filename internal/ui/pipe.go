package ui

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rithlath/lazyterminus/internal/jobs"
	"github.com/rithlath/lazyterminus/internal/terminus"
	"github.com/rithlath/lazyterminus/internal/ui/panels"
	"github.com/rithlath/lazyterminus/internal/ui/pipeline"
	"github.com/rithlath/lazyterminus/internal/ui/theme"
)

// landedFor is how long a box stays green after a deploy lands.
const landedFor = 15 * time.Minute

// animEvery is the pipeline animation frame interval.
const animEvery = 120 * time.Millisecond

type animMsg time.Time

// eventFor is how long a finished action (other than a deploy) stays on its
// box.
const eventFor = 3 * time.Minute

// landing records something that finished on an environment: a deploy or
// code sync seen in workflows, or one of lazyterminus's own jobs.
type landing struct {
	at     time.Time
	count  int    // commits a deploy carried; -1 unknown
	what   string // "deploy", "code sync", or the action ("cache cleared")
	failed bool
	ttl    time.Duration // 0: landedFor
}

// stepInfo describes what a job step does to which environment, for the
// pipeline: what to show while it runs and once it's done.
type stepInfo struct {
	site, env   string
	doing, done string
	from        string // clone source
}

func describeStep(args []string) (stepInfo, bool) {
	a := terminus.Args(args)
	pos := a.Positionals()
	if len(pos) == 0 {
		return stepInfo{}, false
	}
	site, env, ok := terminus.SplitSiteEnv(pos[0])
	if !ok {
		return stepInfo{}, false
	}
	i := stepInfo{site: site, env: env}
	switch cmd := terminus.Canonical(a.Command()); cmd {
	case "env:deploy":
		i.doing, i.done = "deploying", "deploy"
	case "env:clear-cache":
		i.doing, i.done = "clearing cache", "cache cleared"
	case "backup:create":
		i.doing, i.done = "creating backup", "backup created"
	case "env:wake":
		i.doing, i.done = "waking", "awake"
	case "env:commit":
		i.doing, i.done = "committing changes", "changes committed"
	case "env:clone-content":
		if len(pos) < 2 {
			return stepInfo{}, false
		}
		what := "database and files"
		if a.HasFlag("db-only") {
			what = "database"
		} else if a.HasFlag("files-only") {
			what = "files"
		}
		i.from, i.env = env, pos[1]
		i.doing, i.done = "copying "+what+" from "+env, what+" cloned from "+env
	case "connection:set":
		if len(pos) < 2 {
			return stepInfo{}, false
		}
		i.doing, i.done = "switching to "+pos[1], "switched to "+pos[1]
	case "multidev:create":
		if len(pos) < 2 {
			return stepInfo{}, false
		}
		i.doing, i.done = "creating multidev "+pos[1], "created multidev "+pos[1]
	case "multidev:delete":
		i.doing, i.done = "deleting", "deleted"
	case "remote:drush", "remote:wp":
		what := strings.TrimPrefix(cmd, "remote:")
		if k := slices.Index(args, "--"); k >= 0 && k+1 < len(args) {
			what += " " + strings.Join(args[k+1:], " ")
		}
		i.doing, i.done = "running "+what, what+" done"
	default:
		i.doing, i.done = "running "+cmd, cmd+" done"
	}
	return i, true
}

func animTick() tea.Cmd {
	return tea.Tick(animEvery, func(t time.Time) tea.Msg { return animMsg(t) })
}

// startAnim begins the frame ticker if the pipeline is showing something
// in motion. Only one ticker runs at a time.
func (m *Model) startAnim() tea.Cmd {
	if m.animating || !m.showPipeline || !m.pipelineGraph().Animated() {
		return nil
	}
	m.animating = true
	return animTick()
}

func (m *Model) onAnim() tea.Cmd {
	m.frame++
	if !m.showPipeline || !m.pipelineGraph().Animated() {
		m.animating = false
		return nil
	}
	return animTick()
}

// recordLandings notes deploys and code syncs that finished between two
// workflow polls. It must run before the site's code logs are reloaded, so
// the commit count is what was waiting before the deploy.
func (m *Model) recordLandings(site string, prev, next []terminus.Workflow) {
	known := map[string]bool{}
	for _, w := range prev {
		known[w.ID] = true
	}
	var done []terminus.Workflow
	done = append(done, finishedSince(prev, next)...)
	for _, w := range next {
		// Short deploys can start and finish between two polls.
		if !known[w.ID] && !w.Running() && w.StartedAt.Time().After(m.started) {
			done = append(done, w)
		}
	}
	for _, w := range done {
		if w.Status != terminus.StatusSucceeded {
			continue
		}
		env := w.EnvName()
		switch {
		case terminus.IsDeployWorkflow(w, env):
			m.land(site, env, m.waitingInto(site, env), "deploy")
		case strings.HasPrefix(w.Workflow, "Sync code on") && env != "":
			m.land(site, env, -1, "code sync")
		}
	}
}

func (m *Model) land(site, env string, count int, what string) {
	if l, ok := m.landed[site][env]; ok && m.now().Sub(l.at) < time.Minute && l.what == what {
		return // already recorded from the job or the other poll path
	}
	m.event(site, env, landing{count: count, what: what})
}

func (m *Model) event(site, env string, l landing) {
	if m.landed[site] == nil {
		m.landed[site] = map[string]landing{}
	}
	l.at = m.now()
	m.landed[site][env] = l
}

// noteJobDone shows a finished job on its environment's box: deploys as
// landings (in case polling never saw the workflow running), other actions
// as a short-lived "✓ cache cleared", failures as "✗ … failed".
func (m *Model) noteJobDone(j *jobs.Job) {
	if j.State == jobs.Failed {
		if j.Step < len(j.Steps) {
			if i, ok := describeStep(j.Steps[j.Step]); ok {
				m.event(i.site, i.env, landing{what: i.doing + " failed", failed: true, count: -1})
			}
		}
		return
	}
	var last stepInfo
	found := false
	for _, s := range j.Steps {
		i, ok := describeStep(s)
		if !ok {
			continue
		}
		if i.done == "deploy" {
			m.land(i.site, i.env, m.waitingInto(i.site, i.env), "deploy")
			return
		}
		if !found || i.done != "awake" { // waking is a preamble; report the real action
			last, found = i, true
		}
	}
	if found {
		m.event(last.site, last.env, landing{what: last.done, count: -1, ttl: eventFor})
	}
}

// jobOn returns what a running lazyterminus job is doing to env, if any.
func (m *Model) jobOn(site, env string) (string, *jobs.Job) {
	for _, j := range m.jobs.Running() {
		if j.Step >= len(j.Steps) {
			continue
		}
		if i, ok := describeStep(j.Steps[j.Step]); ok && i.site == site && i.env == env {
			return i.doing, j
		}
	}
	return "", nil
}

// waitingInto is the number of commits waiting to deploy into env, from the
// currently loaded logs (-1 unknown).
func (m *Model) waitingInto(site, env string) int {
	st := m.state(site)
	src := terminus.SourceEnv(env)
	log, ok := st.codeLogs[src]
	if src == "" || !ok {
		return -1
	}
	return len(terminus.DeployableCommits(log, src, env))
}

func (m *Model) recentLanding(site, env string) (landing, bool) {
	l, ok := m.landed[site][env]
	ttl := l.ttl
	if ttl == 0 {
		ttl = landedFor
	}
	if !ok || m.now().Sub(l.at) > ttl {
		return landing{}, false
	}
	return l, true
}

// runningFor returns the running Pantheon workflow on env, if any, and
// whether a lazyterminus job is deploying to it.
func (m *Model) runningFor(site, env string) (*terminus.Workflow, bool) {
	st := m.state(site)
	for i := range st.wfs {
		if st.wfs[i].Running() && st.wfs[i].EnvName() == env {
			return &st.wfs[i], false
		}
	}
	doing, _ := m.jobOn(site, env)
	return nil, doing == "deploying"
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// pipelineGraph builds the selected site's pipeline from loaded data.
func (m *Model) pipelineGraph() pipeline.Graph {
	site := m.site()
	st := m.state(site)
	now := m.now()
	var g pipeline.Graph

	node := func(e terminus.Env) pipeline.Node {
		n := pipeline.Node{ID: e.ID, Mode: e.ConnectionMode, State: pipeline.Unknown, Status: "loading…", Progress: -1}
		log, haveLog := st.codeLogs[e.ID]
		if haveLog {
			n.State, n.Status = pipeline.OK, "in sync"
			if len(log) > 0 {
				n.Head = short(log[0].Hash) + " " + log[0].Subject()
				if e.ID == "dev" || e.IsMultidev() {
					n.When = "committed " + panels.Ago(log[0].Time(), now)
				}
			} else {
				n.Head = "no commits"
			}
		}
		if wf, ok := terminus.LastDeploy(st.wfs, e.ID); ok && (e.ID == "test" || e.ID == "live") {
			n.When = "deployed " + panels.Ago(wf.StartedAt.Time(), now)
		}
		if ds, ok := st.diffstat[e.ID]; ok && len(ds) > 0 {
			n.Extra = fmt.Sprintf("✎%d uncommitted", len(ds))
			n.State, n.Status = pipeline.Waiting, "uncommitted changes"
		}
		// Commits waiting to come into this environment.
		if src := terminus.SourceEnv(e.ID); src != "" {
			if p := m.waitingInto(site, e.ID); p > 0 {
				n.State, n.Status = pipeline.Waiting, plural(p, "commit")+" waiting"
			}
		}
		if e.IsMultidev() {
			if mdLog, ok1 := st.codeLogs[e.ID]; ok1 {
				if devLog, ok2 := st.codeLogs["dev"]; ok2 {
					ahead := len(terminus.CommitsNotIn(mdLog, devLog))
					behind := len(terminus.CommitsNotIn(devLog, mdLog))
					switch {
					case ahead > 0 && behind > 0:
						n.State, n.Status = pipeline.Waiting, fmt.Sprintf("%d ahead · ↓%d behind", ahead, behind)
					case ahead > 0:
						n.State, n.Status = pipeline.Waiting, fmt.Sprintf("%d ahead of dev", ahead)
					case behind > 0: // dev moved on; worth a merge, not urgent
						n.Status = fmt.Sprintf("↓%d behind dev", behind)
					}
				}
			}
		}
		if l, ok := m.recentLanding(site, e.ID); ok {
			n.State = pipeline.Landed
			switch {
			case l.failed:
				n.State, n.Status = pipeline.Failed, l.what
			case l.what == "deploy" && l.count > 0:
				n.Status = "landed " + plural(l.count, "commit")
			case l.what == "deploy":
				n.Status = "landed"
			case l.what == "code sync":
				n.Status = "code synced"
			default:
				n.Status = l.what
			}
			n.Status += " · " + panels.Ago(l.at, now)
		}
		if f, ok := terminus.LastFailed(st.wfs, e.ID); ok {
			if l, landed := m.recentLanding(site, e.ID); !landed || f.StartedAt.Time().After(l.at) {
				n.State, n.Status = pipeline.Failed, "failed "+panels.Ago(f.StartedAt.Time(), now)
			}
		}
		doing, job := m.jobOn(site, e.ID)
		if wf, _ := m.runningFor(site, e.ID); wf != nil || job != nil {
			n.State = pipeline.Running
			n.Status, n.When = doing, ""
			if job != nil { // the step number is in the footer
				n.When = now.Sub(job.Started).Round(time.Second).String()
			}
			if wf != nil {
				if job == nil { // started elsewhere: Pantheon's description is all we have
					n.Status = strings.ToLower(wf.Workflow[:1]) + wf.Workflow[1:]
					if terminus.IsDeployWorkflow(*wf, e.ID) {
						n.Status = "deploying"
					}
					n.When = now.Sub(wf.StartedAt.Time()).Round(time.Second).String()
				}
				if ops, ok := st.ops[wf.ID]; ok {
					if done, total := terminus.Progress(ops); total > 0 {
						n.Progress = float64(done) / float64(total)
						n.Status += fmt.Sprintf(" %d/%d", done, total)
					}
				}
			}
		}
		if r := m.runOn(site, e.ID); r != nil && n.State != pipeline.Running {
			n.State, n.Status = pipeline.Running, "running "+runWhat(r)
			n.When = now.Sub(r.Started).Round(time.Second).String()
		}
		return n
	}

	for _, id := range []string{"dev", "test", "live"} {
		if e := st.env(id); e != nil {
			g.Main = append(g.Main, node(*e))
		}
	}
	for _, e := range st.envs {
		if e.IsMultidev() {
			g.Multidevs = append(g.Multidevs, node(e))
		}
	}
	// Hops.
	for _, pair := range [][2]string{{"dev", "test"}, {"test", "live"}} {
		if st.env(pair[0]) == nil || st.env(pair[1]) == nil {
			continue
		}
		wf, job := m.runningFor(site, pair[1])
		active := job || (wf != nil && terminus.IsDeployWorkflow(*wf, pair[1]))
		g.Edges = append(g.Edges, pipeline.Edge{From: pair[0], To: pair[1], Pending: m.waitingInto(site, pair[1]), Active: active})
	}
	for _, j := range m.jobs.Running() {
		if j.Step < len(j.Steps) {
			if i, ok := describeStep(j.Steps[j.Step]); ok && i.site == site && i.from != "" {
				g.Flows = append(g.Flows, pipeline.Flow{From: i.from, To: i.env, Label: i.doing + " → " + i.env, Active: true})
			}
		}
	}
	for _, w := range st.wfs { // clones started elsewhere (dashboard, another terminal)
		if w.Running() && strings.HasPrefix(w.Workflow, "Clone") {
			if src := cloneSource.FindStringSubmatch(w.Workflow); src != nil && w.EnvName() != "" && !hasFlow(g.Flows, src[1], w.EnvName()) {
				g.Flows = append(g.Flows, pipeline.Flow{From: src[1], To: w.EnvName(), Label: "cloning content " + src[1] + " → " + w.EnvName(), Active: true})
			}
		}
	}
	devLog, haveDev := st.codeLogs["dev"]
	for _, md := range g.Multidevs {
		e := pipeline.Edge{From: md.ID, To: "dev", Pending: -1}
		if mdLog, ok := st.codeLogs[md.ID]; ok && haveDev {
			e.Pending = len(terminus.CommitsNotIn(mdLog, devLog))
		}
		// Merges into dev: matched by name, since the exact workflow name for
		// multidev:merge-to-dev wasn't in the captured data.
		for _, w := range st.wfs {
			if w.Running() && strings.Contains(strings.ToLower(w.Workflow), "merge") &&
				(w.EnvName() == md.ID || strings.Contains(w.Workflow, md.ID)) {
				e.Active = true
			}
		}
		g.Edges = append(g.Edges, e)
	}
	return g
}

// cloneSource finds the source environment in a clone workflow's
// description, e.g. `Clone database from "live" to "dev"`. The real wording
// isn't in the captured data; this is a best guess.
var cloneSource = regexp.MustCompile(`from "([^"]+)"`)

func hasFlow(fs []pipeline.Flow, from, to string) bool {
	for _, f := range fs {
		if f.From == from && f.To == to {
			return true
		}
	}
	return false
}

// pipelineFooter explains the selected environment under the graph.
func (m *Model) pipelineFooter(width, maxLines int) []string {
	site, env := m.site(), m.selectedEnv()
	st := m.state(site)
	var out []string
	add := func(s ...string) { out = append(out, s...) }
	commits := func(title string, cs []terminus.Commit) {
		add(theme.Bold.Render(title))
		for i, c := range cs {
			if i == 4 && len(cs) > 5 {
				add(theme.MutedText.Render(fmt.Sprintf("  … %d more", len(cs)-4)))
				break
			}
			add(fmt.Sprintf("  %s %s %s", theme.Warn.Render(short(c.Hash)), c.Subject(), theme.MutedText.Render("— "+c.Author)))
		}
	}
	if env == "" {
		return []string{theme.MutedText.Render("Select an environment.")}
	}
	if src := terminus.SourceEnv(env); src != "" {
		log := st.codeLogs[src]
		cs := terminus.DeployableCommits(log, src, env)
		wf, job := m.runningFor(site, env)
		switch {
		case (wf != nil && terminus.IsDeployWorkflow(*wf, env)) || job:
			commits(fmt.Sprintf("Moving %s → %s (%s):", src, env, plural(len(cs), "commit")), cs)
		case len(cs) > 0:
			commits(fmt.Sprintf("Waiting to deploy %s → %s (%s) · press %s:", src, env, plural(len(cs), "commit"), m.keys.Key(ActDeploy)), cs)
		}
	} else if e := st.env(env); e != nil && e.IsMultidev() {
		if mdLog, ok := st.codeLogs[env]; ok {
			if cs := terminus.CommitsNotIn(mdLog, st.codeLogs["dev"]); len(cs) > 0 {
				commits(fmt.Sprintf("On %s but not on dev (%s):", env, plural(len(cs), "commit")), cs)
			}
		}
	}
	if doing, j := m.jobOn(site, env); j != nil {
		line := fmt.Sprintf("%s %s on %s", theme.SymRunning, doing, env)
		if len(j.Steps) > 1 {
			line += fmt.Sprintf(" (step %d of %d: %s)", j.Step+1, len(j.Steps), terminus.Args(j.Steps[j.Step]).String())
		}
		add(theme.Warn.Render(line))
	}
	if r := m.runOn(site, env); r != nil {
		add(theme.Warn.Render(fmt.Sprintf("%s %s on %s, in the background for %s", theme.SymRunning, runWhat(r), env,
			m.now().Sub(r.Started).Round(time.Second))) +
			theme.MutedText.Render(fmt.Sprintf(" · keeps going if you quit · %s shows output", m.keys.Key(ActOutput))))
	}
	if l, ok := m.recentLanding(site, env); ok {
		at := l.at.Local().Format("15:04")
		switch {
		case l.failed:
			msg := ""
			for _, j := range m.jobs.Recent(5) {
				if j.State == jobs.Failed && j.Err != nil && j.Site == site {
					msg = ": " + j.Err.Error()
					break
				}
			}
			add(theme.Fail.Render(fmt.Sprintf("%s %s on %s at %s%s", theme.SymFailed, l.what, env, at, msg)))
		case l.what == "deploy" || l.what == "code sync":
			what := l.what
			if l.count > 0 {
				what += " of " + plural(l.count, "commit")
			}
			add(theme.OK.Render(fmt.Sprintf("%s %s landed on %s at %s", theme.SymSuccess, what, env, at)))
		default:
			add(theme.OK.Render(fmt.Sprintf("%s %s on %s at %s", theme.SymSuccess, l.what, env, at)))
		}
	}
	if f, ok := terminus.LastFailed(st.wfs, env); ok {
		reason := terminus.FailureReason(st.ops[f.ID])
		if reason == "" {
			reason = "reason loading…"
		}
		add(theme.Fail.Render(fmt.Sprintf("%s %s: %s failed %s: %s", theme.SymFailed, env, f.Workflow, panels.Ago(f.StartedAt.Time(), m.now()), reason)))
	}
	if cmds := m.localCommands(site, env, localShown); len(cmds) > 0 {
		cmds = cmds[:min(len(cmds), max(maxLines-len(out)-3, 0))] // leave room for commits
		if len(cmds) > 0 {
			add(panels.LocalCommands(cmds, terminus.SiteEnv(site, env), env, m.keys.Key(ActOutput), m.now(), width)...)
		}
	}
	// The environment's own history fills what's left.
	if room := min(maxLines-len(out)-1, pipeRecent); room > 0 {
		log, ok := st.codeLogs[env]
		add(theme.Bold.Render("Recent commits on " + env + ":"))
		switch {
		case !ok:
			add(theme.MutedText.Render("  loading…"))
		case len(log) == 0:
			add(theme.MutedText.Render("  none"))
		}
		for _, c := range log[:min(len(log), room)] {
			// Shorten the message, not who and when.
			who := "— " + c.Author + " · " + panels.Ago(c.Time(), m.now())
			msg := panels.Fit(c.Subject(), max(width-2-len(short(c.Hash))-1-ansi.StringWidth(who)-1, 12))
			add(fmt.Sprintf("  %s %s %s", theme.Warn.Render(short(c.Hash)), msg, theme.MutedText.Render(who)))
		}
	}
	if len(out) > maxLines {
		out = out[:maxLines]
	}
	return out
}

// localShown is how many of this machine's commands on an environment the
// Detail panel and pipeline footer list.
const localShown = 5

// pipeRecent is how many of the selected environment's commits the
// pipeline footer lists, room permitting.
const pipeRecent = 8

// renderPipeline draws the pipeline view body.
func (m *Model) renderPipeline(w, h int) string {
	innerW, innerH := max(w-4, 1), max(h-2, 1)
	g := m.pipelineGraph()
	legend := theme.MutedText.Render("● in sync   ") + theme.Warn.Render("↑ waiting   ◐ running   ") +
		theme.OK.Render("✓ landed   ") + theme.Fail.Render("✗ failed")
	budget := innerH / 3
	if innerW >= pipeline.MinWideWidth { // boxes sit side by side; history gets more room
		budget = innerH / 2
	}
	footer := m.pipelineFooter(innerW, max(budget, 3))
	url, urls := m.urlLine(innerW)
	if url != "" {
		footer = append([]string{url}, footer...)
	}
	graphH := max(innerH-2-len(footer), 1)
	lines := []string{legend, ""}
	graph, hits := pipeline.RenderHits(g, innerW, graphH, m.selectedEnv(), m.frame)
	lines = append(lines, graph...)
	lines = append(lines, footer...)
	const gx, gy = 2, 3 // graph origin: border and padding, legend, blank line
	for _, ht := range hits {
		m.zones.add(gx+ht.X, gy+ht.Y, ht.W, min(ht.H, graphH-ht.Y), clickTarget{kind: clickPipe, str: ht.ID})
	}
	for _, u := range urls { // the URL line is the footer's first line
		m.zones.add(gx+u.x, gy+graphH, u.w, 1, clickTarget{kind: clickURL, str: u.url})
	}
	title := " Pipeline "
	if s := m.site(); s != "" {
		title = " Pipeline · " + s + " "
		if env := m.selectedEnv(); env != "" {
			title = " Pipeline · " + s + " · " + env + " "
		}
	}
	hint := fmt.Sprintf(" ←/→ select · enter actions · %s deploy · %s open · %s/esc back ", m.keys.Key(ActDeploy), m.keys.Key(ActOpenURL), m.keys.Key(ActPipeline))
	m.zones.footer(hint, 0, h-1, w)
	return panels.Box(title, lines, w, h, true, hint)
}

// selectEnv moves the shared environment selection (so every action key
// acts on the box selected in the pipeline).
func (m *Model) selectEnv(id string) tea.Cmd {
	rows := m.envRows()
	for i, r := range rows {
		if !r.Divider && r.Name == id {
			m.envList.Cursor = i
			m.detailOffset = 0
			return tea.Batch(m.ensureOps(m.site()), m.ensureBranch(), m.ensureDomains())
		}
	}
	return nil
}

// pipelineKey handles keys specific to the pipeline view; ok=false passes
// the key on to the normal handler (action keys, palette, quit, …).
func (m *Model) pipelineKey(k string) (tea.Cmd, bool) {
	order := m.pipelineGraph().Order()
	cur := -1
	for i, id := range order {
		if id == m.selectedEnv() {
			cur = i
		}
	}
	move := func(d int) tea.Cmd {
		if len(order) == 0 {
			return nil
		}
		next := min(max(cur+d, 0), len(order)-1)
		if cur < 0 {
			next = 0
		}
		return m.selectEnv(order[next])
	}
	switch k {
	case "esc":
		m.showPipeline = false
		return nil, true
	// No h/l here: l is the clone-content key, and action keys must mean the
	// same thing in every view.
	case "left", "up", "k":
		return move(-1), true
	case "right", "down", "j":
		return move(1), true
	case "enter":
		return m.openEnvMenu(), true
	}
	return nil, false
}
