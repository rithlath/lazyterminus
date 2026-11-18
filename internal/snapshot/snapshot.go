// Package snapshot keeps the last site list and environment lists Terminus
// returned on disk, so the UI can draw them at startup before Terminus
// answers. env:list makes about two HTTP requests per environment, so a
// site with many multidevs takes well over ten seconds to list.
package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rithlath/lazyterminus/internal/terminus"
)

// Store reads and writes snapshots under Dir. An empty Dir disables it:
// reads find nothing and writes do nothing.
type Store struct{ Dir string }

// Sites returns the last saved site list.
func (s Store) Sites() ([]terminus.Site, bool) {
	return read[terminus.Site](s.sitesPath())
}

// SaveSites replaces the saved site list.
func (s Store) SaveSites(sites []terminus.Site) error {
	return write(s.sitesPath(), sites)
}

// Envs returns the last saved environment list for site.
func (s Store) Envs(site string) ([]terminus.Env, bool) {
	path, err := s.envsPath(site)
	if err != nil {
		return nil, false
	}
	return read[terminus.Env](path)
}

// SaveEnvs replaces the saved environment list for site.
func (s Store) SaveEnvs(site string, envs []terminus.Env) error {
	path, err := s.envsPath(site)
	if err != nil {
		return err
	}
	return write(path, envs)
}

func (s Store) sitesPath() string {
	if s.Dir == "" {
		return ""
	}
	return filepath.Join(s.Dir, "sites.json")
}

func (s Store) envsPath(site string) (string, error) {
	// The site name becomes a file name.
	if site == "" || site == "." || site == ".." || strings.ContainsAny(site, `/\`) {
		return "", fmt.Errorf("snapshot: unusable site name %q", site)
	}
	if s.Dir == "" {
		return "", nil
	}
	return filepath.Join(s.Dir, "envs", site+".json"), nil
}

func read[T any](path string) ([]T, bool) {
	if path == "" {
		return nil, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var rows []T
	if json.Unmarshal(b, &rows) != nil {
		return nil, false
	}
	return rows, true
}

// write replaces path through a temporary file, so a reader never sees a
// half-written snapshot.
func write(path string, rows any) error {
	if path == "" {
		return nil
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(b)
	if err = errors.Join(err, tmp.Close()); err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}
