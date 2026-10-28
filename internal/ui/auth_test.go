package ui

import (
	"strings"
	"testing"
)

func TestShowsLoggedInAccount(t *testing.T) {
	h := start(t, Options{Site: "acme-web"}, 130, 40)
	h.waitText("dev@example.com")
	h.waitText("feedback")
}

func TestNotLoggedInHintAndRecovery(t *testing.T) {
	mock := testMock(t)
	mock.SetLoggedIn(false)
	h := startWith(t, mock, Options{}, false, 130, 40)
	h.waitText("Run terminus auth:login --machine-token=<token> in another pane, then press r on Sites.")
	h.waitText("Not logged in to Terminus. In another pane run")
	h.waitText("then press r here.")
	h.waitText("No site selected.")
	h.waitText("✗ not logged in")
	if strings.Contains(h.screen(), "dev@example.com") {
		t.Error("account shown while logged out")
	}
	// Log in elsewhere, then r on the Sites panel.
	mock.SetLoggedIn(true)
	h.keys("1", "r")
	h.waitText("acme-web")
	h.waitText("dev@example.com")
	h.waitFor("logged-out badge cleared", func(s string) bool { return !strings.Contains(s, "✗ not logged in") })
}

func TestSessionExpiryMidUse(t *testing.T) {
	mock := testMock(t)
	h := startWith(t, mock, Options{Site: "acme-web"}, false, 130, 40)
	h.waitText("dev@example.com")
	h.waitText("feedback")
	mock.SetLoggedIn(false)
	h.keys("4", "r") // refresh workflows
	h.waitText("Run terminus auth:login --machine-token=<token>")
	h.waitText("✗ not logged in")
	h.keys("O")
	h.waitText("workflow:list acme-web: terminus workflow:list: You are not logged in.")
}

// Logging in elsewhere clears the badge on the next successful call, without
// r on the Sites panel.
func TestBadgeClearsAfterLoginElsewhere(t *testing.T) {
	mock := testMock(t)
	h := startWith(t, mock, Options{Site: "acme-web"}, false, 130, 40)
	h.waitText("dev@example.com")
	h.waitText("feedback")
	mock.SetLoggedIn(false)
	h.keys("4", "r")
	h.waitText("✗ not logged in")
	mock.SetLoggedIn(true)
	h.keys("r") // workflow:list again, still on the Workflows panel
	h.waitFor("logged-out badge cleared", func(s string) bool {
		return !strings.Contains(s, "✗ not logged in") && strings.Contains(s, "dev@example.com")
	})
}
