// Package catalog builds the command palette's catalog from
// `terminus list --format=json`, which includes installed plugins, and caches
// it on disk keyed by the Terminus version.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/sahilm/fuzzy"

	"github.com/rithlath/lazyterminus/internal/terminus"
)

// Arg is a positional argument.
type Arg struct {
	Name        string `json:"name"`
	Required    bool   `json:"is_required"`
	IsArray     bool   `json:"is_array"`
	Description string `json:"description"`
}

// Option is a --option.
type Option struct {
	Name          string   // without leading dashes
	AcceptValue   bool     // takes a value
	ValueRequired bool     // value is mandatory when the option is given
	Description   string   // help text
	Default       string   // rendered default ("" if none)
	Choices       []string // from a leading "[a|b|c]" in the description
}

// Command is one Terminus command.
type Command struct {
	Name        string
	Description string
	Usage       string
	Aliases     []string
	Hidden      bool
	Args        []Arg
	Options     []Option
}

// Scope says what context a command's first argument takes.
type Scope int

// Command scopes.
const (
	ScopeNone Scope = iota
	ScopeSite       // first argument is a site name
	ScopeEnv        // first argument is site.env
)

// Scope returns what the command's first argument expects.
func (c Command) Scope() Scope {
	if len(c.Args) == 0 {
		return ScopeNone
	}
	switch c.Args[0].Name {
	case "site_env", "siteenv", "site_env_id":
		return ScopeEnv
	case "site", "site_id", "site_name":
		return ScopeSite
	}
	return ScopeNone
}

// SupportsJSON reports whether the command accepts --format=json.
func (c Command) SupportsJSON() bool {
	for _, o := range c.Options {
		if o.Name == "format" {
			return strings.Contains(o.Description, "json")
		}
	}
	return false
}

// Interactive reports whether the command needs the real terminal.
func (c Command) Interactive() bool {
	switch c.Name {
	case "remote:drush", "remote:wp", "self:console":
		return true
	}
	return false
}

// globalOptions are Symfony/Terminus options every command takes; the
// palette doesn't offer them.
var globalOptions = map[string]bool{
	"help": true, "quiet": true, "verbose": true, "version": true, "ansi": true, "no-ansi": true,
	"no-interaction": true, "define": true, "yes": true,
}

// Catalog is the parsed command list.
type Catalog struct {
	Version  string
	Commands []Command
	aliases  map[string]string
}

// Get returns a command by name or alias.
func (c *Catalog) Get(name string) (Command, bool) {
	if n, ok := c.aliases[name]; ok {
		name = n
	}
	for _, cmd := range c.Commands {
		if cmd.Name == name {
			return cmd, true
		}
	}
	return Command{}, false
}

// Resolve maps an alias to its canonical command name.
func (c *Catalog) Resolve(name string) (string, bool) {
	n, ok := c.aliases[name]
	return n, ok
}

type rawCommand struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Usage       []string        `json:"usage"`
	Hidden      bool            `json:"hidden"`
	Definition  json.RawMessage `json:"definition"`
}

type rawOption struct {
	Name          string `json:"name"`
	AcceptValue   bool   `json:"accept_value"`
	ValueRequired bool   `json:"is_value_required"`
	Description   string `json:"description"`
	Default       any    `json:"default"`
}

var choicesRe = regexp.MustCompile(`^\[([A-Za-z0-9_.-]+(?:\|[A-Za-z0-9_.-]+)+)\]\s*`)

// Parse parses `terminus list --format=json` output.
func Parse(data []byte) (*Catalog, error) {
	var doc struct {
		Application struct {
			Version string `json:"version"`
		} `json:"application"`
		Commands []rawCommand `json:"commands"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	if len(doc.Commands) == 0 {
		return nil, errors.New("catalog: no commands in terminus list output")
	}
	cat := &Catalog{Version: doc.Application.Version, aliases: map[string]string{}}
	for _, rc := range doc.Commands {
		var def struct {
			Arguments json.RawMessage      `json:"arguments"`
			Options   map[string]rawOption `json:"options"`
		}
		if err := json.Unmarshal(rc.Definition, &def); err != nil {
			return nil, fmt.Errorf("catalog %s: %w", rc.Name, err)
		}
		cmd := Command{Name: rc.Name, Description: rc.Description, Hidden: rc.Hidden}
		if len(rc.Usage) > 0 {
			cmd.Usage = rc.Usage[0]
		}
		// Aliases are trailing single-word usage entries.
		for _, u := range rc.Usage[min(1, len(rc.Usage)):] {
			if u != "" && !strings.ContainsAny(u, " <[") && u != rc.Name {
				cmd.Aliases = append(cmd.Aliases, u)
				cat.aliases[u] = rc.Name
			}
		}
		args, err := decodeArgs(def.Arguments)
		if err != nil {
			return nil, fmt.Errorf("catalog %s: %w", rc.Name, err)
		}
		cmd.Args = args
		for key, o := range def.Options {
			name := strings.TrimPrefix(o.Name, "--")
			if name == "" {
				name = key
			}
			if globalOptions[name] {
				continue
			}
			opt := Option{Name: name, AcceptValue: o.AcceptValue, ValueRequired: o.ValueRequired, Description: o.Description}
			switch v := o.Default.(type) {
			case string:
				opt.Default = v
			case float64:
				opt.Default = fmt.Sprint(v)
			}
			if m := choicesRe.FindStringSubmatch(o.Description); m != nil {
				opt.Choices = strings.Split(m[1], "|")
			}
			cmd.Options = append(cmd.Options, opt)
		}
		sort.Slice(cmd.Options, func(i, j int) bool { return cmd.Options[i].Name < cmd.Options[j].Name })
		cat.Commands = append(cat.Commands, cmd)
	}
	sort.Slice(cat.Commands, func(i, j int) bool { return cat.Commands[i].Name < cat.Commands[j].Name })
	return cat, nil
}

// decodeArgs handles both an ordered object of arguments and the `[]`
// Terminus emits when there are none.
func decodeArgs(raw json.RawMessage) ([]Arg, error) {
	if len(raw) == 0 || raw[0] == '[' {
		return nil, nil
	}
	rows, err := terminus.DecodeRows[Arg](raw)
	if err != nil {
		return nil, err
	}
	out := make([]Arg, len(rows))
	for i, r := range rows {
		out[i] = r.Row
		if out[i].Name == "" {
			out[i].Name = r.Key
		}
	}
	return out, nil
}

// ---- loading and caching -----------------------------------------------------

type cacheFile struct {
	Version string          `json:"version"`
	List    json.RawMessage `json:"list"`
}

// Load returns the catalog, from cacheDir/catalog.json if it was built by
// the same Terminus version, otherwise from `terminus list` (and refreshes
// the cache). force skips the cache.
func Load(ctx context.Context, r terminus.Runner, cacheDir string, force bool) (*Catalog, bool, error) {
	out, _, err := r.Run(ctx, "--version")
	if err != nil {
		return nil, false, fmt.Errorf("terminus --version: %w", err)
	}
	version := strings.TrimPrefix(strings.TrimSpace(string(out)), "Terminus ")
	path := filepath.Join(cacheDir, "catalog.json")
	if !force {
		if b, err := os.ReadFile(path); err == nil {
			var cf cacheFile
			if json.Unmarshal(b, &cf) == nil && cf.Version == version {
				if cat, err := Parse(cf.List); err == nil {
					return cat, true, nil
				}
			}
		}
	}
	list, _, err := r.Run(ctx, "list", "--format=json")
	if err != nil {
		return nil, false, fmt.Errorf("terminus list: %w", err)
	}
	cat, err := Parse(list)
	if err != nil {
		return nil, false, err
	}
	if b, err := json.Marshal(cacheFile{Version: version, List: list}); err == nil {
		if os.MkdirAll(cacheDir, 0o755) == nil {
			_ = os.WriteFile(path, b, 0o644) // a failed cache write only costs a reload
		}
	}
	return cat, false, nil
}

// ---- search ---------------------------------------------------------------------

// Match is a search result.
type Match struct {
	Command Command
	Scoped  bool // first argument will be filled from the current selection
	Indexes []int
}

// Search ranks commands for query. Exact name or alias matches come first,
// then a whole ":"-segment match (so "cc" finds env:cc), then substrings,
// then fuzzy matches on names, then words found in descriptions. A
// multi-word query requires every word. Commands matching scope get a
// bonus, and shorter names win ties.
func (c *Catalog) Search(query string, scope Scope) []Match {
	var cmds []Command
	for _, cmd := range c.Commands {
		if !cmd.Hidden {
			cmds = append(cmds, cmd)
		}
	}
	scoped := func(cmd Command) bool { return scope != ScopeNone && cmd.Scope() == scope }
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		out := make([]Match, 0, len(cmds))
		for _, cmd := range cmds {
			out = append(out, Match{Command: cmd, Scoped: scoped(cmd)})
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Scoped && !out[j].Scoped })
		return out
	}
	words := strings.Fields(query)
	// Fuzzy scores are unbounded, so only their rank is used (1000–1999).
	fuzzyScore := map[int]fuzzy.Match{}
	fuzzyRank := map[int]int{}
	if len(words) == 1 {
		names := make([]string, len(cmds))
		for i, cmd := range cmds {
			names[i] = cmd.Name
		}
		for rank, res := range fuzzy.Find(query, names) {
			fuzzyScore[res.Index] = res
			fuzzyRank[res.Index] = 1999 - min(rank, 999)
		}
	}
	type scored struct {
		m     Match
		score int
	}
	var all []scored
	for i, cmd := range cmds {
		keys := append([]string{cmd.Name}, cmd.Aliases...)
		text := strings.ToLower(strings.Join(keys, " ") + " " + cmd.Description)
		score, ok := 0, true
		for _, w := range words {
			best := 0
			for _, k := range keys {
				switch {
				case k == w || k == query:
					best = max(best, 100000)
				case slices.Contains(strings.FieldsFunc(k, func(r rune) bool { return r == ':' }), w):
					best = max(best, 5000)
				case strings.Contains(k, w):
					best = max(best, 3000)
				}
			}
			if best == 0 {
				if r, ok := fuzzyRank[i]; ok {
					best = r
				} else if strings.Contains(text, w) {
					best = 100
				}
			}
			if best == 0 {
				ok = false
				break
			}
			score += best
		}
		if !ok {
			continue
		}
		m := Match{Command: cmd, Scoped: scoped(cmd)}
		if fm, ok := fuzzyScore[i]; ok {
			m.Indexes = fm.MatchedIndexes
		}
		if m.Scoped {
			score += 500
		}
		all = append(all, scored{m, score})
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		if li, lj := len(all[i].m.Command.Name), len(all[j].m.Command.Name); li != lj {
			return li < lj
		}
		return all[i].m.Command.Name < all[j].m.Command.Name
	})
	out := make([]Match, len(all))
	for i, s := range all {
		out[i] = s.m
	}
	return out
}
