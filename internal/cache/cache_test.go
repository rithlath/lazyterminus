package cache

import (
	"errors"
	"testing"
	"time"
)

func TestTTLAndPrefix(t *testing.T) {
	now := time.Unix(0, 0)
	c := New()
	c.Now = func() time.Time { return now }
	c.Set("env|acme-web|dev", 1, 30*time.Second)
	c.Set("env|acme-web|test", 2, 30*time.Second)
	c.Set("env|other|dev", 3, time.Minute)
	if v, ok := c.Get("env|acme-web|dev"); !ok || v != 1 {
		t.Fatal("missing fresh entry")
	}
	now = now.Add(30 * time.Second)
	if _, ok := c.Get("env|acme-web|dev"); ok {
		t.Error("entry should expire at TTL")
	}
	c.DeletePrefix("env|other|")
	if _, ok := c.Get("env|other|dev"); ok {
		t.Error("prefix delete failed")
	}
	c.Set("zero", 1, 0)
	if _, ok := c.Get("zero"); ok {
		t.Error("zero TTL should not cache")
	}
}

func TestTyped(t *testing.T) {
	c := New()
	calls := 0
	load := func() (int, error) { calls++; return 42, nil }
	for range 3 {
		if v, _ := Typed(c, "k", time.Minute, false, load); v != 42 {
			t.Fatal(v)
		}
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
	_, _ = Typed(c, "k", time.Minute, true, load)
	if calls != 2 {
		t.Errorf("force did not reload")
	}
	_, err := Typed(c, "e", time.Minute, false, func() (int, error) { return 0, errors.New("x") })
	if err == nil {
		t.Fatal("want error")
	}
	if _, ok := c.Get("e"); ok {
		t.Error("errors must not be cached")
	}
}
