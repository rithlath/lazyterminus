package catalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/rithlath/lazyterminus/internal/terminus"
)

func realCatalog(t *testing.T) *Catalog {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "real", "list.json"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestParseRealCatalog(t *testing.T) {
	c := realCatalog(t)
	if c.Version != "4.2.0" || len(c.Commands) != 158 {
		t.Fatalf("version %q, %d commands", c.Version, len(c.Commands))
	}
	deploy, ok := c.Get("env:deploy")
	if !ok {
		t.Fatal("env:deploy missing")
	}
	if deploy.Scope() != ScopeEnv || len(deploy.Args) != 1 || !deploy.Args[0].Required {
		t.Errorf("deploy = %+v", deploy)
	}
	var names []string
	for _, o := range deploy.Options {
		names = append(names, o.Name)
	}
	if !slices.Equal(names, []string{"cc", "note", "sync-content", "updatedb"}) {
		t.Errorf("deploy options = %v (globals must be dropped)", names)
	}
	if !slices.Contains(deploy.Aliases, "deploy") {
		t.Errorf("aliases = %v", deploy.Aliases)
	}
	if got, _ := c.Get("deploy"); got.Name != "env:deploy" {
		t.Error("alias lookup failed")
	}
	drush, _ := c.Get("remote:drush")
	if len(drush.Args) != 2 || !drush.Args[1].IsArray || drush.Args[1].Required || !drush.Interactive() {
		t.Errorf("drush = %+v", drush)
	}
	backup, _ := c.Get("backup:create")
	for _, o := range backup.Options {
		if o.Name == "element" && !slices.Equal(o.Choices, []string{"all", "code", "files", "database", "db"}) {
			t.Errorf("element choices = %v", o.Choices)
		}
	}
	envList, _ := c.Get("env:list")
	if envList.Scope() != ScopeSite || !envList.SupportsJSON() {
		t.Errorf("env:list scope=%v json=%v", envList.Scope(), envList.SupportsJSON())
	}
	cc, _ := c.Get("env:clear-cache")
	if cc.SupportsJSON() {
		t.Error("env:clear-cache has no --format")
	}
	// Commands with no arguments (arguments: [])
	whoami, _ := c.Get("auth:whoami")
	if len(whoami.Args) != 0 || whoami.Scope() != ScopeNone {
		t.Errorf("whoami = %+v", whoami)
	}
}

func TestEveryCommandIsFindable(t *testing.T) {
	c := realCatalog(t)
	for _, cmd := range c.Commands {
		if cmd.Hidden {
			continue
		}
		res := c.Search(cmd.Name, ScopeNone)
		if len(res) == 0 || res[0].Command.Name != cmd.Name {
			top := ""
			if len(res) > 0 {
				top = res[0].Command.Name
			}
			t.Errorf("search %q: top result %q", cmd.Name, top)
		}
	}
}

func TestSearchScopeAndDescriptions(t *testing.T) {
	c := realCatalog(t)
	all := c.Search("", ScopeEnv)
	if !all[0].Scoped || all[0].Command.Scope() != ScopeEnv {
		t.Errorf("env-scoped commands should come first, got %s", all[0].Command.Name)
	}
	for i := 1; i < len(all); i++ {
		if all[i].Scoped && !all[i-1].Scoped {
			t.Fatal("scoped commands not grouped first")
		}
	}
	if res := c.Search("clear cache", ScopeEnv); len(res) == 0 || res[0].Command.Name != "env:clear-cache" {
		t.Errorf("'clear cache' → %v", res[:min(3, len(res))])
	}
	if res := c.Search("cc", ScopeEnv); len(res) == 0 || res[0].Command.Name != "env:clear-cache" {
		t.Errorf("alias 'cc' top = %s", res[0].Command.Name)
	}
	for _, m := range c.Search("", ScopeNone) {
		if m.Command.Hidden {
			t.Errorf("hidden %s listed", m.Command.Name)
		}
	}
}

func TestResolveFeedsSafety(t *testing.T) {
	c := realCatalog(t)
	if n, ok := c.Resolve("env:cc"); !ok || n != "env:clear-cache" {
		t.Errorf("env:cc → %q", n)
	}
	if n, ok := c.Resolve("import:db"); !ok || n != "import:database" {
		t.Errorf("import:db → %q", n)
	}
}

func TestLoadCachesByVersion(t *testing.T) {
	m, err := terminus.NewMockRunner(os.DirFS(filepath.Join("..", "..", "testdata")))
	if err != nil {
		t.Fatal(err)
	}
	m.MinLatency, m.MaxLatency, m.WorkflowDuration = 0, 0, time.Millisecond
	dir := t.TempDir()
	countList := func() int {
		n := 0
		for _, c := range m.Calls() {
			if len(c.Args) > 0 && c.Args[0] == "list" {
				n++
			}
		}
		return n
	}
	cat, cached, err := Load(context.Background(), m, dir, false)
	if err != nil || cached || countList() != 1 {
		t.Fatalf("first load: cached=%v err=%v lists=%d", cached, err, countList())
	}
	if _, ok := cat.Get("build:env:list"); !ok {
		t.Error("plugin command missing from mock catalog")
	}
	if _, cached, _ := Load(context.Background(), m, dir, false); !cached || countList() != 1 {
		t.Errorf("second load should come from cache (lists=%d)", countList())
	}
	// A different Terminus version invalidates the cache.
	b, _ := os.ReadFile(filepath.Join(dir, "catalog.json"))
	var cf cacheFile
	_ = json.Unmarshal(b, &cf)
	cf.Version = "3.6.0"
	b, _ = json.Marshal(cf)
	_ = os.WriteFile(filepath.Join(dir, "catalog.json"), b, 0o644)
	if _, cached, _ := Load(context.Background(), m, dir, false); cached || countList() != 2 {
		t.Errorf("version change should reload (cached=%v lists=%d)", cached, countList())
	}
	if _, cached, _ := Load(context.Background(), m, dir, true); cached || countList() != 3 {
		t.Error("force should reload")
	}
}
