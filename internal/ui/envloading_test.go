package ui

import (
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/rithlath/lazyterminus/internal/terminus"
)

// A row is loading while a load its badges depend on is in flight: its own
// code log or diffstat, or the code log its ahead count is measured against.
func TestEnvRowLoadingFollowsInFlightLoads(t *testing.T) {
	envs := []terminus.Env{
		{ID: "dev", Initialized: true}, {ID: "test", Initialized: true}, {ID: "live", Initialized: true},
		{ID: "feat", Initialized: true}, {ID: "other", Initialized: true},
	}
	for _, tc := range []struct {
		inflight string
		want     []string
	}{
		{"", nil},
		{"wfs", nil},
		{"codelog:feat", []string{"feat"}},
		{"diffstat:feat", []string{"feat"}},
		{"codelog:dev", []string{"dev", "test", "feat", "other"}},
		{"codelog:test", []string{"test", "live"}},
		{"codelog:live", []string{"live"}},
	} {
		m := New(Options{Runner: testMock(t), Config: testConfig()})
		st := m.state("s")
		if tc.inflight != "" {
			st.begin(tc.inflight)
		}
		var got []string
		for _, e := range envs {
			if m.envRow("s", st, e).Loading {
				got = append(got, e.ID)
			}
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("in flight %q: loading rows = %v, want %v", tc.inflight, got, tc.want)
		}
	}
}

// The glyph drawn in an environment row is one cell wide (the spinner's own
// frames carry a trailing space) and advances with the spinner.
func TestEnvSpinnerGlyph(t *testing.T) {
	m := New(Options{Runner: testMock(t), Config: testConfig()})
	first := m.envSpinner()
	if w := ansi.StringWidth(first); w != 1 {
		t.Errorf("glyph %q is %d cells wide, want 1", ansi.Strip(first), w)
	}
	m.pulse++
	if next := m.envSpinner(); ansi.Strip(next) == ansi.Strip(first) {
		t.Errorf("glyph did not advance: %q", ansi.Strip(next))
	}
}

// With slow Terminus calls, rows spin while their code logs load and settle
// on a state dot once the data is in.
func TestEnvRowsSpinUntilLoaded(t *testing.T) {
	mock := testMock(t)
	mock.MinLatency, mock.MaxLatency = 200*time.Millisecond, 200*time.Millisecond
	h := startWith(t, mock, Options{Site: "acme-web"}, false, 130, 40)
	spinning := regexp.MustCompile(`[⣾⣽⣻⢿⡿⣟⣯⣷] (dev|test|live|feedback)\b`)
	h.waitFor("a spinning environment row", spinning.MatchString)
	settled := regexp.MustCompile(`● live +git ↑1`)
	h.waitFor("settled environment rows", func(sc string) bool {
		return settled.MatchString(sc) && !spinning.MatchString(sc)
	})
}
