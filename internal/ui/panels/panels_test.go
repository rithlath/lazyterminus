package panels

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/rithlath/lazyterminus/internal/terminus"
	"github.com/rithlath/lazyterminus/internal/ui/theme"
)

// A workflow Pantheon never finished must not render as a success.
func TestWorkflowsSupersededNeverFinished(t *testing.T) {
	env := "cx-form"
	now := time.Unix(1793803412, 0)
	d := WorkflowsData{
		Now: now,
		Workflows: []terminus.Workflow{
			{ID: "a", Env: &env, Workflow: `Sync code on "cx-form"`, Status: terminus.StatusSucceeded, StartedAt: terminus.Epoch(1793660270)},
			{ID: "b", Env: &env, Workflow: `Sync code on "cx-form"`, Status: terminus.StatusSuperseded, StartedAt: terminus.Epoch(1793652896)},
		},
	}
	lines := Workflows(d, &List{}, 80, 10, false)
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2: %q", len(lines), lines)
	}
	done, stuck := strings.TrimLeft(ansi.Strip(lines[0]), " "), strings.TrimLeft(ansi.Strip(lines[1]), " ")
	if !strings.HasPrefix(done, theme.SymSuccess+" ") || strings.Contains(done, "never finished") {
		t.Errorf("succeeded row = %q", done)
	}
	if !strings.HasPrefix(stuck, theme.SymUnknown+" ") || !strings.Contains(stuck, "cx-form · never finished · ") {
		t.Errorf("superseded row = %q, want %s … cx-form · never finished · <ago>", stuck, theme.SymUnknown)
	}
}

// A row whose data is still loading shows the spinner where its state dot
// goes, without shifting the name column.
func TestEnvsLoadingRowShowsSpinner(t *testing.T) {
	rows := []EnvRow{
		{Name: "dev", State: EnvOK, Ahead: -1, Changes: -1},
		{Name: "test", State: EnvOK, Ahead: -1, Changes: -1, Loading: true},
		{Name: "live", State: EnvOK, Ahead: -1, Changes: -1},
	}
	lines := Envs(rows, &List{}, 40, 5, false, "", "⣾")
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3: %q", len(lines), lines)
	}
	loading, loaded := ansi.Strip(lines[1]), ansi.Strip(lines[2])
	if !strings.HasPrefix(loading, "  ⣾ test") {
		t.Errorf("loading row = %q, want the spinner before the name", loading)
	}
	if !strings.HasPrefix(loaded, "  "+theme.SymOK+" live") {
		t.Errorf("loaded row = %q, want its state dot", loaded)
	}
}

// A site whose environment count is unknown shows "…" only while its list
// is loading; other sites' lists load when they are selected.
func TestSitesUnknownCountOnlyShowsWhileLoading(t *testing.T) {
	rows := []SiteRow{
		{Name: "idle", Framework: "drupal8", EnvCount: -1},
		{Name: "busy", Framework: "drupal8", EnvCount: -1, Loading: true},
		{Name: "known", Framework: "drupal8", EnvCount: 3},
	}
	lines := Sites(rows, &List{Cursor: 2}, 40, 5, false, "")
	idle, busy := ansi.Strip(lines[0]), ansi.Strip(lines[1])
	if strings.Contains(idle, "…") {
		t.Errorf("idle row = %q, want no loading mark", idle)
	}
	if !strings.Contains(busy, "…") {
		t.Errorf("busy row = %q, want a loading mark", busy)
	}
	if known := ansi.Strip(lines[2]); !strings.Contains(known, "3 envs") {
		t.Errorf("known row = %q, want its count", known)
	}
}
