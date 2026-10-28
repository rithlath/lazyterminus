package ui

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/rithlath/lazyterminus/internal/safety"
	"github.com/rithlath/lazyterminus/internal/terminus"
	"github.com/rithlath/lazyterminus/internal/ui/theme"
)

// confirmModal shows the exact commands and collects the confirmation the
// safety policy requires: y/n, or typing the site.env name.
type confirmModal struct {
	title    string
	steps    [][]string // all commands, in order (interactive last)
	sctx     safety.Context
	decision safety.Decision
	warnings []string
	input    textinput.Model
	err      string
	onOK     func(m *Model, a *safety.Approval) tea.Cmd
}

func newConfirm(m *Model, title string, steps [][]string, sctx safety.Context, warnings []string,
	onOK func(m *Model, a *safety.Approval) tea.Cmd) *confirmModal {
	c := &confirmModal{title: title, steps: steps, sctx: sctx, warnings: warnings, onOK: onOK,
		decision: m.policy().EvaluatePlan(steps, sctx)}
	c.input = textinput.New()
	c.input.Prompt = "› "
	c.input.Placeholder = c.decision.Phrase
	c.input.CharLimit = 200
	c.input.Focus()
	return c
}

func (c *confirmModal) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		c.input, cmd = c.input.Update(msg)
		return false, cmd
	}
	k := key.String()
	if k == "esc" {
		m.setStatus("Cancelled: nothing was run.", false)
		m.showOutputOnJob = false
		return true, nil
	}
	switch c.decision.Level {
	case safety.Blocked:
		return true, nil
	case safety.Typed:
		if k != "enter" {
			var cmd tea.Cmd
			c.input, cmd = c.input.Update(msg)
			c.err = ""
			return false, cmd
		}
		return c.approve(m, c.input.Value(), false)
	default:
		// Only "y" confirms: enter could arrive as a second press from the
		// form that opened this modal.
		switch k {
		case "y", "Y":
			return c.approve(m, "", true)
		case "n", "N", "q":
			m.setStatus("Cancelled: nothing was run.", false)
			m.showOutputOnJob = false
			return true, nil
		}
	}
	return false, nil
}

func (c *confirmModal) approve(m *Model, typed string, yes bool) (bool, tea.Cmd) {
	a, err := m.policy().Approve(c.steps, c.sctx, typed, yes)
	if err != nil {
		if errors.Is(err, safety.ErrWrongPhrase) {
			c.err = fmt.Sprintf("Type %s exactly to confirm.", c.decision.Phrase)
			return false, nil
		}
		m.reportErr("safety", err)
		return true, nil
	}
	return true, c.onOK(m, a)
}

func (c *confirmModal) view(width int) (string, []string, string) {
	var lines []string
	d := c.decision
	for _, w := range c.warnings {
		lines = append(lines, theme.Warn.Render("! "+w))
	}
	if len(c.warnings) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, theme.MutedText.Render("This will run:"))
	for _, s := range c.steps {
		lines = append(lines, "  "+theme.Bold.Render(terminus.Args(s).String()))
	}
	lines = append(lines, "")
	if len(d.Reasons) > 0 {
		lines = append(lines, theme.MutedText.Render("Why confirmation: "+strings.Join(d.Reasons, "; ")))
		lines = append(lines, "")
	}
	footer := ""
	switch d.Level {
	case safety.Blocked:
		lines = append(lines, theme.Fail.Render(theme.SymFailed+" Blocked: "+strings.Join(d.Reasons, "; ")))
		footer = " esc close "
	case safety.Typed:
		lines = append(lines, "Type "+theme.Key.Render(d.Phrase)+" to confirm:")
		c.input.SetWidth(max(width-6, 10))
		lines = append(lines, c.input.View())
		footer = " enter confirm · esc cancel "
	default:
		lines = append(lines, "Run it? "+theme.Key.Render("y")+theme.MutedText.Render("/")+theme.Key.Render("n"))
		footer = " y confirm · n/esc cancel "
	}
	if c.err != "" {
		lines = append(lines, theme.Fail.Render(theme.SymFailed+" "+c.err))
	}
	return c.title, lines, footer
}

// promptModal is a plain yes/no question (not a safety confirmation).
type promptModal struct {
	title, question string
	onYes           func(m *Model) tea.Cmd
}

func (p *promptModal) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}
	switch key.String() {
	case "y", "Y":
		return true, p.onYes(m)
	case "n", "N", "esc", "q":
		return true, nil
	}
	return false, nil
}

func (p *promptModal) view(int) (string, []string, string) {
	return p.title, []string{p.question, "", theme.Key.Render("y") + " yes   " + theme.Key.Render("n") + " no"}, " y yes · n no "
}

// infoModal shows text (e.g. "nothing to deploy").
type infoModal struct {
	title string
	lines []string
}

func (i *infoModal) update(_ *Model, msg tea.Msg) (bool, tea.Cmd) {
	_, ok := msg.(tea.KeyPressMsg)
	return ok, nil
}

func (i *infoModal) view(int) (string, []string, string) {
	return i.title, i.lines, " any key to close "
}
