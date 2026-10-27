package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/rithlath/lazyterminus/internal/terminus"
	"github.com/rithlath/lazyterminus/internal/ui/panels"
	"github.com/rithlath/lazyterminus/internal/ui/theme"
)

// modal is an overlay that takes all key input while open.
type modal interface {
	// update handles a message; done closes the modal.
	update(m *Model, msg tea.Msg) (done bool, cmd tea.Cmd)
	// view renders the modal body for the given inner width.
	view(width int) (title string, lines []string, footer string)
}

type fieldKind int

const (
	fieldText fieldKind = iota
	fieldToggle
	fieldSelect
)

type formField struct {
	key, label string
	kind       fieldKind
	input      textinput.Model
	on         bool
	options    []string
	idx        int
	required   bool
	validate   func(string) error
	help       string
}

// formValues maps field keys to values; toggles are "true" or "".
type formValues map[string]string

func (v formValues) on(key string) bool { return v[key] == "true" }

// form is a generic argument form. preview turns current values into the
// exact commands that will run (or a validation error).
type form struct {
	title   string
	intro   []string
	fields  []formField
	focus   int
	err     string
	preview func(v formValues) ([][]string, error)
	submit  func(m *Model, v formValues, steps [][]string) tea.Cmd
}

func textField(key, label, value, placeholder string, required bool, validate func(string) error) formField {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = placeholder
	ti.SetValue(value)
	ti.CharLimit = 500
	return formField{key: key, label: label, kind: fieldText, input: ti, required: required, validate: validate}
}

func toggleField(key, label string, on bool, help string) formField {
	return formField{key: key, label: label, kind: fieldToggle, on: on, help: help}
}

func selectField(key, label string, options []string, selected string) formField {
	f := formField{key: key, label: label, kind: fieldSelect, options: options}
	for i, o := range options {
		if o == selected {
			f.idx = i
		}
	}
	return f
}

func (f *form) values() formValues {
	v := formValues{}
	for _, fl := range f.fields {
		switch fl.kind {
		case fieldText:
			v[fl.key] = strings.TrimSpace(fl.input.Value())
		case fieldToggle:
			if fl.on {
				v[fl.key] = "true"
			}
		case fieldSelect:
			if len(fl.options) > 0 {
				v[fl.key] = fl.options[fl.idx]
			}
		}
	}
	return v
}

func (f *form) validate() ([][]string, error) {
	v := f.values()
	for _, fl := range f.fields {
		if fl.kind != fieldText {
			continue
		}
		if fl.required && v[fl.key] == "" {
			return nil, fmt.Errorf("%s is required", strings.ToLower(fl.label))
		}
		if fl.validate != nil && v[fl.key] != "" {
			if err := fl.validate(v[fl.key]); err != nil {
				return nil, fmt.Errorf("%s: %w", strings.ToLower(fl.label), err)
			}
		}
	}
	return f.preview(v)
}

func (f *form) focusField(i int) tea.Cmd {
	if len(f.fields) == 0 {
		return nil
	}
	f.focus = (i + len(f.fields)) % len(f.fields)
	var cmd tea.Cmd
	for j := range f.fields {
		if f.fields[j].kind != fieldText {
			continue
		}
		if j == f.focus {
			cmd = f.fields[j].input.Focus()
		} else {
			f.fields[j].input.Blur()
		}
	}
	return cmd
}

func (f *form) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		if len(f.fields) > 0 && f.fields[f.focus].kind == fieldText {
			var cmd tea.Cmd
			f.fields[f.focus].input, cmd = f.fields[f.focus].input.Update(msg)
			return false, cmd
		}
		return false, nil
	}
	f.err = ""
	var cur *formField
	if len(f.fields) > 0 {
		cur = &f.fields[f.focus]
	}
	switch key.String() {
	case "esc":
		m.setStatus("Cancelled.", false)
		return true, nil
	case "tab", "down":
		return false, f.focusField(f.focus + 1)
	case "shift+tab", "up":
		return false, f.focusField(f.focus - 1)
	case "enter", "ctrl+s":
		steps, err := f.validate()
		if err != nil {
			f.err = err.Error()
			return false, nil
		}
		return true, f.submit(m, f.values(), steps)
	}
	if cur == nil {
		return false, nil
	}
	switch cur.kind {
	case fieldToggle:
		if k := key.String(); k == "space" || k == "x" {
			cur.on = !cur.on
		}
	case fieldSelect:
		switch key.String() {
		case "left", "h":
			cur.idx = (cur.idx + len(cur.options) - 1) % len(cur.options)
		case "right", "l", "space":
			cur.idx = (cur.idx + 1) % len(cur.options)
		}
	case fieldText:
		var cmd tea.Cmd
		cur.input, cmd = cur.input.Update(msg)
		return false, cmd
	}
	return false, nil
}

func (f *form) view(width int) (string, []string, string) {
	lines := append([]string{}, f.intro...)
	if len(f.intro) > 0 {
		lines = append(lines, "")
	}
	labelW := 0
	for _, fl := range f.fields {
		labelW = max(labelW, lipgloss.Width(fl.label))
	}
	for i, fl := range f.fields {
		marker := "  "
		label := theme.MutedText.Render(panels.Fit(fl.label, labelW))
		if i == f.focus {
			marker = theme.Key.Render(theme.SymCursor + " ")
			label = theme.Bold.Render(panels.Fit(fl.label, labelW))
		}
		var val string
		switch fl.kind {
		case fieldText:
			fl.input.SetWidth(max(width-labelW-6, 10))
			val = fl.input.View()
			if fl.required {
				label += theme.Warn.Render("*")
			} else {
				label += " "
			}
		case fieldToggle:
			box := "[ ]"
			if fl.on {
				box = theme.OK.Render("[x]")
			}
			val = box + " " + theme.MutedText.Render(fl.help)
			label += " "
		case fieldSelect:
			val = theme.MutedText.Render("‹ ") + theme.Bold.Render(fl.options[fl.idx]) + theme.MutedText.Render(" ›")
			label += " "
		}
		lines = append(lines, marker+label+" "+val)
	}
	lines = append(lines, "")
	steps, err := f.validate()
	if f.err != "" {
		lines = append(lines, theme.Fail.Render(theme.SymFailed+" "+f.err))
	} else if err != nil {
		lines = append(lines, theme.MutedText.Render("… "+err.Error()))
	} else {
		lines = append(lines, theme.MutedText.Render("Will run:"))
		for _, s := range steps {
			lines = append(lines, "  "+theme.Bold.Render(terminus.Args(s).String()))
		}
	}
	return f.title, lines, " enter submit · tab next · space toggle · ←/→ choose · esc cancel "
}
