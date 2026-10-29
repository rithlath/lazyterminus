package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/rithlath/lazyterminus/internal/catalog"
	"github.com/rithlath/lazyterminus/internal/config"
	"github.com/rithlath/lazyterminus/internal/safety"
	"github.com/rithlath/lazyterminus/internal/terminus"
	"github.com/rithlath/lazyterminus/internal/ui/panels"
	"github.com/rithlath/lazyterminus/internal/ui/theme"
)

type (
	catalogMsg struct {
		cat    *catalog.Catalog
		cached bool
		err    error
	}
	paletteResultMsg struct {
		title string
		lines []string
		err   error
	}
)

// catalogDir keeps mock catalogs apart from real ones.
func (m *Model) catalogDir() string {
	if m.opts.CatalogDir != "" {
		return m.opts.CatalogDir
	}
	if m.opts.Mock {
		return config.CacheDir() + "/mock"
	}
	return config.CacheDir()
}

func loadCatalog(r terminus.Runner, dir string, force bool) tea.Cmd {
	return func() tea.Msg {
		cx, cancel := ctx()
		defer cancel()
		cat, cached, err := catalog.Load(cx, r, dir, force)
		return catalogMsg{cat: cat, cached: cached, err: err}
	}
}

func (m *Model) onCatalog(msg catalogMsg) tea.Cmd {
	m.catalogLoading = false
	if msg.err != nil {
		m.reportErr("command catalog", msg.err)
		return nil
	}
	m.catalog = msg.cat
	terminus.SetAliasResolver(msg.cat.Resolve)
	src := "terminus list"
	if msg.cached {
		src = "cache"
	}
	m.logOutput(fmt.Sprintf("Loaded %d commands (Terminus %s) from %s", len(msg.cat.Commands), msg.cat.Version, src))
	if m.paletteWanted {
		m.paletteWanted = false
		return m.openPalette()
	}
	return nil
}

// paletteScope decides which commands are listed first and what fills
// their first argument.
func (m *Model) paletteScope() (catalog.Scope, string) {
	site, env := m.site(), m.selectedEnv()
	switch {
	case site == "":
		return catalog.ScopeNone, ""
	case (m.focus == panelEnvs || m.focus == panelDetail || m.showPipeline) && env != "":
		return catalog.ScopeEnv, terminus.SiteEnv(site, env)
	default:
		return catalog.ScopeSite, site
	}
}

func (m *Model) openPalette() tea.Cmd {
	if m.catalog == nil {
		m.paletteWanted = true
		m.setStatus("Loading command catalog…", false)
		if !m.catalogLoading {
			m.catalogLoading = true
			return tea.Batch(loadCatalog(m.opts.Runner, m.catalogDir(), false), m.startSpinner())
		}
		return nil
	}
	scope, fill := m.paletteScope()
	p := &paletteModal{cat: m.catalog, scope: scope, fill: fill}
	p.input = textinput.New()
	p.input.Prompt = ": "
	p.input.Placeholder = "search commands"
	p.refresh()
	m.modal = p
	return p.input.Focus()
}

type paletteModal struct {
	cat     *catalog.Catalog
	scope   catalog.Scope
	fill    string
	input   textinput.Model
	matches []catalog.Match
	cursor  int
	offset  int
}

const paletteRows = 14

func (p *paletteModal) refresh() {
	p.matches = p.cat.Search(p.input.Value(), p.scope)
	p.cursor, p.offset = 0, 0
}

func (p *paletteModal) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(msg)
		return false, cmd
	}
	switch key.String() {
	case "esc":
		return true, nil
	case "up", "ctrl+p", "ctrl+k":
		p.cursor = max(p.cursor-1, 0)
		return false, nil
	case "down", "ctrl+n", "ctrl+j", "tab":
		p.cursor = min(p.cursor+1, max(len(p.matches)-1, 0))
		return false, nil
	case "enter":
		if len(p.matches) == 0 {
			return false, nil
		}
		match := p.matches[p.cursor]
		fill := ""
		if match.Scoped {
			fill = p.fill
		}
		return true, m.open(m.commandForm(match.Command, fill))
	}
	before := p.input.Value()
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	if p.input.Value() != before {
		p.refresh()
	}
	return false, cmd
}

// click chooses the command on line (lines 0-1 are the input and context).
func (p *paletteModal) click(m *Model, line int, _ bool) (bool, tea.Cmd) {
	i := p.offset + line - 2
	if line < 2 || i >= len(p.matches) {
		return false, nil
	}
	p.cursor = i
	return p.update(m, keyMsg("enter"))
}

func (p *paletteModal) view(width int) (string, []string, string) {
	p.input.SetWidth(max(width-4, 10))
	lines := []string{p.input.View(), ""}
	if p.fill != "" {
		lines[1] = theme.MutedText.Render("Context: " + p.fill + " (filled into matching commands)")
	}
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+paletteRows {
		p.offset = p.cursor - paletteRows + 1
	}
	nameW := 0
	for _, mt := range p.matches {
		nameW = max(nameW, len(mt.Command.Name))
	}
	nameW = min(nameW, 30)
	end := min(p.offset+paletteRows, len(p.matches))
	for i := p.offset; i < end; i++ {
		mt := p.matches[i]
		tag := ""
		if mt.Scoped {
			tag = theme.Key.Render("◆ ")
		} else {
			tag = "  "
		}
		text := tag + panels.Fit(mt.Command.Name, nameW) + "  " + theme.MutedText.Render(mt.Command.Description)
		lines = append(lines, panels.Row(text, width, i == p.cursor, true))
	}
	if len(p.matches) == 0 {
		lines = append(lines, theme.MutedText.Render("No matching commands."))
	}
	footer := fmt.Sprintf(" %d/%d · ◆ fills %s · enter choose · esc close ", min(p.cursor+1, len(p.matches)), len(p.matches), orDash(p.fill))
	return "Command palette", lines, footer
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// commandForm builds an argument form for cmd from the catalog. fill, if
// set, is the value for the first argument.
func (m *Model) commandForm(cmd catalog.Command, fill string) *form {
	var fields []formField
	intro := []string{theme.Bold.Render(cmd.Name) + "  " + theme.MutedText.Render(cmd.Description)}
	if fill != "" && len(cmd.Args) > 0 {
		intro = append(intro, theme.MutedText.Render(cmd.Args[0].Name+" = ")+theme.Bold.Render(fill))
	}
	for i, a := range cmd.Args {
		if i == 0 && fill != "" {
			continue
		}
		label := a.Name
		placeholder := a.Description
		if a.IsArray {
			placeholder = "space-separated; " + placeholder
		}
		fields = append(fields, textField("arg:"+a.Name, label, "", placeholder, a.Required, nil))
	}
	json := cmd.SupportsJSON()
	for _, o := range cmd.Options {
		if json && (o.Name == "format" || o.Name == "field") {
			continue
		}
		key := "opt:" + o.Name
		switch {
		case !o.AcceptValue:
			fields = append(fields, toggleField(key, "--"+o.Name, false, o.Description))
		case len(o.Choices) > 0:
			fields = append(fields, selectField(key, "--"+o.Name, append([]string{"(default)"}, o.Choices...), "(default)"))
		default:
			ph := o.Description
			if o.Default != "" {
				ph = "default " + o.Default + " · " + ph
			}
			fields = append(fields, textField(key, "--"+o.Name, "", ph, false, nil))
		}
	}
	if len(fields) == 0 {
		intro = append(intro, "", theme.MutedText.Render("No arguments needed."))
	}
	return &form{
		title:  "Run " + cmd.Name,
		intro:  intro,
		fields: fields,
		preview: func(v formValues) ([][]string, error) {
			argv, err := buildArgv(cmd, fill, v)
			if err != nil {
				return nil, err
			}
			return [][]string{argv}, nil
		},
		submit: func(m *Model, v formValues, steps [][]string) tea.Cmd {
			return m.runPaletteCommand(cmd, steps[0])
		},
	}
}

// buildArgv assembles a command line: positionals, options, then any array
// argument after "--" so its values aren't parsed as Terminus options.
// Mutating, non-interactive commands get --yes because Terminus answers
// "no" to its own prompts under --no-interaction; lazyterminus has already
// confirmed by then.
func buildArgv(cmd catalog.Command, fill string, v formValues) ([]string, error) {
	argv := []string{cmd.Name}
	var rest []string
	missing := ""
	for i, a := range cmd.Args {
		val := v["arg:"+a.Name]
		if i == 0 && fill != "" {
			val = fill
		}
		if val == "" {
			if a.Required {
				return nil, fmt.Errorf("%s is required", a.Name)
			}
			if missing == "" {
				missing = a.Name
			}
			continue
		}
		if missing != "" {
			return nil, fmt.Errorf("fill %s before %s", missing, a.Name)
		}
		if a.IsArray {
			words, err := splitWords(val)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", a.Name, err)
			}
			rest = append(rest, words...)
			continue
		}
		argv = append(argv, val)
	}
	for _, o := range cmd.Options {
		val, ok := v["opt:"+o.Name]
		switch {
		case !ok || val == "" || val == "(default)":
		case !o.AcceptValue:
			argv = append(argv, "--"+o.Name)
		default:
			argv = append(argv, "--"+o.Name+"="+val)
		}
	}
	if cmd.SupportsJSON() {
		argv = append(argv, "--format=json")
	}
	if terminus.IsMutating(argv) && !cmd.Interactive() {
		argv = append(argv, "--yes")
	}
	if len(rest) > 0 {
		argv = append(append(argv, "--"), rest...)
	}
	return argv, nil
}

// runPaletteCommand routes a palette command through the same safety layer
// and job machinery as keybound actions.
func (m *Model) runPaletteCommand(cmd catalog.Command, argv []string) tea.Cmd {
	site, env := "", ""
	if p := terminus.Args(argv).Positionals(); len(p) > 0 {
		site, env, _ = terminus.SplitSiteEnv(p[0])
	}
	title := cmd.Name
	if site != "" {
		title += " " + terminus.Args(argv).Positionals()[0]
	}
	mutating := terminus.IsMutating(argv)
	switch c := terminus.Args(argv).Command(); {
	case (c == "remote:drush" || c == "remote:wp") && env != "":
		// Like the x key: a background job, so you can keep working.
		if k := slices.Index(argv, "--"); k >= 0 {
			title = strings.TrimPrefix(c, "remote:") + " " + strings.Join(argv[k+1:], " ") + " on " + terminus.SiteEnv(site, env)
		}
		return m.confirmAndRun(title, site, env, wakeSteps(site, env), &final{args: argv, detached: true}, safety.Context{}, nil)
	case cmd.Interactive() && mutating:
		var steps [][]string
		if env != "" {
			steps = wakeSteps(site, env)
		}
		return m.confirmAndRun(title, site, env, steps, &final{args: argv}, safety.Context{}, nil)
	case cmd.Interactive():
		return m.execInteractive(context.Background(), title, site, argv)
	case mutating:
		m.showOutputOnJob = true
		return m.confirmAndRun(title, site, env, [][]string{argv}, nil, safety.Context{}, nil)
	case cmd.SupportsJSON():
		m.setStatus("Running "+terminus.Args(argv).String()+"…", false)
		r := m.opts.Runner
		return tea.Batch(m.startSpinner(), func() tea.Msg {
			cx, cancel := ctx()
			defer cancel()
			out, stderr, err := r.Run(cx, argv...)
			if err != nil {
				return paletteResultMsg{title: terminus.Args(argv).String(), err: err}
			}
			lines, terr := jsonTable(out)
			if terr != nil {
				lines = strings.Split(strings.TrimRight(string(out), "\n"), "\n")
			}
			if len(lines) == 0 {
				note := "(no rows)"
				if msg := strings.TrimSpace(string(stderr)); msg != "" {
					note += " " + terminus.ErrorMessage(stderr)
				}
				lines = []string{theme.MutedText.Render(note)}
			}
			return paletteResultMsg{title: terminus.Args(argv).String(), lines: lines}
		})
	default:
		// Read-only, plain output: stream it.
		_, jcmd := m.jobs.Start(context.Background(), title, site, env, [][]string{argv})
		m.showOutput = true
		m.logOutput(theme.Bold.Render("▶ " + terminus.Args(argv).String()))
		return tea.Batch(jcmd, m.startSpinner())
	}
}

func (m *Model) onPaletteResult(msg paletteResultMsg) {
	m.logOutput(theme.Bold.Render("▶ " + msg.title))
	if msg.err != nil {
		m.reportErr(msg.title, msg.err)
	} else {
		m.logOutput(msg.lines...)
		m.setStatus(theme.SymSuccess+" "+msg.title, false)
	}
	m.showOutput = true
}

// ---- JSON → table ------------------------------------------------------------

var epochKeys = map[string]bool{"created": true, "date": true, "expiry": true, "started_at": true,
	"finished_at": true, "last_frozen_at": true, "timestamp": true}

// jsonTable renders Terminus JSON output as table lines: a list of objects
// (keyed object or array) becomes rows; a single object becomes key/value
// pairs; a list of scalars becomes one column.
func jsonTable(data []byte) ([]string, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "[]" || string(data) == "null" {
		return nil, nil
	}
	if !json.Valid(data) {
		return nil, errors.New("not JSON")
	}
	rows, err := terminus.DecodeRows[json.RawMessage](data)
	if err != nil {
		return nil, err
	}
	allObjects := len(rows) > 0
	for _, r := range rows {
		if t := bytes.TrimSpace(r.Row); len(t) == 0 || t[0] != '{' {
			allObjects = false
		}
	}
	switch {
	case allObjects:
		var cols []string
		seen := map[string]bool{}
		var parsed []map[string]json.RawMessage
		for _, r := range rows {
			keys, vals, err := orderedObject(r.Row)
			if err != nil {
				return nil, err
			}
			for _, k := range keys {
				if !seen[k] {
					seen[k] = true
					cols = append(cols, k)
				}
			}
			parsed = append(parsed, vals)
		}
		table := [][]string{upper(cols)}
		for _, vals := range parsed {
			row := make([]string, len(cols))
			for i, c := range cols {
				row[i] = cell(c, vals[c])
			}
			table = append(table, row)
		}
		return renderTable(table), nil
	case data[0] == '{':
		table := [][]string{{"FIELD", "VALUE"}}
		for _, r := range rows {
			table = append(table, []string{r.Key, cell(r.Key, r.Row)})
		}
		return renderTable(table), nil
	default:
		table := [][]string{{"VALUE"}}
		for _, r := range rows {
			table = append(table, []string{cell("", r.Row)})
		}
		return renderTable(table), nil
	}
}

func upper(s []string) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = strings.ToUpper(x)
	}
	return out
}

func orderedObject(raw json.RawMessage) ([]string, map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil {
		return nil, nil, err
	}
	vals := map[string]json.RawMessage{}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		k, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, nil, err
		}
		keys = append(keys, k)
		vals[k] = v
	}
	return keys, vals, nil
}

func cell(key string, raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "—"
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if epochKeys[key] && x > 1e9 {
			return terminus.Epoch(x).Time().Local().Format(time.DateTime)
		}
		return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%f", x), "0"), ".")
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return string(raw)
}
