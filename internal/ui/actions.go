package ui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/rithlath/lazyterminus/internal/jobs"
	"github.com/rithlath/lazyterminus/internal/safety"
	"github.com/rithlath/lazyterminus/internal/terminus"
	"github.com/rithlath/lazyterminus/internal/ui/panels"
	"github.com/rithlath/lazyterminus/internal/ui/theme"
)

// Messages for action pre-checks that need fresh data first.
type (
	deployPreviewMsg struct {
		site, env, src string
		log, commits   []terminus.Commit
		err            error
	}
	diffPreviewMsg struct {
		site, env, purpose string
		entries            []terminus.DiffstatEntry
		err                error
	}
	upstreamMsg struct {
		site, env, status string
		updates           []terminus.UpstreamUpdate
		err               error
	}
	execDoneMsg struct {
		title, site string
		err         error
	}
)

// policy builds the safety policy from config. main installs a Guard with
// the same policy, so what the UI asks for is what the runner enforces.
func (m *Model) policy() safety.Policy { return safety.PolicyFrom(m.cfg) }

// target returns the selected site and environment for an action, or sets
// a status message and returns ok=false.
func (m *Model) target() (site string, env *terminus.Env, ok bool) {
	site = m.site()
	if site == "" {
		m.setStatus("No site selected.", true)
		return "", nil, false
	}
	envID := m.selectedEnv()
	e := m.state(site).env(envID)
	if e == nil {
		m.setStatus("Select an environment first (press 2).", true)
		return "", nil, false
	}
	return site, e, true
}

func (m *Model) open(md modal) tea.Cmd {
	m.modal = md
	if f, ok := md.(*form); ok {
		return f.focusField(0)
	}
	return nil
}

// wakeSteps returns env:wake steps for development environments among envs.
// Terminus can't report whether an environment is asleep, so lazyterminus
// always wakes dev and multidevs before commands that need them awake.
// Test and live don't sleep on paid plans and are never auto-woken.
func wakeSteps(site string, envs ...string) [][]string {
	var out [][]string
	seen := map[string]bool{}
	for _, e := range envs {
		if e == "test" || e == "live" || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, []string{"env:wake", terminus.SiteEnv(site, e)})
	}
	return out
}

// confirmAndRun opens the confirmation modal for steps and, once approved,
// runs them as a background job. If interactive is set it runs last with
// the real terminal (tea.ExecProcess) after the job succeeds.
func (m *Model) confirmAndRun(title, site, env string, steps [][]string, interactive []string, sctx safety.Context, warnings []string) tea.Cmd {
	all := steps
	if interactive != nil {
		all = append(append([][]string{}, steps...), interactive)
	}
	if d := m.policy().EvaluatePlan(all, sctx); d.Level == safety.Blocked {
		m.reportErr(title, fmt.Errorf("blocked: %s", strings.Join(d.Reasons, "; ")))
		return nil
	}
	return m.open(newConfirm(m, title, all, sctx, warnings, func(m *Model, a *safety.Approval) tea.Cmd {
		actx := safety.WithApproval(context.Background(), a)
		if len(steps) == 0 {
			return m.execInteractive(actx, title, site, interactive)
		}
		job, cmd := m.jobs.Start(actx, title, site, env, steps)
		deploy := ""
		for _, s := range steps {
			if a := terminus.Args(s); a.Command() == "env:deploy" && len(a.Positionals()) > 0 {
				deploy = a.Positionals()[0]
			}
		}
		switch {
		case deploy != "":
			// Watch it land: switch to the pipeline focused on the target.
			m.showPipeline, m.showOutput, m.showOutputOnJob = true, false, false
			if s, e, ok := terminus.SplitSiteEnv(deploy); ok && s == m.site() {
				cmd = tea.Batch(cmd, m.selectEnv(e))
			}
			cmd = tea.Batch(cmd, m.startAnim())
		case m.showOutputOnJob:
			m.showOutput, m.showOutputOnJob = true, false
		}
		cmd = tea.Batch(cmd, m.startAnim()) // show it moving if the pipeline is open
		if interactive != nil {
			m.afterJob[job.ID] = func(m *Model) tea.Cmd { return m.execInteractive(actx, title, site, interactive) }
		}
		m.setStatus(fmt.Sprintf("Started: %s", title), false)
		m.logOutput(theme.Bold.Render("▶ "+title), theme.MutedText.Render("  "+terminus.Args(steps[0]).String()))
		return tea.Batch(cmd, m.startSpinner())
	}))
}

func (m *Model) execInteractive(actx context.Context, title, site string, args []string) tea.Cmd {
	cmd, err := terminus.CommandFor(m.opts.Runner, actx, args...)
	if err != nil {
		m.reportErr(title, err)
		return nil
	}
	m.logOutput(theme.Bold.Render("▶ " + terminus.Args(args).String()))
	done := func(err error) tea.Msg { return execDoneMsg{title: title, site: site, err: err} }
	if m.opts.NoExecPause {
		return tea.ExecProcess(cmd, done)
	}
	return tea.Exec(&pausedExec{Cmd: cmd, title: terminus.Args(args).String()}, done)
}

// pausedExec runs a command with the terminal, then waits for Enter before
// lazyterminus redraws, so the command's output stays on screen to read.
type pausedExec struct {
	*exec.Cmd
	title string // shown while the command connects, which can take a while
	in    io.Reader
	out   io.Writer
}

func (p *pausedExec) SetStdin(r io.Reader)  { p.Stdin, p.in = r, r }
func (p *pausedExec) SetStdout(w io.Writer) { p.Stdout, p.out = w, w }
func (p *pausedExec) SetStderr(w io.Writer) { p.Stderr = w }

func (p *pausedExec) Run() error {
	in, out := p.in, p.out
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	if p.title != "" {
		fmt.Fprintf(out, "%s\n%s\n\n", theme.Bold.Render("▶ "+p.title), theme.MutedText.Render("Running on Pantheon; output appears when the command sends it…"))
	}
	err := p.Cmd.Run()
	discardTypeahead(in)
	status := theme.OK.Render(theme.SymSuccess + " Finished")
	if err != nil {
		status = theme.Fail.Render(theme.SymFailed + " " + err.Error())
	}
	fmt.Fprintf(out, "\n%s · %s ", status, theme.Bold.Render("press Enter to return to lazyterminus"))
	_, _ = bufio.NewReader(in).ReadString('\n')
	return err
}

// onJobMsg applies a job message and reacts to step changes and completion.
func (m *Model) onJobMsg(msg tea.Msg) tea.Cmd {
	cmd := m.jobs.Apply(msg)
	switch msg := msg.(type) {
	case jobs.StepMsg:
		a := terminus.Args(msg.Args)
		if a.Command() == "env:wake" {
			m.setStatus("Waking "+a.Positionals()[0]+" before continuing…", false)
		} else if msg.Step > 0 {
			m.setStatus("Running "+a.Command()+"…", false)
		}
	case jobs.LineMsg:
		m.logOutput("  " + msg.Line)
	case jobs.DoneMsg:
		j := m.jobs.Get(msg.ID)
		if j == nil {
			return cmd
		}
		cmds := []tea.Cmd{cmd}
		m.noteJobDone(j)
		if msg.Err != nil {
			m.reportErr(j.Title, msg.Err)
		} else {
			m.setStatus(theme.SymSuccess+" "+j.Title+" finished", false)
			m.logOutput(theme.OK.Render(theme.SymSuccess + " " + j.Title + " finished"))
			cmds = append(cmds, m.recheckAuth())
			if next := m.afterJob[j.ID]; next != nil {
				cmds = append(cmds, next(m))
			}
		}
		delete(m.afterJob, j.ID)
		m.client.InvalidateSite(j.Site)
		st := m.state(j.Site)
		st.inflight = map[string]bool{}
		cmds = append(cmds, m.ensureSite(j.Site, true))
		return tea.Batch(cmds...)
	}
	return cmd
}

// ---- individual actions -----------------------------------------------------

func (m *Model) runAction(action string) tea.Cmd {
	if action == ActUpstream {
		return m.actUpstream()
	}
	site, e, ok := m.target()
	if !ok {
		return nil
	}
	se := terminus.SiteEnv(site, e.ID)
	switch action {
	case ActDeploy:
		src := terminus.SourceEnv(e.ID)
		if src == "" {
			m.setStatus("Deploy is only available on test and live (dev and multidevs receive code from Git).", true)
			return nil
		}
		m.setStatus("Loading commits that would deploy from "+src+"…", false)
		c, repo := m.client, m.cfg.RepoFor(site)
		return func() tea.Msg {
			cx, cancel := ctx()
			defer cancel()
			log, err := c.CodeLog(cx, site, src, true)
			log = withFullMessages(repo, log)
			return deployPreviewMsg{site: site, env: e.ID, src: src, log: log, commits: terminus.DeployableCommits(log, src, e.ID), err: err}
		}

	case ActClearCache:
		return m.confirmAndRun("Clear cache on "+se, site, e.ID, [][]string{{"env:clear-cache", se}}, nil, safety.Context{}, nil)

	case ActWake:
		return m.confirmAndRun("Wake "+se, site, e.ID, [][]string{{"env:wake", se}}, nil, safety.Context{}, nil)

	case ActBackup:
		return m.open(&form{
			title: "Create backup · " + se,
			fields: []formField{
				selectField("element", "Element", []string{"all", "code", "files", "database"}, "all"),
				textField("keep", "Keep for (days)", "", "365 (Terminus default)", false, positiveInt),
			},
			preview: func(v formValues) ([][]string, error) {
				args := []string{"backup:create", se, "--element=" + v["element"]}
				if v["keep"] != "" {
					args = append(args, "--keep-for="+v["keep"])
				}
				return [][]string{args}, nil
			},
			submit: func(m *Model, v formValues, steps [][]string) tea.Cmd {
				return m.confirmAndRun("Backup "+se+" ("+v["element"]+")", site, e.ID, steps, nil, safety.Context{}, nil)
			},
		})

	case ActCloneContent:
		return m.actClone(site, e)

	case ActConnectionMode:
		if !terminus.IsMultidevName(e.ID) && e.ID != "dev" {
			m.setStatus("Connection mode can only be changed on dev and multidevs (Terminus excludes test and live).", true)
			return nil
		}
		m.setStatus("Checking for uncommitted SFTP changes…", false)
		return m.diffPreview(site, e.ID, "mode")

	case ActDrush:
		return m.open(&form{
			title: "Drush · " + se,
			intro: []string{theme.MutedText.Render("Runs with your terminal attached; press Enter afterwards to return to lazyterminus.")},
			fields: []formField{
				textField("cmd", "drush", "", "status", true, nil),
			},
			preview: func(v formValues) ([][]string, error) {
				words, err := splitWords(v["cmd"])
				if err != nil {
					return nil, err
				}
				if len(words) > 0 && words[0] == "drush" {
					words = words[1:]
				}
				steps := wakeSteps(site, e.ID)
				return append(steps, append([]string{"remote:drush", se, "--"}, words...)), nil
			},
			submit: func(m *Model, v formValues, steps [][]string) tea.Cmd {
				last := steps[len(steps)-1]
				return m.confirmAndRun("drush "+v["cmd"]+" on "+se, site, e.ID, steps[:len(steps)-1], last, safety.Context{}, nil)
			},
		})

	case ActCommit:
		if e.ConnectionMode != "sftp" {
			m.setStatus("Commit is only for environments in SFTP mode; "+e.ID+" is in "+e.ConnectionMode+" mode.", true)
			return nil
		}
		m.setStatus("Checking uncommitted changes…", false)
		return m.diffPreview(site, e.ID, "commit")

	case ActCreateMultidev:
		src := e.ID
		if terminus.IsMultidevName(src) {
			src = "dev"
		}
		return m.open(&form{
			title: "New multidev · " + site,
			fields: []formField{
				textField("name", "Name", "", "e.g. feature-x (≤11 chars)", true, validMultidevName(m.state(site))),
				selectField("from", "Clone from", []string{"dev", "test", "live"}, src),
				toggleField("nodb", "Skip database", false, "--no-db"),
				toggleField("nofiles", "Skip files", false, "--no-files"),
			},
			preview: func(v formValues) ([][]string, error) {
				args := []string{"multidev:create", terminus.SiteEnv(site, v["from"]), v["name"]}
				if v.on("nodb") {
					args = append(args, "--no-db")
				}
				if v.on("nofiles") {
					args = append(args, "--no-files")
				}
				return [][]string{args}, nil
			},
			submit: func(m *Model, v formValues, steps [][]string) tea.Cmd {
				return m.confirmAndRun("Create multidev "+v["name"], site, v["name"], steps, nil, safety.Context{}, nil)
			},
		})

	case ActDeleteMultidev:
		if !e.IsMultidev() {
			m.setStatus("Only multidevs can be deleted.", true)
			return nil
		}
		return m.open(&form{
			title: "Delete multidev · " + se,
			intro: []string{theme.Fail.Render("Deletes the " + e.ID + " environment, its database, and its files.")},
			fields: []formField{
				toggleField("branch", "Delete Git branch", false, "--delete-branch (also removes "+e.ID+" from the Pantheon repo)"),
			},
			preview: func(v formValues) ([][]string, error) {
				args := []string{"multidev:delete", se}
				if v.on("branch") {
					args = append(args, "--delete-branch")
				}
				return [][]string{append(args, "--yes")}, nil
			},
			submit: func(m *Model, v formValues, steps [][]string) tea.Cmd {
				return m.confirmAndRun("Delete multidev "+se, site, e.ID, steps, nil, safety.Context{}, nil)
			},
		})
	}
	return nil
}

func positiveInt(s string) error {
	if n, err := strconv.Atoi(s); err != nil || n < 1 {
		return errors.New("must be a whole number of days")
	}
	return nil
}

var multidevName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,10}$`)

func validMultidevName(st *siteState) func(string) error {
	reserved := map[string]bool{"dev": true, "test": true, "live": true, "settings": true, "team": true, "support": true,
		"multidev": true, "debug": true, "files": true, "tags": true, "releases": true}
	return func(s string) error {
		switch {
		case !multidevName.MatchString(s):
			return errors.New("use 1–11 lowercase letters, digits, or dashes, starting with a letter or digit")
		case reserved[s]:
			return fmt.Errorf("%q is a reserved name", s)
		case st.env(s) != nil:
			return fmt.Errorf("%q already exists", s)
		}
		return nil
	}
}

func (m *Model) diffPreview(site, env, purpose string) tea.Cmd {
	c := m.client
	return func() tea.Msg {
		cx, cancel := ctx()
		defer cancel()
		ds, err := c.Diffstat(cx, site, env, true)
		return diffPreviewMsg{site: site, env: env, purpose: purpose, entries: ds, err: err}
	}
}

func (m *Model) onDiffPreview(msg diffPreviewMsg) tea.Cmd {
	if msg.err != nil {
		m.reportErr("env:diffstat "+msg.site+"."+msg.env, msg.err)
		return nil
	}
	st := m.state(msg.site)
	st.diffstat[msg.env] = msg.entries
	e := st.env(msg.env)
	if e == nil {
		return nil
	}
	se := terminus.SiteEnv(msg.site, msg.env)
	switch msg.purpose {
	case "mode":
		to := "sftp"
		if e.ConnectionMode == "sftp" {
			to = "git"
		}
		sctx := safety.Context{UncommittedChanges: to == "git" && len(msg.entries) > 0}
		var warn []string
		if sctx.UncommittedChanges {
			warn = append(warn, fmt.Sprintf("%s has %d uncommitted file(s). Switching to Git DISCARDS them. Commit first with %s.",
				se, len(msg.entries), m.keys.Key(ActCommit)))
			for _, f := range msg.entries {
				warn = append(warn, "  "+f.Status+" "+f.File)
			}
		}
		return m.confirmAndRun(fmt.Sprintf("Switch %s to %s mode", se, to), msg.site, msg.env,
			[][]string{{"connection:set", se, to, "--yes"}}, nil, sctx, warn)
	case "commit":
		if len(msg.entries) == 0 {
			m.setStatus("Nothing to commit on "+se+".", false)
			return nil
		}
		intro := []string{fmt.Sprintf("Commit %d changed file(s) on %s:", len(msg.entries), se)}
		for _, f := range msg.entries {
			intro = append(intro, fmt.Sprintf("  %s %s %s", theme.SFTP.Render(panels.Fit(f.Status, 2)), f.File,
				theme.MutedText.Render(fmt.Sprintf("+%d −%d", f.Additions, f.Deletions))))
		}
		return m.open(&form{
			title:  "Commit SFTP changes · " + se,
			intro:  intro,
			fields: []formField{textField("msg", "Message", "", "describe the change", true, nil)},
			preview: func(v formValues) ([][]string, error) {
				return [][]string{{"env:commit", se, "--message=" + v["msg"], "--yes"}}, nil
			},
			submit: func(m *Model, v formValues, steps [][]string) tea.Cmd {
				return m.confirmAndRun("Commit on "+se, msg.site, msg.env, steps, nil, safety.Context{}, nil)
			},
		})
	}
	return nil
}

// onDeployPreview shows the deploy form once the commits are known.
// Pantheon deploys promote everything; there is no per-commit selection.
func (m *Model) onDeployPreview(msg deployPreviewMsg) tea.Cmd {
	if msg.err != nil {
		m.reportErr("env:code-log "+msg.site+"."+msg.src, msg.err)
		return nil
	}
	m.state(msg.site).codeLogs[msg.src] = msg.log
	se := terminus.SiteEnv(msg.site, msg.env)
	m.setStatus(fmt.Sprintf("%d commit(s) ready to deploy to %s", len(msg.commits), se), false)
	if len(msg.commits) == 0 {
		m.setStatus("Nothing to deploy: "+msg.env+" already has every commit from "+msg.src+".", false)
		return m.open(&infoModal{title: "Deploy · " + se, lines: []string{
			theme.OK.Render(theme.SymSuccess) + " Nothing to deploy.",
			"",
			msg.env + " already has every commit from " + msg.src + ".",
		}})
	}
	intro := []string{
		theme.Warn.Render(fmt.Sprintf("Deploying promotes ALL %d commit(s) from %s to %s.", len(msg.commits), msg.src, msg.env)),
		theme.MutedText.Render("Pantheon has no per-commit deploys. Quicksilver hooks run after deploy (drush cim)."),
		"",
	}
	for _, c := range msg.commits {
		intro = append(intro, fmt.Sprintf("  %s %s %s", theme.Warn.Render(short(c.Hash)), c.Subject(), theme.MutedText.Render("— "+c.Author)))
	}
	dd := m.cfg.DeployDefaults
	fields := []formField{
		textField("note", "Deploy note", "", "what is going out and why", true, nil),
		toggleField("cc", "Clear cache", dd.ClearCache, "--cc"),
		toggleField("updatedb", "Update DB", dd.UpdateDB, "--updatedb (runs update.php)"),
	}
	if msg.env == "test" {
		fields = append(fields, toggleField("sync", "Sync content", dd.SyncContent, "--sync-content (clone database and files from live into test first)"))
	}
	return m.open(&form{
		title:  "Deploy " + msg.src + " → " + msg.env + " · " + msg.site,
		intro:  intro,
		fields: fields,
		preview: func(v formValues) ([][]string, error) {
			args := []string{"env:deploy", se, "--note=" + v["note"]}
			if v.on("cc") {
				args = append(args, "--cc")
			}
			if v.on("updatedb") {
				args = append(args, "--updatedb")
			}
			if msg.env == "test" && v.on("sync") {
				args = append(args, "--sync-content")
			}
			return [][]string{append(args, "--yes")}, nil
		},
		submit: func(m *Model, v formValues, steps [][]string) tea.Cmd {
			return m.confirmAndRun(fmt.Sprintf("Deploy %d commit(s) to %s", len(msg.commits), se), msg.site, msg.env, steps, nil, safety.Context{}, nil)
		},
	})
}

func short(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

func (m *Model) actClone(site string, e *terminus.Env) tea.Cmd {
	st := m.state(site)
	var sources, targets []string
	for _, x := range st.envs {
		sources = append(sources, x.ID)
		if x.ID != "live" { // cloning into live is never offered
			targets = append(targets, x.ID)
		}
	}
	source, tgt := "live", e.ID
	if e.ID == "live" {
		source, tgt = "live", "dev"
	}
	return m.open(&form{
		title: "Clone content · " + site,
		intro: []string{theme.MutedText.Render("Overwrites the target's database and/or files. Cloning into live is not offered.")},
		fields: []formField{
			selectField("from", "From", sources, source),
			selectField("to", "To", targets, tgt),
			toggleField("db", "Database", true, ""),
			toggleField("files", "Files", true, ""),
			toggleField("cc", "Clear cache", true, "--cc"),
			toggleField("updatedb", "Update DB", false, "--updatedb"),
		},
		preview: func(v formValues) ([][]string, error) {
			if v["from"] == v["to"] {
				return nil, errors.New("source and target must differ")
			}
			if !v.on("db") && !v.on("files") {
				return nil, errors.New("choose database, files, or both")
			}
			args := []string{"env:clone-content", terminus.SiteEnv(site, v["from"]), v["to"]}
			switch {
			case v.on("db") && !v.on("files"):
				args = append(args, "--db-only")
			case v.on("files") && !v.on("db"):
				args = append(args, "--files-only")
			}
			if v.on("cc") {
				args = append(args, "--cc")
			}
			if v.on("updatedb") {
				args = append(args, "--updatedb")
			}
			return append(wakeSteps(site, v["from"], v["to"]), append(args, "--yes")), nil
		},
		submit: func(m *Model, v formValues, steps [][]string) tea.Cmd {
			return m.confirmAndRun(fmt.Sprintf("Clone %s → %s", v["from"], v["to"]), site, v["to"], steps, nil, safety.Context{}, nil)
		},
	})
}

// actUpstream shows upstream updates (read-only) in the Output panel.
func (m *Model) actUpstream() tea.Cmd {
	site := m.site()
	if site == "" {
		return nil
	}
	env := m.selectedEnv()
	if env == "" || env == "test" || env == "live" {
		env = "dev" // upstream:updates:list only works on development environments
	}
	m.setStatus("Checking upstream updates for "+site+"."+env+"…", false)
	r := m.opts.Runner
	c := m.client
	return func() tea.Msg {
		cx, cancel := ctx()
		defer cancel()
		se := terminus.SiteEnv(site, env)
		out, _, serr := r.Run(cx, "upstream:updates:status", se)
		status := strings.TrimSpace(string(out))
		if serr != nil {
			status = "unknown (" + serr.Error() + ")"
		}
		ups, err := c.UpstreamUpdates(cx, site, env, true)
		return upstreamMsg{site: site, env: env, status: status, updates: ups, err: err}
	}
}

func (m *Model) onUpstream(msg upstreamMsg) {
	se := terminus.SiteEnv(msg.site, msg.env)
	m.logOutput(theme.Bold.Render("Upstream updates for "+se), "  status: "+msg.status)
	if msg.err != nil {
		m.reportErr("upstream:updates:list "+se, msg.err)
	} else if len(msg.updates) == 0 {
		m.logOutput("  " + theme.OK.Render(theme.SymSuccess+" no updates available"))
	} else {
		rows := [][]string{{"COMMIT", "DATE", "AUTHOR", "MESSAGE"}}
		for _, u := range msg.updates {
			rows = append(rows, []string{short(u.Hash), u.Datetime, u.Author, u.Message})
		}
		m.logOutput(renderTable(rows)...)
	}
	m.setStatus(fmt.Sprintf("Upstream %s: %d update(s)", se, len(msg.updates)), false)
	m.showOutput = true
}

// splitWords splits a command line like a POSIX shell would for simple
// quoting ('…', "…", and backslash escapes).
func splitWords(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	var quote rune
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped, inWord = true, true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	if escaped {
		return nil, errors.New("trailing backslash")
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}
