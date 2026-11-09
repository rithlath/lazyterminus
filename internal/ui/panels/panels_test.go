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
