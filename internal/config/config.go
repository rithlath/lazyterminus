// Package config loads lazyterminus's YAML configuration and resolves XDG
// paths.
package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Site maps a Pantheon site to its local git checkout.
type Site struct {
	Name string `yaml:"name"`
	Repo string `yaml:"repo"`
}

// DeployDefaults prefill the deploy form toggles.
type DeployDefaults struct {
	ClearCache  bool `yaml:"clear_cache"`
	UpdateDB    bool `yaml:"update_db"`
	SyncContent bool `yaml:"sync_content"`
}

// Duration is a time.Duration that unmarshals from strings like "10s".
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: invalid duration %q", n.Line, n.Value)
	}
	*d = Duration(v)
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// Config is the parsed config file.
type Config struct {
	TerminusPath             string              `yaml:"terminus_path"`
	LazygitPath              string              `yaml:"lazygit_path"`
	Sites                    []Site              `yaml:"sites"`
	Repos                    map[string]string   `yaml:"repos"`
	ProtectedEnvs            []string            `yaml:"protected_envs"`
	ReadOnly                 bool                `yaml:"read_only"`
	DeployDefaults           DeployDefaults      `yaml:"deploy_defaults"`
	PollInterval             Duration            `yaml:"poll_interval"`
	MaxConcurrent            int                 `yaml:"max_concurrent"`
	ExtraDestructiveCommands []string            `yaml:"extra_destructive_commands"`
	Keymap                   map[string][]string `yaml:"keymap"`

	// Path is where the config was loaded from (not serialized).
	Path string `yaml:"-"`
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		TerminusPath:   "terminus",
		LazygitPath:    "lazygit",
		ProtectedEnvs:  []string{"live", "test"},
		DeployDefaults: DeployDefaults{ClearCache: true, UpdateDB: true},
		PollInterval:   Duration(10 * time.Second),
		MaxConcurrent:  4,
		Keymap:         map[string][]string{},
	}
}

// DefaultFile is written on first run.
const DefaultFile = `# lazyterminus configuration.
# Created on first run; edit freely. All keys are optional.

# Path to the terminus binary.
terminus_path: terminus

# Path to lazygit, opened with "g" in the mapped repo.
lazygit_path: lazygit

# Optional allowlist of sites, each mapped to its local git checkout.
# Empty means every site from "terminus site:list".
sites: []
#  - name: acme-web
#    repo: ~/sites/acme-web

# Local git checkouts by site, without limiting which sites are shown. Used
# for lazygit, the Drupal version, branch status, and full commit messages
# (Pantheon's code log cuts them at 50 characters). A repo under sites: wins.
repos: {}
#  acme-web: ~/sites/acme-web

# Environments where every mutating action requires typing "site.env".
# live is always protected, even if removed from this list.
protected_envs: [live, test]

# Disable every mutating action (deploys, cache clears, backups, ...).
read_only: false

# Prefilled toggles in the deploy form.
deploy_defaults:
  clear_cache: true
  update_db: true
  sync_content: false   # test only; never offered for live

# How often to poll workflow:list for the selected site.
poll_interval: 10s

# Maximum concurrent terminus processes.
max_concurrent: 4

# Extra commands that always require typed confirmation. "*" is a wildcard,
# e.g. "secret:*" or "build:env:delete:*".
extra_destructive_commands: []

# Override key bindings: action name -> list of keys. Press "?" in the app to
# see action names. Example:
# keymap:
#   deploy: ["D"]
#   delete_multidev: ["X"]
keymap: {}
`

func xdg(env, fallback string) string {
	if v := os.Getenv(env); v != "" && filepath.IsAbs(v) {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, fallback)
}

// appDir returns base/lazyterminus. A directory left from the app's old
// name, lazypantheon, is moved there on first use so settings carry over.
func appDir(base string) string {
	dir := filepath.Join(base, "lazyterminus")
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		_ = os.Rename(filepath.Join(base, "lazypantheon"), dir)
	}
	return dir
}

// ConfigDir returns $XDG_CONFIG_HOME/lazyterminus.
func ConfigDir() string { return appDir(xdg("XDG_CONFIG_HOME", ".config")) }

// StateDir returns $XDG_STATE_HOME/lazyterminus.
func StateDir() string { return appDir(xdg("XDG_STATE_HOME", ".local/state")) }

// CacheDir returns $XDG_CACHE_HOME/lazyterminus.
func CacheDir() string { return appDir(xdg("XDG_CACHE_HOME", ".cache")) }

// DefaultPath returns the default config file path.
func DefaultPath() string { return filepath.Join(ConfigDir(), "config.yaml") }

// Load reads the config at path (DefaultPath if empty). If the file does not
// exist at the default path, a commented default is written there first.
func Load(path string) (Config, error) {
	explicit := path != ""
	if !explicit {
		path = DefaultPath()
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if explicit {
			return Config{}, fmt.Errorf("config file %s does not exist", path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			_ = os.WriteFile(path, []byte(DefaultFile), 0o644)
		}
		b = []byte(DefaultFile)
	} else if err != nil {
		return Config{}, err
	}
	cfg, err := Parse(b)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	cfg.Path = path
	return cfg, nil
}

// Parse decodes YAML over the defaults and normalizes the result.
func Parse(b []byte) (Config, error) {
	cfg := Default()
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, err
	}
	cfg.normalize()
	return cfg, nil
}

func (c *Config) normalize() {
	if c.TerminusPath == "" {
		c.TerminusPath = "terminus"
	}
	if c.LazygitPath == "" {
		c.LazygitPath = "lazygit"
	}
	if !slices.Contains(c.ProtectedEnvs, "live") {
		c.ProtectedEnvs = append(c.ProtectedEnvs, "live")
	}
	if c.MaxConcurrent < 1 {
		c.MaxConcurrent = 1
	}
	if c.PollInterval < Duration(2*time.Second) {
		c.PollInterval = Duration(2 * time.Second)
	}
	if c.Keymap == nil {
		c.Keymap = map[string][]string{}
	}
	for i := range c.Sites {
		c.Sites[i].Repo = ExpandHome(c.Sites[i].Repo)
	}
	for site, repo := range c.Repos {
		c.Repos[site] = ExpandHome(repo)
	}
}

// ExpandHome expands a leading "~/".
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// Allowed reports whether site passes the allowlist (empty allows all).
func (c Config) Allowed(site string) bool {
	if len(c.Sites) == 0 {
		return true
	}
	for _, s := range c.Sites {
		if s.Name == site {
			return true
		}
	}
	return false
}

// RepoFor returns the local repo mapped to site, or "": its sites: entry's
// repo, else its repos: entry.
func (c Config) RepoFor(site string) string {
	for _, s := range c.Sites {
		if s.Name == site && s.Repo != "" {
			return s.Repo
		}
	}
	return c.Repos[site]
}

// SiteForDir returns the configured site whose repo contains dir (the
// deepest match), or "".
func (c Config) SiteForDir(dir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	best, bestLen := "", -1
	mapped := slices.Clone(c.Sites)
	for name, repo := range c.Repos {
		mapped = append(mapped, Site{Name: name, Repo: repo})
	}
	for _, s := range mapped {
		if s.Repo == "" || c.RepoFor(s.Name) != s.Repo {
			continue
		}
		repo, err := filepath.Abs(s.Repo)
		if err != nil {
			continue
		}
		if real, err := filepath.EvalSymlinks(repo); err == nil {
			repo = real
		}
		if (dir == repo || strings.HasPrefix(dir, repo+string(filepath.Separator))) && len(repo) > bestLen {
			best, bestLen = s.Name, len(repo)
		}
	}
	return best
}
