package terminus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// stubTerminus writes a fake terminus binary that echoes its arguments and
// fails when asked to. Tests never run the real terminus.
func stubTerminus(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "terminus")
	script := `#!/bin/sh
case "$1" in
  fail) echo "[notice] working" >&2; echo " [error]  Something broke. " >&2; exit 1 ;;
  stream) echo one; echo two >&2; echo three; exit 0 ;;
  sleep) sleep 0.2; echo done; exit 0 ;;
esac
printf '%s\n' "$@"
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExecRunnerRunAddsNoInteraction(t *testing.T) {
	r := ExecRunner{Path: stubTerminus(t)}
	out, _, err := r.Run(context.Background(), "remote:drush", "s.dev", "--", "status")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(string(out))
	want := []string{"remote:drush", "s.dev", "--no-interaction", "--", "status"}
	if !slices.Equal(got, want) {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestExecRunnerError(t *testing.T) {
	r := ExecRunner{Path: stubTerminus(t)}
	_, _, err := r.Run(context.Background(), "fail")
	var ce *CommandError
	if !errors.As(err, &ce) || ce.ExitCode != 1 || ce.Message != "Something broke." {
		t.Fatalf("err = %#v", err)
	}
}

func TestExecRunnerStream(t *testing.T) {
	r := ExecRunner{Path: stubTerminus(t)}
	lines, errc := r.Stream(context.Background(), "stream")
	var got []string
	for l := range lines {
		got = append(got, l)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"one", "three", "two"}) {
		t.Errorf("lines = %q", got)
	}
}

func TestExecRunnerStreamError(t *testing.T) {
	r := ExecRunner{Path: stubTerminus(t)}
	lines, errc := r.Stream(context.Background(), "fail")
	for range lines {
	}
	var ce *CommandError
	if err := <-errc; !errors.As(err, &ce) || ce.Message != "Something broke." {
		t.Fatalf("err = %v", err)
	}
}

type countingRunner struct {
	cur, peak atomic.Int32
}

func (c *countingRunner) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	n := c.cur.Add(1)
	for {
		p := c.peak.Load()
		if n <= p || c.peak.CompareAndSwap(p, n) {
			break
		}
	}
	time.Sleep(20 * time.Millisecond)
	c.cur.Add(-1)
	return nil, nil, nil
}

func (c *countingRunner) Stream(ctx context.Context, args ...string) (<-chan string, <-chan error) {
	return nil, nil
}

func TestLimitedBoundsConcurrency(t *testing.T) {
	inner := &countingRunner{}
	l := NewLimited(inner, 2)
	done := make(chan struct{})
	for range 8 {
		go func() {
			_, _, _ = l.Run(context.Background(), "site:list")
			done <- struct{}{}
		}()
	}
	for range 8 {
		<-done
	}
	if p := inner.peak.Load(); p > 2 {
		t.Errorf("peak concurrency %d > 2", p)
	}
}

func TestLimitedHonorsContext(t *testing.T) {
	l := NewLimited(ExecRunner{Path: stubTerminus(t)}, 1)
	go func() { _, _, _ = l.Run(context.Background(), "sleep") }()
	time.Sleep(20 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, _, err := l.Run(ctx, "x"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
}

// Real Terminus prints only a stderr notice for whoami without a session.
func TestWhoAmIWithStubTerminus(t *testing.T) {
	p := filepath.Join(t.TempDir(), "terminus")
	script := "#!/bin/sh\nif [ -f \"$0.in\" ]; then echo me@example.com; else echo ' [notice] You are not logged in.' >&2; fi\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c := NewClient(ExecRunner{Path: p})
	if _, err := c.WhoAmI(context.Background()); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("logged out: %v", err)
	}
	if err := os.WriteFile(p+".in", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if who, err := c.WhoAmI(context.Background()); err != nil || who != "me@example.com" {
		t.Errorf("logged in: %q %v", who, err)
	}
}
