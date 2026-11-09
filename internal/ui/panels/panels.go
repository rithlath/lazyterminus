package panels

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/lipgloss/v2"

	"github.com/rithlath/lazyterminus/internal/terminus"
	"github.com/rithlath/lazyterminus/internal/ui/theme"
)

// Ago formats t relative to now ("5m ago").
func Ago(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	switch {
	case d < 45*time.Second:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()+0.5))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Format("2006-01-02")
}

// Framework renders a Pantheon framework id for humans.
func Framework(f string) string {
	switch f {
	case "drupal8":
		return "drupal"
	case "drupal":
		return "drupal7"
	}
	return f
}

// ---- Sites ----------------------------------------------------------------

// SiteRow is one row in the Sites panel.
type SiteRow struct {
	Name      string
	Framework string
	EnvCount  int // -1 while loading
	Err       bool
}

// Sites renders the Sites panel body lines.
func Sites(rows []SiteRow, l *List, width, height int, focused bool, status string) []string {
	if len(rows) == 0 {
		var out []string
		for _, line := range strings.Split(status, "\n") {
			out = append(out, theme.MutedText.Render(line))
		}
		return out
	}
	nameW := 0
	for _, r := range rows {
		nameW = max(nameW, len(r.Name))
	}
	start, end := l.Window(len(rows), height)
	out := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		r := rows[i]
		count := theme.MutedText.Render("…")
		switch {
		case r.Err:
			count = theme.Fail.Render(theme.SymFailed + " error")
		case r.EnvCount >= 0:
			count = theme.MutedText.Render(fmt.Sprintf("%d envs", r.EnvCount))
		}
		text := fmt.Sprintf("%-*s  %s  %s", nameW, r.Name, theme.MutedText.Render(fmt.Sprintf("%-7s", Framework(r.Framework))), count)
		out = append(out, Row(text, width, i == l.Cursor, focused))
	}
	return out
}

// ---- Environments ---------------------------------------------------------

// EnvState is an environment's summarized health.
type EnvState int

// Environment states, in increasing urgency.
const (
	EnvUnknown EnvState = iota
	EnvOK
	EnvAttention
	EnvRunning
	EnvFailed
)

// EnvRow is one row in the Environments panel; Divider rows are labels.
type EnvRow struct {
	Name      string
	Divider   bool
	Mode      string // git, sftp
	Locked    bool
	Uninit    bool
	Ahead     int // commits ready to deploy here (test/live) or ahead (dev/multidev); -1 unknown
	AheadHint string
	Changes   int // uncommitted SFTP files; -1 unknown
	State     EnvState
	Err       string
	Protected bool
}

// StateSymbol returns the colored indicator and its no-color meaning.
func StateSymbol(s EnvState) string {
	switch s {
	case EnvOK:
		return theme.OK.Render(theme.SymOK)
	case EnvAttention:
		return theme.Warn.Render(theme.SymOK)
	case EnvRunning:
		return theme.Warn.Render(theme.SymRunning)
	case EnvFailed:
		return theme.Fail.Render(theme.SymFailed)
	}
	return theme.MutedText.Render(theme.SymUnknown)
}

// EnvBadges renders a row's badges: mode, ahead count, changes, locked, error.
func EnvBadges(r EnvRow) string {
	var b []string
	switch r.Mode {
	case "sftp":
		b = append(b, theme.SFTP.Render("sftp"))
	case "":
	default:
		b = append(b, theme.MutedText.Render(r.Mode))
	}
	if r.Ahead > 0 {
		b = append(b, theme.Warn.Render(fmt.Sprintf("%s%d", theme.SymAhead, r.Ahead)))
	}
	if r.Changes > 0 {
		b = append(b, theme.SFTP.Render(fmt.Sprintf("✎%d uncommitted", r.Changes)))
	}
	if r.Locked {
		b = append(b, theme.Warn.Render("locked"))
	}
	if r.Uninit {
		b = append(b, theme.MutedText.Render("uninitialized"))
	}
	if r.State == EnvFailed {
		b = append(b, theme.Fail.Render("failed"))
	}
	if r.State == EnvRunning {
		b = append(b, theme.Warn.Render("running"))
	}
	if r.Err != "" {
		b = append(b, theme.Fail.Render("error"))
	}
	return strings.Join(b, " ")
}

// Envs renders the Environments panel body lines.
func Envs(rows []EnvRow, l *List, width, height int, focused bool, status string) []string {
	if len(rows) == 0 {
		return []string{theme.MutedText.Render(status)}
	}
	nameW := 4
	for _, r := range rows {
		if !r.Divider {
			nameW = max(nameW, len(r.Name))
		}
	}
	nameW = min(nameW, max(width/2, 8))
	start, end := l.Window(len(rows), height)
	var out []string
	for i := start; i < end; i++ {
		r := rows[i]
		if r.Divider {
			label := " " + r.Name + " "
			dash := max(width-lipgloss.Width(label)-2, 0)
			out = append(out, theme.Divider.Render("──"+label+strings.Repeat("─", dash)))
			continue
		}
		name := Fit(r.Name, nameW)
		text := StateSymbol(r.State) + " " + name + "  " + EnvBadges(r)
		out = append(out, Row(text, width, i == l.Cursor, focused))
	}
	return out
}

// ---- Detail ---------------------------------------------------------------

// DetailData is everything the Detail panel shows for one environment.
type DetailData struct {
	Site, EnvID     string
	Env             *terminus.Env
	Protected       bool
	Drupal          string // version or ""
	DrupalNote      string // "(local repo)", "no repo mapped", ...
	AheadTarget     string // env the Ahead count is relative to
	AheadLabel      string // e.g. "ready to deploy from dev"
	Ahead           []terminus.Commit
	AheadKnown      bool
	Diffstat        []terminus.DiffstatEntry
	DiffstatKnown   bool
	Recent          []terminus.Commit // the environment's own log, newest first
	RecentKnown     bool
	LastDeploy      *terminus.Workflow
	Failed          *terminus.Workflow
	FailReason      string
	Branch          string
	LocalBranch     string // local git status for multidevs
	UpstreamPending int    // -1 unknown
	Domains         []terminus.Domain
	DomainsKnown    bool
	Local           []terminus.Logged // this machine's commands here, newest first
	OutputKey       string            // the key that opens Output
	Err             string
	Now             time.Time
}

func field(label, value string) string {
	return theme.Label.Render(label) + value
}

// Detail renders the Detail panel body lines.
func Detail(d DetailData, width, height int) []string {
	if d.Env == nil {
		if d.Err != "" {
			return []string{theme.Fail.Render(theme.SymFailed + " " + d.Err)}
		}
		return []string{theme.MutedText.Render("Select an environment.")}
	}
	e := d.Env
	var out []string
	add := func(l, v string) { out = append(out, field(l, v)) }

	add("URL", terminus.URL(e.Domain))
	if custom := terminus.CustomDomains(d.Domains); len(custom) > 0 {
		for i, c := range custom {
			label := ""
			if i == 0 {
				label = "Custom"
			}
			v := terminus.URL(c.ID)
			if c.Primary {
				v += theme.MutedText.Render(" (primary)")
			}
			add(label, v)
		}
	} else if !d.DomainsKnown {
		add("Custom", theme.MutedText.Render("loading…"))
	}
	mode := theme.MutedText.Render(e.ConnectionMode)
	if e.ConnectionMode == "sftp" {
		mode = theme.SFTP.Render("sftp") + theme.MutedText.Render(" (on-server development)")
	}
	add("Connection", mode)
	php := e.PHPVersion
	if php == "" {
		php = "—"
	}
	add("PHP", php)
	drupal := d.Drupal
	if drupal == "" {
		drupal = "unknown"
	}
	add("Drupal", drupal+" "+theme.MutedText.Render(d.DrupalNote))
	if d.Protected {
		add("Protection", theme.Warn.Render("protected")+theme.MutedText.Render(" (typed confirmation)"))
	}
	if e.Locked {
		add("Locked", theme.Warn.Render("yes")+theme.MutedText.Render(" (HTTP auth)"))
	}
	if d.Branch != "" {
		add("Branch", d.Branch)
		if d.LocalBranch != "" {
			add("Local", d.LocalBranch)
		}
	}

	if d.AheadTarget != "" {
		switch {
		case !d.AheadKnown:
			add("Commits", theme.MutedText.Render("loading…"))
		case len(d.Ahead) == 0:
			add("Commits", theme.OK.Render(theme.SymSuccess+" in sync")+theme.MutedText.Render(" with "+d.AheadTarget))
		default:
			add("Commits", theme.Warn.Render(fmt.Sprintf("%s%d %s", theme.SymAhead, len(d.Ahead), d.AheadLabel)))
		}
	}
	if e.ConnectionMode == "sftp" {
		switch {
		case !d.DiffstatKnown:
			add("Uncommitted", theme.MutedText.Render("loading…"))
		case len(d.Diffstat) == 0:
			add("Uncommitted", theme.OK.Render("none"))
		default:
			adds, dels := 0, 0
			for _, f := range d.Diffstat {
				adds += int(f.Additions)
				dels += int(f.Deletions)
			}
			add("Uncommitted", theme.SFTP.Render(fmt.Sprintf("✎ %d files (+%d −%d)", len(d.Diffstat), adds, dels)))
		}
	}
	if d.LastDeploy != nil {
		sym := theme.OK.Render(theme.SymSuccess)
		if d.LastDeploy.Status == terminus.StatusFailed {
			sym = theme.Fail.Render(theme.SymFailed)
		} else if d.LastDeploy.Running() {
			sym = theme.Warn.Render(theme.SymRunning)
		}
		add("Last deploy", sym+" "+Ago(d.LastDeploy.StartedAt.Time(), d.Now)+theme.MutedText.Render(" by "+d.LastDeploy.User))
	} else if e.ID == "test" || e.ID == "live" {
		add("Last deploy", theme.MutedText.Render("none in recent workflows"))
	}
	if d.UpstreamPending > 0 {
		add("Upstream", theme.Warn.Render(fmt.Sprintf("%d updates available", d.UpstreamPending)))
	}
	if d.Failed != nil {
		out = append(out, "")
		out = append(out, theme.Fail.Render(fmt.Sprintf("%s Failed %s: %s", theme.SymFailed, Ago(d.Failed.StartedAt.Time(), d.Now), d.Failed.Workflow)))
		reason := d.FailReason
		if reason == "" {
			reason = "loading reason…"
		}
		out = append(out, theme.MutedText.Render("  "+reason))
	}
	if len(d.Ahead) > 0 {
		out = append(out, "", theme.Bold.Render("Commits "+d.AheadLabel+":"))
		for _, c := range d.Ahead {
			out = append(out, fmt.Sprintf("  %s %s %s", theme.Warn.Render(short(c.Hash)), c.Subject(), theme.MutedText.Render("— "+c.Author)))
		}
	}
	if len(d.Diffstat) > 0 {
		out = append(out, "", theme.Bold.Render("Uncommitted changes:"))
		for _, f := range d.Diffstat {
			out = append(out, fmt.Sprintf("  %s %s %s", theme.SFTP.Render(Fit(f.Status, 2)), f.File,
				theme.MutedText.Render(fmt.Sprintf("+%d −%d", f.Additions, f.Deletions))))
		}
	}
	if len(d.Local) > 0 {
		out = append(out, "")
		out = append(out, LocalCommands(d.Local, terminus.SiteEnv(d.Site, e.ID), e.ID, d.OutputKey, d.Now, width)...)
	}
	out = append(out, "", theme.Bold.Render("Recent commits on "+e.ID+":"))
	switch {
	case !d.RecentKnown:
		out = append(out, theme.MutedText.Render("  loading…"))
	case len(d.Recent) == 0:
		out = append(out, theme.MutedText.Render("  none"))
	}
	for _, c := range d.Recent[:min(len(d.Recent), recentCommits)] {
		// Shorten the message, not who and when.
		who := "— " + c.Author + " · " + Ago(c.Time(), d.Now)
		msg := Fit(c.Subject(), max(width-2-len(short(c.Hash))-1-lipgloss.Width(who)-1, 12))
		out = append(out, fmt.Sprintf("  %s %s %s", theme.Warn.Render(short(c.Hash)), msg, theme.MutedText.Render(who)))
	}
	if len(out) > height && height > 0 {
		out = append(out[:height-1], theme.MutedText.Render(fmt.Sprintf("… %d more lines", len(out)-height+1)))
	}
	return out
}

// LocalCommands renders commands this machine ran on env through
// lazyterminus, under a heading that says they're local, not team-wide, and
// which key shows their output.
func LocalCommands(cmds []terminus.Logged, siteEnv, env, outputKey string, now time.Time, width int) []string {
	out := []string{Fit(theme.Bold.Render("Your recent commands on "+env)+theme.MutedText.Render(" (this machine only) · ")+
		theme.Key.Render(outputKey)+theme.MutedText.Render(" shows output"), width)}
	for _, c := range cmds {
		var sym string
		switch {
		case c.Result == "ok":
			sym = theme.OK.Render(theme.SymSuccess)
		case strings.HasPrefix(c.Result, "failed"):
			sym = theme.Fail.Render(theme.SymFailed)
		case c.Result == "running":
			sym = theme.Warn.Render(theme.SymRunning)
		case c.Result == "exec":
			sym = theme.MutedText.Render("↗") // ran in the terminal; exit not logged
		default:
			sym = theme.MutedText.Render(theme.SymUnknown)
		}
		when := theme.MutedText.Render(Fit(Ago(c.At, now), 8))
		out = append(out, Fit(fmt.Sprintf("  %s %s %s", sym, when, c.Describe(siteEnv)), width))
	}
	return out
}

// recentCommits is how many of an environment's own commits Detail lists.
const recentCommits = 8

func short(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

// ---- Workflows ------------------------------------------------------------

// JobRow is a local background job (a Terminus process lazyterminus started).
type JobRow struct {
	Title      string
	Background bool // detached; keeps running if lazyterminus quits
	Running    bool
	Failed     bool
	Err        string
	Started    time.Time
	Ended      time.Time
}

// WorkflowsData is everything the Workflows panel shows.
type WorkflowsData struct {
	Jobs      []JobRow
	Workflows []terminus.Workflow
	Progress  map[string][2]int // workflow id → done,total
	Spinner   string
	Frame     int // animation frame for running background rows
	Bar       progress.Model
	Now       time.Time
	Status    string // shown when there is nothing else
}

// Workflows renders the Workflows panel body lines. Running items come
// first, then recent finished workflows newest first.
func Workflows(d WorkflowsData, l *List, width, height int, focused bool) []string {
	var lines []string
	for _, j := range d.Jobs {
		switch {
		case j.Running && j.Background:
			lines = append(lines, theme.Warn.Render(d.Spinner)+" "+theme.Shimmer(j.Title, d.Frame)+theme.MutedText.Render(" · "+d.Now.Sub(j.Started).Round(time.Second).String()))
		case j.Running:
			lines = append(lines, theme.Warn.Render(d.Spinner)+" "+j.Title+theme.MutedText.Render(" · job · "+d.Now.Sub(j.Started).Round(time.Second).String()))
		case j.Failed:
			lines = append(lines, theme.Fail.Render(theme.SymFailed)+" "+j.Title+theme.Fail.Render(" · "+j.Err))
		default:
			lines = append(lines, theme.OK.Render(theme.SymSuccess)+" "+j.Title+theme.MutedText.Render(" · job · "+Ago(j.Ended, d.Now)))
		}
	}
	for _, w := range d.Workflows {
		if !w.Running() {
			continue
		}
		lines = append(lines, theme.Warn.Render(d.Spinner)+" "+w.Workflow+theme.MutedText.Render(" · "+envOrSite(w)+" · "+d.Now.Sub(w.StartedAt.Time()).Round(time.Second).String()))
		if p, ok := d.Progress[w.ID]; ok && p[1] > 0 {
			bar := d.Bar
			bar.SetWidth(max(width-12, 10))
			lines = append(lines, "  "+bar.ViewAs(float64(p[0])/float64(p[1]))+theme.MutedText.Render(fmt.Sprintf(" %d/%d", p[0], p[1])))
		} else {
			lines = append(lines, theme.MutedText.Render("  starting…"))
		}
	}
	for _, w := range d.Workflows {
		if w.Running() {
			continue
		}
		sym := theme.OK.Render(theme.SymSuccess)
		if w.Status == terminus.StatusFailed {
			sym = theme.Fail.Render(theme.SymFailed)
		}
		right := " " + envOrSite(w) + " · " + Ago(w.StartedAt.Time(), d.Now)
		desc := Fit(w.Workflow, max(width-2-lipgloss.Width(right)-2, 8))
		lines = append(lines, sym+" "+desc+theme.MutedText.Render(right))
	}
	if len(lines) == 0 {
		return []string{theme.MutedText.Render(d.Status)}
	}
	sel := make([]bool, len(lines))
	for i := range sel {
		sel[i] = true
	}
	l.Clamp(sel)
	start, end := l.Window(len(lines), height)
	out := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, Row(lines[i], width, focused && i == l.Cursor, focused))
	}
	return out
}

func envOrSite(w terminus.Workflow) string {
	if e := w.EnvName(); e != "" {
		return e
	}
	return "site"
}
