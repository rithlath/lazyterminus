package ui

import "testing"

func TestHistoryShowsReadsAndChanges(t *testing.T) {
	h := start(t, Options{Site: "acme-web", Env: "acme-web.dev"}, 140, 40)
	h.waitText("[3] acme-web.dev")
	h.keys("h")
	h.waitText("History · last 15 min")
	h.waitText("read") // background loads are listed
	h.waitText("env:list acme-web")
	h.keys("f")
	h.waitText("No changes in the last 15 min.")
	h.keys("f", "t")
	h.waitText("History · last hour")
	h.keys("esc")
	h.waitText("[3] acme-web.dev")

	// A change made from the app shows up as one.
	h.keys("c")
	h.waitText("Clear cache")
	h.keys("y")
	h.waitText("Clear cache on acme-web.dev finished")
	h.keys("h", "f")
	h.waitText("1 change")
	h.waitText("env:clear-cache acme-web.dev")
}
