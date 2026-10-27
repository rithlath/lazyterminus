package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rithlath/lazyterminus/internal/config"
	"github.com/rithlath/lazyterminus/internal/ui/theme"
)

type (
	branchMsg struct {
		site, branch, status string
	}
	lazygitDoneMsg struct {
		site string
		err  error
	}
)

func git(repo string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

// PantheonRemote returns the name of the repo's Pantheon remote: the one
// whose URL points at a *.drush.in codeserver, else "pantheon" if such a
// remote exists, else "".
func PantheonRemote(repo string) string {
	out, err := git(repo, "remote", "-v")
	if err != nil {
		return ""
	}
	fallback := ""
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		if strings.Contains(f[1], ".drush.in") {
			return f[0]
		}
		if f[0] == "pantheon" {
			fallback = "pantheon"
		}
	}
	return fallback
}

// BranchStatus describes a local branch relative to the Pantheon remote,
// from local refs only (as of the last fetch; lazyterminus never fetches).
func BranchStatus(repo, branch string) string {
	if repo == "" {
		return theme.MutedText.Render("no local repo mapped")
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		if _, err := git(repo, "rev-parse", "--git-dir"); err != nil {
			return theme.MutedText.Render("not a git repo: " + repo)
		}
	}
	if _, err := git(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		return theme.MutedText.Render("no local branch " + branch)
	}
	remote := PantheonRemote(repo)
	if remote == "" {
		return "local branch exists " + theme.MutedText.Render("(no Pantheon remote configured)")
	}
	rref := remote + "/" + branch
	if _, err := git(repo, "rev-parse", "--verify", "--quiet", "refs/remotes/"+rref); err != nil {
		return "local branch exists " + theme.MutedText.Render("(not on "+remote+" as of last fetch)")
	}
	out, err := git(repo, "rev-list", "--left-right", "--count", branch+"..."+rref)
	if err != nil {
		return theme.Fail.Render("git error: " + err.Error())
	}
	var ahead, behind int
	if _, err := fmt.Sscanf(out, "%d %d", &ahead, &behind); err != nil {
		return theme.Fail.Render("git error: " + out)
	}
	note := theme.MutedText.Render(" (as of last fetch)")
	switch {
	case ahead == 0 && behind == 0:
		return theme.OK.Render(theme.SymSuccess+" in sync with "+rref) + note
	case behind == 0:
		return theme.Warn.Render(fmt.Sprintf("↑%d ahead of %s", ahead, rref)) + theme.MutedText.Render(" (push to deploy)") + note
	case ahead == 0:
		return theme.Warn.Render(fmt.Sprintf("↓%d behind %s", behind, rref)) + note
	}
	return theme.Warn.Render(fmt.Sprintf("↑%d ↓%d diverged from %s", ahead, behind, rref)) + note
}

func loadBranch(site, repo, branch string) tea.Cmd {
	return func() tea.Msg {
		return branchMsg{site: site, branch: branch, status: BranchStatus(repo, branch)}
	}
}

// ensureBranch loads local branch status for a selected multidev.
func (m *Model) ensureBranch() tea.Cmd {
	site, env := m.site(), m.selectedEnv()
	st := m.state(site)
	e := st.env(env)
	if e == nil || !e.IsMultidev() {
		return nil
	}
	if _, ok := st.branches[env]; ok || !st.begin("branch:"+env) {
		return nil
	}
	return loadBranch(site, m.cfg.RepoFor(site), env)
}

// localBranchStatus is shown in the Detail panel for multidevs.
func (m *Model) localBranchStatus(site, branch string) string {
	if s, ok := m.state(site).branches[branch]; ok {
		return s
	}
	return theme.MutedText.Render("checking…")
}

// openLazygit hands the terminal to lazygit in the site's repo.
func (m *Model) openLazygit() tea.Cmd {
	site := m.site()
	repo := m.cfg.RepoFor(site)
	if repo == "" {
		path := m.cfg.Path
		if path == "" {
			path = config.DefaultPath()
		}
		m.setStatus(fmt.Sprintf("No local repo mapped for %s. Add it under sites: in %s", site, path), true)
		return nil
	}
	if _, err := os.Stat(repo); err != nil {
		m.reportErr("lazygit", fmt.Errorf("repo %s: %w", repo, err))
		return nil
	}
	c := exec.Command(m.cfg.LazygitPath, "-p", repo)
	return tea.ExecProcess(c, func(err error) tea.Msg { return lazygitDoneMsg{site: site, err: err} })
}

func (m *Model) onLazygitDone(msg lazygitDoneMsg) tea.Cmd {
	if msg.err != nil {
		m.reportErr("lazygit", msg.err)
	} else {
		m.setStatus("Back from lazygit; refreshing "+msg.site+"…", false)
	}
	// Pushes from lazygit change what Pantheon has: reload everything.
	m.client.InvalidateSite(msg.site)
	st := m.state(msg.site)
	st.inflight = map[string]bool{}
	st.branches = map[string]string{}
	st.drupalLoaded = false
	return tea.Batch(m.ensureSite(msg.site, true), m.ensureBranch(), m.startSpinner())
}
