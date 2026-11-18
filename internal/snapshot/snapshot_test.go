package snapshot

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rithlath/lazyterminus/internal/terminus"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "real", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// What Terminus returned must come back unchanged, including the float
// timestamps and the fields env:list only sometimes sets.
func TestSitesAndEnvsRoundTrip(t *testing.T) {
	sites, err := terminus.ParseSites(fixture(t, "site_list.json"))
	if err != nil || len(sites) == 0 {
		t.Fatalf("sites fixture: %d sites, %v", len(sites), err)
	}
	envs, err := terminus.ParseEnvs(fixture(t, "env_list_acme-web.json"))
	if err != nil || len(envs) == 0 {
		t.Fatalf("envs fixture: %d envs, %v", len(envs), err)
	}
	s := Store{Dir: filepath.Join(t.TempDir(), "not-created-yet")}
	if err := s.SaveSites(sites); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveEnvs("acme-web", envs); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Sites(); !ok || !reflect.DeepEqual(got, sites) {
		t.Errorf("sites = %+v %v, want %+v", got, ok, sites)
	}
	if got, ok := s.Envs("acme-web"); !ok || !reflect.DeepEqual(got, envs) {
		t.Errorf("envs = %+v %v, want %+v", got, ok, envs)
	}
}

func TestMissingOrCorruptSnapshotIsAbsent(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if got, ok := s.Sites(); ok || got != nil {
		t.Errorf("missing sites = %v %v", got, ok)
	}
	if got, ok := s.Envs("acme-web"); ok || got != nil {
		t.Errorf("missing envs = %v %v", got, ok)
	}
	if err := s.SaveEnvs("acme-web", []terminus.Env{{ID: "dev"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "envs", "acme-web.json"), []byte(`[{"id": "de`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Envs("acme-web"); ok || got != nil {
		t.Errorf("corrupt envs = %v %v", got, ok)
	}
}

// A newer list replaces the older one.
func TestSaveReplacesTheSnapshot(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for _, envs := range [][]terminus.Env{{{ID: "dev"}, {ID: "gone"}}, {{ID: "dev"}}} {
		if err := s.SaveEnvs("acme-web", envs); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := s.Envs("acme-web"); len(got) != 1 || got[0].ID != "dev" {
		t.Errorf("envs = %+v, want only dev", got)
	}
	left, _ := os.ReadDir(filepath.Join(s.Dir, "envs"))
	if len(left) != 1 {
		t.Errorf("envs dir has %d entries, want only acme-web.json", len(left))
	}
}

// A site name is used as a file name, so one that could leave the snapshot
// directory is refused.
func TestSiteNameCannotEscapeTheDirectory(t *testing.T) {
	root := t.TempDir()
	s := Store{Dir: filepath.Join(root, "snapshot")}
	for _, name := range []string{"../escaped", "a/b", "", ".", ".."} {
		if err := s.SaveEnvs(name, []terminus.Env{{ID: "dev"}}); err == nil {
			t.Errorf("SaveEnvs(%q) succeeded", name)
		}
		if _, ok := s.Envs(name); ok {
			t.Errorf("Envs(%q) found a snapshot", name)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "escaped.json")); err == nil {
		t.Error("a snapshot was written outside the directory")
	}
}

// With no directory the store does nothing.
func TestEmptyDirDisablesTheStore(t *testing.T) {
	var s Store
	if err := s.SaveSites([]terminus.Site{{Name: "x"}}); err != nil {
		t.Errorf("SaveSites = %v", err)
	}
	if _, ok := s.Sites(); ok {
		t.Error("a disabled store returned sites")
	}
}
