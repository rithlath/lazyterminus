package terminus

import (
	"context"
	"testing"
)

func TestRecorderKeepsEveryCommand(t *testing.T) {
	m := newTestMock(t)
	r := &Recorder{Inner: m}
	ctx := context.Background()
	if _, _, err := r.Run(ctx, "site:list", "--format=json"); err != nil {
		t.Fatal(err)
	}
	m.FailNext("env:wake", "nope")
	_, _, _ = r.Run(ctx, "env:wake", "acme-web.dev")
	lines, errc := r.Stream(ctx, "env:clear-cache", "acme-web.dev")
	for range lines {
	}
	<-errc

	got := r.Entries()
	if len(got) != 3 {
		t.Fatalf("entries = %d, want 3", len(got))
	}
	cc, wake, list := got[0], got[1], got[2] // newest first
	if Args(list.Args).Command() != "site:list" || list.Mutating || !list.Done || list.Err != nil {
		t.Errorf("site:list = %+v", list)
	}
	if !wake.Mutating || !wake.Done || wake.Err == nil {
		t.Errorf("env:wake = %+v, want a recorded failure", wake)
	}
	if Args(cc.Args).Command() != "env:clear-cache" || !cc.Done || cc.Err != nil {
		t.Errorf("stream = %+v", cc)
	}
}

func TestRecorderDropsOldest(t *testing.T) {
	r := &Recorder{Inner: newTestMock(t), Max: 2}
	ctx := context.Background()
	for _, c := range []string{"site:list", "auth:whoami", "env:list"} {
		_, _, _ = r.Run(ctx, c)
	}
	got := r.Entries()
	if len(got) != 2 || Args(got[0].Args).Command() != "env:list" || Args(got[1].Args).Command() != "auth:whoami" {
		t.Fatalf("entries = %+v", got)
	}
}
