// Command lazyterminus is a terminal UI for Terminus, Pantheon's CLI.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	lazyterminus "github.com/rithlath/lazyterminus"
	"github.com/rithlath/lazyterminus/internal/config"
	"github.com/rithlath/lazyterminus/internal/safety"
	"github.com/rithlath/lazyterminus/internal/terminus"
	"github.com/rithlath/lazyterminus/internal/ui"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "lazyterminus:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		mock       = flag.Bool("mock", false, "run against built-in fixtures instead of Terminus")
		dryRun     = flag.Bool("dry-run", false, "run read-only commands for real but print mutating commands instead of running them")
		site       = flag.String("site", "", "select this site on startup")
		env        = flag.String("env", "", "focus this environment on startup: site.env, or just env when run inside a mapped repo")
		configPath = flag.String("config", "", "config file (default "+config.DefaultPath()+")")
		showVer    = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()
	if *showVer {
		fmt.Println("lazyterminus", version)
		return nil
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	// Inside a repo mapped in config, the site can be inferred, so lazygit
	// can launch "lazyterminus --env <branch>".
	cwdSite := ""
	if wd, err := os.Getwd(); err == nil {
		cwdSite = cfg.SiteForDir(wd)
	}
	*site, *env, err = resolveStart(*site, *env, cwdSite)
	if err != nil {
		return fmt.Errorf("%w (map repos under sites: in %s)", err, cfg.Path)
	}

	var base terminus.Runner = terminus.ExecRunner{Path: cfg.TerminusPath}
	historyPath := terminus.HistoryPath(config.StateDir())
	if *mock {
		mr, err := terminus.NewMockRunner(lazyterminus.Fixtures())
		if err != nil {
			return fmt.Errorf("loading fixtures: %w", err)
		}
		base = mr
		historyPath = filepath.Join(config.StateDir(), "history.mock.log")
	}
	r, dry, rec := safety.Chain(base, cfg, historyPath, *dryRun)

	model := ui.New(ui.Options{Runner: r, Recorder: rec, Config: cfg, Site: *site, Env: *env, Mock: *mock, DryRun: *dryRun, HistoryLog: historyPath})
	if _, err := tea.NewProgram(model).Run(); err != nil {
		return err
	}
	if dry != nil && len(dry.Printed()) > 0 {
		fmt.Println("Dry run: these commands would have run:")
		for _, args := range dry.Printed() {
			fmt.Println("  " + strings.TrimSpace(terminus.Args(args).String()))
		}
	}
	return nil
}

// resolveStart turns --site/--env into a site and a site.env. env may be
// site.env, or a bare env (or branch name) when the site comes from --site
// or the current directory. master/main map to dev, Pantheon's branch for
// the dev environment.
func resolveStart(site, env, cwdSite string) (string, string, error) {
	if env != "" {
		s, e, ok := terminus.SplitSiteEnv(env)
		if !ok {
			if s = site; s == "" {
				s = cwdSite
			}
			if s == "" {
				return "", "", fmt.Errorf("--env %q has no site: use site.env, --site, or run inside a mapped repo", env)
			}
			e = env
		}
		if e == "master" || e == "main" {
			e = "dev"
		}
		env = terminus.SiteEnv(s, e)
		site = s // the environment's site wins over --site
	}
	if site == "" {
		site = cwdSite
	}
	return site, env, nil
}
