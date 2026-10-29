package ui

import (
	"fmt"
	"sort"

	"charm.land/bubbles/v2/key"
)

// Action names are the keys used in the config file's keymap section.
const (
	ActQuit           = "quit"
	ActHelp           = "help"
	ActPalette        = "palette"
	ActRefresh        = "refresh"
	ActNextPanel      = "next_panel"
	ActPrevPanel      = "prev_panel"
	ActPanel1         = "panel_sites"
	ActPanel2         = "panel_envs"
	ActPanel3         = "panel_detail"
	ActPanel4         = "panel_workflows"
	ActUp             = "up"
	ActDown           = "down"
	ActSelect         = "select"
	ActLazygit        = "lazygit"
	ActOutput         = "output"
	ActPipeline       = "pipeline"
	ActHistory        = "history"
	ActDeploy         = "deploy"
	ActClearCache     = "clear_cache"
	ActBackup         = "backup"
	ActCloneContent   = "clone_content"
	ActConnectionMode = "connection_mode"
	ActWake           = "wake"
	ActDrush          = "drush"
	ActCommit         = "commit"
	ActCreateMultidev = "create_multidev"
	ActDeleteMultidev = "delete_multidev"
	ActUpstream       = "upstream_updates"
	ActOpenURL        = "open_url"
	ActCopyURL        = "copy_url"
)

type binding struct {
	action string
	keys   []string
	help   string
	group  string
}

var defaultBindings = []binding{
	{ActNextPanel, []string{"tab"}, "next panel", "Global"},
	{ActPrevPanel, []string{"shift+tab"}, "previous panel", "Global"},
	{ActPanel1, []string{"1"}, "sites", "Global"},
	{ActPanel2, []string{"2"}, "environments", "Global"},
	{ActPanel3, []string{"3"}, "detail", "Global"},
	{ActPanel4, []string{"4"}, "workflows", "Global"},
	{ActUp, []string{"k", "up"}, "up", "Global"},
	{ActDown, []string{"j", "down"}, "down", "Global"},
	{ActSelect, []string{"enter"}, "open / actions menu", "Global"},
	{ActRefresh, []string{"r"}, "refresh panel", "Global"},
	{ActPalette, []string{":"}, "command palette", "Global"},
	{ActLazygit, []string{"g"}, "lazygit", "Global"},
	{ActPipeline, []string{"p"}, "pipeline view", "Global"},
	{ActOutput, []string{"O"}, "output", "Global"},
	{ActHistory, []string{"h"}, "command history", "Global"},
	{ActHelp, []string{"?"}, "help", "Global"},
	{ActQuit, []string{"q", "ctrl+c"}, "quit", "Global"},

	{ActDeploy, []string{"d"}, "deploy here", "Environment"},
	{ActClearCache, []string{"c"}, "clear cache", "Environment"},
	{ActBackup, []string{"b"}, "create backup", "Environment"},
	{ActCloneContent, []string{"l"}, "clone content", "Environment"},
	{ActConnectionMode, []string{"m"}, "toggle git/sftp", "Environment"},
	{ActWake, []string{"w"}, "wake", "Environment"},
	{ActDrush, []string{"x"}, "drush", "Environment"},
	{ActCommit, []string{"o"}, "commit sftp changes", "Environment"},
	{ActCreateMultidev, []string{"n"}, "new multidev", "Environment"},
	{ActDeleteMultidev, []string{"D"}, "delete multidev", "Environment"},
	{ActUpstream, []string{"u"}, "upstream updates", "Environment"},
	{ActOpenURL, []string{"v"}, "open in browser", "Environment"},
	{ActCopyURL, []string{"y"}, "copy URL", "Environment"},
}

// KeyMap resolves key presses to actions.
type KeyMap struct {
	bindings map[string]key.Binding
	order    []binding
	byKey    map[string]string
}

// NewKeyMap builds the keymap from defaults plus config overrides. Unknown
// action names and keys bound to two actions are reported as errors; the
// defaults are kept in those cases.
func NewKeyMap(overrides map[string][]string) (KeyMap, []error) {
	km := KeyMap{bindings: map[string]key.Binding{}, byKey: map[string]string{}}
	known := map[string]bool{}
	for _, b := range defaultBindings {
		known[b.action] = true
	}
	var errs []error
	names := make([]string, 0, len(overrides))
	for n := range overrides {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !known[n] {
			errs = append(errs, fmt.Errorf("keymap: unknown action %q", n))
		}
	}
	for _, b := range defaultBindings {
		if ks, ok := overrides[b.action]; ok && len(ks) > 0 {
			b.keys = ks
		}
		km.order = append(km.order, b)
	}
	// Overridden bindings win key conflicts over defaults.
	for pass := range 2 {
		for _, b := range km.order {
			_, overridden := overrides[b.action]
			if (pass == 0) != overridden {
				continue
			}
			for _, k := range b.keys {
				if other, taken := km.byKey[k]; taken {
					errs = append(errs, fmt.Errorf("keymap: %q is bound to both %s and %s", k, other, b.action))
					continue
				}
				km.byKey[k] = b.action
			}
		}
	}
	for _, b := range km.order {
		var ks []string
		for _, k := range b.keys {
			if km.byKey[k] == b.action {
				ks = append(ks, k)
			}
		}
		km.bindings[b.action] = key.NewBinding(key.WithKeys(ks...), key.WithHelp(firstOr(ks, "—"), b.help))
	}
	return km, errs
}

func firstOr(s []string, def string) string {
	if len(s) == 0 {
		return def
	}
	return s[0]
}

// Action returns the action bound to a key string, or "".
func (km KeyMap) Action(k string) string { return km.byKey[k] }

// Key returns the primary key for an action, for hints.
func (km KeyMap) Key(action string) string { return km.bindings[action].Help().Key }

// Binding returns the bubbles key binding for an action.
func (km KeyMap) Binding(action string) key.Binding { return km.bindings[action] }

// Groups returns bindings grouped for the help screen.
func (km KeyMap) Groups() map[string][]key.Binding {
	g := map[string][]key.Binding{}
	for _, b := range km.order {
		g[b.group] = append(g[b.group], km.bindings[b.action])
	}
	return g
}

// Actions returns action names in display order with their group.
func (km KeyMap) Actions() []struct{ Action, Group string } {
	var out []struct{ Action, Group string }
	for _, b := range km.order {
		out = append(out, struct{ Action, Group string }{b.action, b.group})
	}
	return out
}
