package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDefaultFileParsesToDefaults(t *testing.T) {
	cfg, err := Parse([]byte(DefaultFile))
	if err != nil {
		t.Fatal(err)
	}
	d := Default()
	if cfg.TerminusPath != d.TerminusPath || cfg.MaxConcurrent != 4 || time.Duration(cfg.PollInterval) != 10*time.Second ||
		!cfg.DeployDefaults.ClearCache || !cfg.DeployDefaults.UpdateDB || cfg.DeployDefaults.SyncContent || cfg.ReadOnly {
		t.Errorf("cfg = %+v", cfg)
	}
	if !slices.Equal(cfg.ProtectedEnvs, []string{"live", "test"}) {
		t.Errorf("protected = %v", cfg.ProtectedEnvs)
	}
}

func TestLiveAlwaysProtected(t *testing.T) {
	cfg, err := Parse([]byte("protected_envs: [test]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cfg.ProtectedEnvs, "live") {
		t.Errorf("live dropped: %v", cfg.ProtectedEnvs)
	}
	cfg, _ = Parse([]byte("protected_envs: []\n"))
	if !slices.Equal(cfg.ProtectedEnvs, []string{"live"}) {
		t.Errorf("protected = %v", cfg.ProtectedEnvs)
	}
}

func TestParseFull(t *testing.T) {
	home, _ := os.UserHomeDir()
	cfg, err := Parse([]byte(`
terminus_path: /opt/terminus
sites:
  - name: acme-web
    repo: ~/sites/acme-web
read_only: true
poll_interval: 30s
max_concurrent: 0
extra_destructive_commands: ["secret:*"]
keymap:
  deploy: ["D", "ctrl+d"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RepoFor("acme-web") != filepath.Join(home, "sites/acme-web") {
		t.Errorf("repo = %q", cfg.RepoFor("acme-web"))
	}
	if !cfg.Allowed("acme-web") || cfg.Allowed("acme-intranet") || !cfg.ReadOnly || cfg.MaxConcurrent != 1 {
		t.Errorf("cfg = %+v", cfg)
	}
	if !slices.Equal(cfg.Keymap["deploy"], []string{"D", "ctrl+d"}) {
		t.Errorf("keymap = %v", cfg.Keymap)
	}
}

func TestParseRejectsUnknownKeysAndBadDuration(t *testing.T) {
	if _, err := Parse([]byte("protectd_envs: [live]\n")); err == nil {
		t.Error("typo key should fail")
	}
	if _, err := Parse([]byte("poll_interval: soon\n")); err == nil || !strings.Contains(err.Error(), "duration") {
		t.Errorf("err = %v", err)
	}
}

func TestLoadCreatesDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "lazyterminus", "config.yaml"))
	if err != nil || string(b) != DefaultFile {
		t.Fatalf("default not written: %v", err)
	}
	if cfg.Path == "" {
		t.Error("path not set")
	}
	if _, err := Load(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Error("explicit missing path should error")
	}
}

func TestEmptyFile(t *testing.T) {
	cfg, err := Parse(nil)
	if err != nil || cfg.MaxConcurrent != 4 {
		t.Errorf("cfg=%+v err=%v", cfg, err)
	}
}

func TestSiteForDir(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "sites", "acme-web")
	b := filepath.Join(root, "sites", "acme-web", "vendor", "nested")
	if err := os.MkdirAll(filepath.Join(b, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Sites: []Site{{Name: "acme-web", Repo: a}, {Name: "nested", Repo: b}, {Name: "none"}}}
	cases := map[string]string{a: "acme-web", filepath.Join(b, "x"): "nested", root: "", a + "-other": ""}
	for dir, want := range cases {
		if got := cfg.SiteForDir(dir); got != want {
			t.Errorf("SiteForDir(%s) = %q, want %q", dir, got, want)
		}
	}
}

func TestConfigDirMovesOldName(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	old := filepath.Join(dir, "lazypantheon")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "config.yaml"), []byte("poll_interval: 30s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollInterval != Duration(30*time.Second) {
		t.Errorf("poll_interval = %v, old config not carried over", cfg.PollInterval)
	}
	if _, err := os.Stat(old); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("old dir still present: %v", err)
	}
}
