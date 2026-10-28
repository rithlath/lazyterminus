package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/rithlath/lazyterminus/internal/terminus"
	"github.com/rithlath/lazyterminus/internal/ui/panels"
	"github.com/rithlath/lazyterminus/internal/ui/theme"
)

// menuItem is one entry in an environment's action menu. A non-empty
// disabled explains why the action doesn't apply here.
type menuItem struct {
	action   string // keymap action, or menuPalette
	label    string
	disabled string
}

const menuPalette = "palette"

// actionMenu lists what can be done with one environment. Choosing an entry
// runs exactly what its key does, with the same forms and confirmations.
type actionMenu struct {
	title  string
	items  []menuItem
	keys   map[string]string // action → key shown (respects keymap overrides)
	cursor int
}

// envMenuItems builds the menu for env, mirroring the checks in runAction
// so the menu never offers something the key would refuse.
func (m *Model) envMenuItems(site string, e *terminus.Env) []menuItem {
	st := m.state(site)
	readOnly := m.cfg.ReadOnly
	item := func(action, label, disabled string) menuItem {
		if readOnly && disabled == "" && action != ActUpstream && action != menuPalette {
			disabled = "read_only is on"
		}
		return menuItem{action: action, label: label, disabled: disabled}
	}
	var items []menuItem

	// Deploy
	if src := terminus.SourceEnv(e.ID); src != "" {
		label := "Deploy from " + src
		switch n := m.waitingInto(site, e.ID); {
		case n > 0:
			label = fmt.Sprintf("Deploy %s from %s", plural(n, "commit"), src)
		case n == 0:
			label = "Deploy from " + src + " (nothing waiting)"
		}
		items = append(items, item(ActDeploy, label, ""))
	} else {
		items = append(items, item(ActDeploy, "Deploy", "test and live only; push to Git to update "+e.ID))
	}
	items = append(items,
		item(ActClearCache, "Clear cache", ""),
		item(ActBackup, "Create backup", ""),
	)
	if e.ID == "live" {
		items = append(items, item(ActCloneContent, "Clone content from live into another environment", ""))
	} else {
		items = append(items, item(ActCloneContent, "Clone content into "+e.ID, ""))
	}
	items = append(items,
		item(ActDrush, "Run drush", ""),
		item(ActWake, "Wake", ""),
	)

	// Connection mode
	if e.ID == "dev" || e.IsMultidev() {
		if e.ConnectionMode == "sftp" {
			items = append(items, item(ActConnectionMode, "Switch to Git mode", ""))
		} else {
			items = append(items, item(ActConnectionMode, "Switch to SFTP mode", ""))
		}
	} else {
		items = append(items, item(ActConnectionMode, "Git/SFTP mode", "dev and multidevs only"))
	}

	// Commit
	if e.ConnectionMode == "sftp" {
		label := "Commit SFTP changes"
		if ds, ok := st.diffstat[e.ID]; ok {
			if len(ds) == 0 {
				label = "Commit SFTP changes (none)"
			} else {
				label = "Commit " + plural(len(ds), "changed file")
			}
		}
		items = append(items, item(ActCommit, label, ""))
	} else {
		items = append(items, item(ActCommit, "Commit SFTP changes", "SFTP mode only"))
	}

	// Multidevs
	from := e.ID
	if e.IsMultidev() {
		from = "dev"
	}
	items = append(items, item(ActCreateMultidev, "New multidev (from "+from+")", ""))
	if e.IsMultidev() {
		items = append(items, item(ActDeleteMultidev, "Delete multidev "+e.ID, ""))
	} else {
		items = append(items, item(ActDeleteMultidev, "Delete multidev", "multidevs only"))
	}

	items = append(items,
		item(ActUpstream, "Upstream updates", ""),
		item(menuPalette, "More Terminus commands for "+e.ID+"…", ""),
	)
	// Available first, then the rest, keeping each group's order.
	var on, off []menuItem
	for _, it := range items {
		if it.disabled == "" {
			on = append(on, it)
		} else {
			off = append(off, it)
		}
	}
	return append(on, off...)
}

// openEnvMenu opens the action menu for the selected environment.
func (m *Model) openEnvMenu() tea.Cmd {
	site, e, ok := m.target()
	if !ok {
		return nil
	}
	menu := &actionMenu{title: terminus.SiteEnv(site, e.ID), items: m.envMenuItems(site, e), keys: map[string]string{}}
	for _, it := range menu.items {
		if it.action == menuPalette {
			menu.keys[it.action] = m.keys.Key(ActPalette)
		} else {
			menu.keys[it.action] = m.keys.Key(it.action)
		}
	}
	m.modal = menu
	return nil
}

func (a *actionMenu) move(d int) {
	for i := a.cursor + d; i >= 0 && i < len(a.items); i += d {
		if a.items[i].disabled == "" {
			a.cursor = i
			return
		}
	}
}

func (a *actionMenu) run(m *Model, it menuItem) tea.Cmd {
	if it.disabled != "" {
		m.setStatus(it.label+": "+it.disabled, true)
		return nil
	}
	if it.action == menuPalette {
		return m.openPalette()
	}
	return m.runAction(it.action)
}

func (a *actionMenu) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}
	k := key.String()
	switch k {
	case "esc", "q":
		return true, nil
	case "up", "k":
		a.move(-1)
		return false, nil
	case "down", "j", "tab":
		a.move(1)
		return false, nil
	case "enter":
		if len(a.items) == 0 {
			return true, nil
		}
		return true, a.run(m, a.items[a.cursor])
	}
	// An item's own key runs it directly.
	for _, it := range a.items {
		if (it.action == menuPalette && k == m.keys.Key(ActPalette)) || (it.action != menuPalette && m.keys.Action(k) == it.action) {
			if it.disabled != "" {
				m.setStatus(it.label+": "+it.disabled, true)
				return false, nil
			}
			return true, a.run(m, it)
		}
	}
	return false, nil
}

// click runs the item on line (as its key would).
func (a *actionMenu) click(m *Model, line int, _ bool) (bool, tea.Cmd) {
	l, shownOff := 0, false
	for i, it := range a.items {
		if it.disabled != "" && !shownOff {
			l, shownOff = l+1, true // the "not available here" divider
		}
		if l == line {
			if it.disabled != "" {
				m.setStatus(it.label+": "+it.disabled, true)
				return false, nil
			}
			a.cursor = i
			return true, a.run(m, it)
		}
		l++
	}
	return false, nil
}

func (a *actionMenu) view(width int) (string, []string, string) {
	var lines []string
	shownOff := false
	for i, it := range a.items {
		key := a.keys[it.action]
		if it.disabled != "" {
			if !shownOff {
				lines = append(lines, theme.Divider.Render("── not available here "+strings.Repeat("─", max(width-24, 0))))
				shownOff = true
			}
			lines = append(lines, "  "+theme.MutedText.Render(panels.Fit(key, 3)+it.label+" · "+it.disabled))
			continue
		}
		text := theme.Key.Render(panels.Fit(key, 3)) + it.label
		lines = append(lines, panels.Row(text, width, i == a.cursor, true))
	}
	return a.title, lines, " enter run · key runs directly · esc close "
}
