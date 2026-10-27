package ui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rithlath/lazyterminus/internal/terminus"
	"github.com/rithlath/lazyterminus/internal/ui/panels"
	"github.com/rithlath/lazyterminus/internal/ui/theme"
)

// historyWindows are the time ranges t cycles through; 0 means the whole
// session.
var historyWindows = []struct {
	label string
	d     time.Duration
}{
	{"last 15 min", 15 * time.Minute},
	{"last hour", time.Hour},
	{"this session", 0},
	{"last 5 min", 5 * time.Minute},
}

// historyEntries returns the recorded commands in the selected window,
// newest first.
func (m *Model) historyEntries() []terminus.Recorded {
	if m.opts.Recorder == nil {
		return nil
	}
	w := historyWindows[m.historyWindow]
	cutoff := m.now().Add(-w.d)
	var out []terminus.Recorded
	for _, e := range m.opts.Recorder.Entries() {
		if w.d > 0 && e.Started.Before(cutoff) {
			break // newest first: everything after is older
		}
		if m.historyChangesOnly && !e.Mutating {
			continue
		}
		out = append(out, e)
	}
	return out
}

// historyLines renders one row per command, plus the reason under a failure.
func (m *Model) historyLines(entries []terminus.Recorded, width int) []string {
	now := m.now()
	var out []string
	for _, e := range entries {
		var sym, took string
		switch {
		case e.Interactive:
			sym, took = theme.MutedText.Render("↗"), "terminal"
		case !e.Done:
			sym, took = theme.Warn.Render(theme.SymRunning), now.Sub(e.Started).Round(time.Second).String()
		case e.Err != nil:
			sym, took = theme.Fail.Render(theme.SymFailed), e.Duration.Round(100*time.Millisecond).String()
		default:
			sym, took = theme.OK.Render(theme.SymSuccess), e.Duration.Round(100*time.Millisecond).String()
		}
		kind := theme.MutedText.Render("read  ")
		if e.Mutating {
			kind = theme.Warn.Render("change")
		}
		at := theme.MutedText.Render(e.Started.Local().Format("15:04:05"))
		out = append(out, panels.Fit(fmt.Sprintf("%s %s %-8s %s  %s", at, sym, took, kind, terminus.ShellJoin(e.Args)), width))
		if e.Done && e.Err != nil {
			msg := e.Err.Error()
			var ce *terminus.CommandError
			if errors.As(e.Err, &ce) && ce.Message != "" {
				msg = ce.Message
			}
			out = append(out, panels.Fit(theme.Fail.Render(strings.Repeat(" ", 11)+oneLine(msg)), width))
		}
	}
	return out
}

// renderHistory draws the History view body.
func (m *Model) renderHistory(w, h int) string {
	innerW, innerH := max(w-4, 1), max(h-2, 1)
	entries := m.historyEntries()
	var lines []string
	window := historyWindows[m.historyWindow].label
	switch {
	case m.opts.Recorder == nil:
		lines = []string{theme.MutedText.Render("Commands aren't recorded in this mode.")}
	case len(entries) == 0 && m.historyChangesOnly:
		lines = []string{theme.MutedText.Render("No changes in the " + window + ".")}
	case len(entries) == 0:
		lines = []string{theme.MutedText.Render("No commands in the " + window + ".")}
	default:
		lines = m.historyLines(entries, innerW)
	}
	lines = lines[min(m.historyOffset, max(len(lines)-innerH, 0)):]

	changes, failed := 0, 0
	for _, e := range entries {
		if e.Mutating {
			changes++
		}
		if e.Done && e.Err != nil {
			failed++
		}
	}
	title := fmt.Sprintf(" History · %s · %s · %s", window, plural(len(entries), "command"), plural(changes, "change"))
	if failed > 0 {
		title += fmt.Sprintf(" · %d failed", failed)
	}
	title += " "
	filter := "changes only"
	if m.historyChangesOnly {
		filter = "show reads"
	}
	hint := fmt.Sprintf(" t time range · f %s · j/k scroll · %s/esc back ", filter, m.keys.Key(ActHistory))
	return panels.Box(title, lines, w, h, true, hint)
}

// historyKey handles keys while the History view is open. Keys it doesn't
// use are swallowed so actions can't fire against an unseen environment.
func (m *Model) historyKey(k string) tea.Cmd {
	switch {
	case k == "ctrl+c":
		return tea.Quit
	case k == "esc" || m.keys.Action(k) == ActHistory || m.keys.Action(k) == ActQuit:
		m.showHistory = false
	case k == "t":
		m.historyWindow = (m.historyWindow + 1) % len(historyWindows)
		m.historyOffset = 0
	case k == "f":
		m.historyChangesOnly = !m.historyChangesOnly
		m.historyOffset = 0
	case m.keys.Action(k) == ActDown:
		m.historyOffset = min(m.historyOffset+1, m.historyMaxOffset())
	case m.keys.Action(k) == ActUp:
		m.historyOffset = max(m.historyOffset-1, 0)
	case k == "home":
		m.historyOffset = 0
	}
	return nil
}

// historyMaxOffset is how far the History view can scroll before its last
// line reaches the bottom of the box.
func (m *Model) historyMaxOffset() int {
	innerH := max(m.height-4, 1) // status and hint lines, then the box border
	return max(len(m.historyLines(m.historyEntries(), 1))-innerH, 0)
}
