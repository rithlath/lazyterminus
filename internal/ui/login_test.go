package ui

import (
	"slices"
	"testing"
)

func TestPaletteLoginAsCopiesLink(t *testing.T) {
	copied, browsed := &opened{}, &opened{}
	h := start(t, Options{Site: "acme-web", Env: "acme-web.proj-19", Clipboard: copied.open, OpenURL: browsed.open}, 140, 44)
	h.waitText("[3] acme-web.proj-19")
	h.keys(":")
	h.typeText("login")
	h.waitText("◆ login-as")
	h.keys("enter")
	h.waitText("Log in as · acme-web.proj-19")
	h.typeText("jdoe")
	h.keys("tab")
	h.typeText("/webform/ewt_impact_award_2027")
	h.waitText("terminus remote:drush acme-web.proj-19 -- uli --name=jdoe webform/ewt_impact_award_2027")
	h.keys("enter")
	h.waitText("Run it? y/n")
	h.keys("y")
	calls := h.waitCalls(2)
	if calls[0][0] != "env:wake" {
		t.Fatalf("calls = %v", calls)
	}
	want := []string{"remote:drush", "acme-web.proj-19", "--", "uli", "--name=jdoe", "webform/ewt_impact_award_2027"}
	if !slices.Equal(calls[1], want) {
		t.Errorf("drush = %q", calls[1])
	}
	h.waitText("Copied the login link for jdoe to the clipboard.")
	link := "https://proj-19-acme-web.pantheonsite.io/user/reset/1/1700000000/mock-hash/login?destination=webform/ewt_impact_award_2027"
	if got := copied.last(); got != link {
		t.Errorf("copied %q", got)
	}
	if got := browsed.last(); got != "" {
		t.Errorf("opened %q without being asked", got)
	}
}

func TestLoginAsFromMenuOpensBrowser(t *testing.T) {
	copied, browsed := &opened{}, &opened{}
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev", Clipboard: copied.open, OpenURL: browsed.open}, 140, 44)
	h.waitText("[3] acme-web.dev")
	h.keys("enter")
	h.waitText("Log in as a user (one-time link)")
	h.click("Log in as a user")
	h.waitText("Log in as · acme-web.dev")
	h.keys("tab", "tab", "space")
	h.waitText("terminus remote:drush acme-web.dev -- uli")
	h.keys("enter", "y")
	h.waitText("Copied the login link for user 1 to the clipboard.")
	link := "https://dev-acme-web.pantheonsite.io/user/reset/1/1700000000/mock-hash/login"
	if got := copied.last(); got != link {
		t.Errorf("copied %q", got)
	}
	h.waitFor("browser opened the link", func(string) bool { return browsed.last() == link })
}
