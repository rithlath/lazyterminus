package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Mouse support, after wsl-monitor. While rendering, each frame records
// where clickable things were drawn (panel rows, pipeline boxes, key hints,
// modal lines); View attaches an OnMouse handler mapping a click there to a
// clickMsg.
//
// A click does what the matching key does: a single click selects, a double
// or right click acts like enter, and a key hint is that key. Every form and
// confirmation still applies; no click runs a command by itself. The one
// exception to "select first" is a URL, which a click opens in the browser.

type clickKind int

const (
	clickKey     clickKind = iota // str: a key, handled as if pressed
	clickPanel                    // panel: focus it
	clickRow                      // panel, index: a list row
	clickPipe                     // str: a pipeline box's environment
	clickURL                      // str: a URL to open in the browser
	clickModal                    // index: a line of the open modal's view
	clickNone                     // inside the open modal, on nothing
	clickOutside                  // outside the open modal: esc
)

type clickTarget struct {
	kind  clickKind
	panel int
	index int
	str   string
}

type clickMsg struct {
	t     clickTarget
	right bool
}

type zone struct {
	x, y, w, h int
	t          clickTarget
}

func (z zone) contains(x, y int) bool {
	return x >= z.x && x < z.x+z.w && y >= z.y && y < z.y+z.h
}

// zoneList collects one frame's zones. Render copies of the model share it;
// a nil list (rendering outside View) records nothing. The first zone
// containing a click wins, so rows go before the panel around them.
type zoneList struct{ list []zone }

func (z *zoneList) add(x, y, w, h int, t clickTarget) {
	if z != nil && w > 0 && h > 0 {
		z.list = append(z.list, zone{x, y, w, h, t})
	}
}

func (z *zoneList) reset() {
	if z != nil {
		z.list = z.list[:0]
	}
}

// footer registers each "key label" segment of the footer panels.Box draws
// into the bottom border of the w-wide box at x, bottom row y.
func (z *zoneList) footer(footer string, x, y, w int) {
	plain := ansi.Strip(ansi.Truncate(footer, max(w-5, 0), "…"))
	col := x + w - 2 - ansi.StringWidth(plain)
	for _, seg := range strings.Split(plain, "·") {
		sw := ansi.StringWidth(seg)
		if k := hintKey(seg); k != "" {
			z.add(col, y, sw, 1, clickTarget{kind: clickKey, str: k})
		}
		col += sw + ansi.StringWidth("·")
	}
}

// hintKey is the key a footer segment like "enter run" or "n/esc cancel"
// names, or "" when it names none ("3/14", "◆ fills dev", "←/→ choose").
func hintKey(seg string) string {
	f := strings.Fields(seg)
	if len(f) < 2 {
		return ""
	}
	k, _, _ := strings.Cut(f[0], "/")
	switch {
	case k == "any":
		return "esc"
	case k == "enter" || k == "esc" || k == "tab" || k == "space" || k == "ctrl+s":
		return k
	case len(k) == 1 && k[0] > ' ' && k[0] < 0x7f:
		return k
	}
	return ""
}

// onMouse builds the frame's mouse handler.
func onMouse(zs []zone) func(tea.MouseMsg) tea.Cmd {
	return func(msg tea.MouseMsg) tea.Cmd {
		switch msg := msg.(type) {
		case tea.MouseWheelMsg:
			// Terminals turn the wheel into arrow keys for full-screen apps
			// that don't take the mouse; keep that.
			switch msg.Button {
			case tea.MouseWheelUp:
				return func() tea.Msg { return keyMsg("up") }
			case tea.MouseWheelDown:
				return func() tea.Msg { return keyMsg("down") }
			}
		case tea.MouseClickMsg:
			mo := msg.Mouse()
			if mo.Button != tea.MouseLeft && mo.Button != tea.MouseRight {
				return nil
			}
			for _, z := range zs {
				if z.contains(mo.X, mo.Y) {
					c := clickMsg{t: z.t, right: mo.Button == tea.MouseRight}
					return func() tea.Msg { return c }
				}
			}
		}
		return nil
	}
}

const doubleClick = 450 * time.Millisecond

func (m Model) handleClick(msg clickMsg) (tea.Model, tea.Cmd) {
	t := msg.t
	id := fmt.Sprintf("%d:%d:%d:%s", t.kind, t.panel, t.index, t.str)
	now := time.Now()
	act := msg.right || id == m.lastClick && now.Sub(m.lastClickAt) < doubleClick
	m.lastClick, m.lastClickAt = id, now
	if act {
		m.lastClick = "" // a third click starts over
	}
	enter := keyMsg(m.keys.Key(ActSelect))

	switch t.kind {
	case clickKey:
		return m.Update(keyMsg(t.str))
	case clickOutside:
		if m.modal != nil {
			return m.Update(keyMsg("esc"))
		}
	case clickModal:
		if c, ok := m.modal.(clickable); ok {
			cur := m.modal
			done, cmd := c.click(&m, t.index, act)
			if done && m.modal == cur { // it may have opened the next modal
				m.modal = nil
			}
			return m, cmd
		}
	case clickPanel:
		m.focus = t.panel
		if act {
			return m.handleKey(enter)
		}
	case clickRow:
		m.focus = t.panel
		cmd := m.selectRow(t.panel, t.index)
		if act {
			next, open := m.handleKey(enter)
			return next, tea.Batch(cmd, open)
		}
		return m, cmd
	case clickURL:
		return m, m.openURL(t.str)
	case clickPipe:
		cmd := m.selectEnv(t.str)
		if act {
			return m, tea.Batch(cmd, m.openEnvMenu())
		}
		return m, cmd
	}
	return m, nil
}

// selectRow moves panel p's cursor to row i, as the arrow keys would.
func (m *Model) selectRow(p, i int) tea.Cmd {
	switch p {
	case panelSites:
		if i < len(m.sites) {
			return m.selectSite(i)
		}
	case panelEnvs:
		m.envList.Cursor = i
		m.detailOffset = 0
		return tea.Batch(m.ensureOps(m.site()), m.ensureBranch(), m.ensureDomains())
	case panelWorkflows:
		m.wfList.Cursor = i
	}
	return nil
}

// clickable is implemented by modals whose lines respond to the mouse.
type clickable interface {
	// click handles a click on line i of the modal's view lines; act is a
	// double or right click. done closes the modal.
	click(m *Model, line int, act bool) (done bool, cmd tea.Cmd)
}

// keyMsg builds a key press for a key string as KeyPressMsg.String()
// reports it.
func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	case "":
		return tea.KeyPressMsg{}
	}
	r := []rune(k)[0]
	if r >= 'A' && r <= 'Z' {
		return tea.KeyPressMsg{Code: r + ('a' - 'A'), ShiftedCode: r, Text: k, Mod: tea.ModShift}
	}
	return tea.KeyPressMsg{Code: r, Text: k}
}
