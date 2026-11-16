package ui

import (
	"context"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rithlath/lazyterminus/internal/safety"
	"github.com/rithlath/lazyterminus/internal/terminus"
	"github.com/rithlath/lazyterminus/internal/ui/theme"
)

// capturedMsg carries the output of a final command run for its result
// (rather than streamed to Output or detached).
type capturedMsg struct {
	title string
	out   string
	err   error
	then  func(m *Model, out string) tea.Cmd
}

// captureTimeout covers waking the SSH connection and drush bootstrapping.
const captureTimeout = 3 * time.Minute

func (m *Model) runCaptured(actx context.Context, title string, args []string, then func(*Model, string) tea.Cmd) tea.Cmd {
	m.setStatus("Running "+title+"…", false)
	m.logOutput(theme.Bold.Render("▶ " + terminus.Args(args).String()))
	r := m.opts.Runner
	return tea.Batch(m.startSpinner(), func() tea.Msg {
		cx, cancel := context.WithTimeout(actx, captureTimeout)
		defer cancel()
		out, _, err := r.Run(cx, args...)
		return capturedMsg{title: title, out: string(out), err: err, then: then}
	})
}

func (m *Model) onCaptured(msg capturedMsg) tea.Cmd {
	if msg.err != nil {
		m.reportErr(msg.title, msg.err)
		return nil
	}
	if out := strings.TrimRight(msg.out, "\n"); out != "" {
		m.logOutput(strings.Split(out, "\n")...)
	}
	return msg.then(m, msg.out)
}

var linkRe = regexp.MustCompile(`https?://\S+`)

// actLoginAs asks for a user and a path, runs drush uli on the environment
// and copies the one-time login link.
func (m *Model) actLoginAs(site, env string) tea.Cmd {
	se := terminus.SiteEnv(site, env)
	return m.open(&form{
		title: "Log in as · " + se,
		intro: []string{
			theme.MutedText.Render("Makes a one-time login link (drush uli) and copies it. It works once and expires after a day,"),
			theme.MutedText.Render("so you can open it yourself or send it to someone who can't get past SSO."),
		},
		fields: []formField{
			textField("name", "User", "", "username; blank logs in as user 1", false, nil),
			textField("path", "Then go to", "", "optional path, e.g. node/12/edit", false, nil),
			toggleField("open", "Open in my browser", false, "uses up the link; leave off to send it to someone"),
		},
		preview: func(v formValues) ([][]string, error) {
			drush := []string{"remote:drush", se, "--", "uli"}
			if n := v["name"]; n != "" {
				drush = append(drush, "--name="+n)
			}
			if p := v["path"]; p != "" {
				drush = append(drush, strings.TrimPrefix(p, "/"))
			}
			return append(wakeSteps(site, env), drush), nil
		},
		submit: func(m *Model, v formValues, steps [][]string) tea.Cmd {
			who := v["name"]
			if who == "" {
				who = "user 1"
			}
			open := v["open"] == "true"
			then := func(m *Model, out string) tea.Cmd {
				link := linkRe.FindString(out)
				if link == "" {
					m.setStatus("drush uli gave no link; see Output ("+m.keys.Key(ActOutput)+").", true)
					m.showOutput = true
					return nil
				}
				cmd := m.copyText(link, "the login link for "+who)
				if open {
					cmd = tea.Batch(cmd, m.openURL(link))
				}
				return cmd
			}
			n := len(steps) - 1
			return m.confirmAndRun("Login link for "+who+" on "+se, site, env, steps[:n], &final{args: steps[n], then: then}, safety.Context{}, nil)
		},
	})
}
