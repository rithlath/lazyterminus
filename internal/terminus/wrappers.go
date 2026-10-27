package terminus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// History appends every mutating command it passes through to a log file:
// one line when it starts, one with the result.
type History struct {
	Inner Runner
	Path  string
	Now   func() time.Time

	mu sync.Mutex
}

// HistoryPath returns the log path inside stateDir.
func HistoryPath(stateDir string) string { return filepath.Join(stateDir, "history.log") }

func currentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "unknown"
}

func (h *History) log(event string, args []string) error {
	if h.Path == "" {
		return nil
	}
	now := time.Now
	if h.Now != nil {
		now = h.Now
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(h.Path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(h.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "%s\tuser=%s\t%s\t%s\n", now().UTC().Format(time.RFC3339), currentUser(), event, Args(args).String())
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// ErrHistory wraps a failure to write the audit log. Mutating commands are
// refused rather than run unlogged.
type ErrHistory struct{ Err error }

func (e *ErrHistory) Error() string { return "cannot write history log: " + e.Err.Error() }
func (e *ErrHistory) Unwrap() error { return e.Err }

func result(err error) string {
	if err == nil {
		return "ok"
	}
	var ce *CommandError
	if errors.As(err, &ce) {
		return fmt.Sprintf("failed(exit=%d)", ce.ExitCode)
	}
	return "failed"
}

// Run implements Runner.
func (h *History) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	if !IsMutating(args) {
		return h.Inner.Run(ctx, args...)
	}
	if err := h.log("start", args); err != nil {
		return nil, nil, &ErrHistory{err}
	}
	out, stderr, err := h.Inner.Run(ctx, args...)
	_ = h.log(result(err), args) // the command already ran; don't mask its result
	return out, stderr, err
}

// Stream implements Runner.
func (h *History) Stream(ctx context.Context, args ...string) (<-chan string, <-chan error) {
	if !IsMutating(args) {
		return h.Inner.Stream(ctx, args...)
	}
	if err := h.log("start", args); err != nil {
		lines, errc := make(chan string), make(chan error, 1)
		close(lines)
		errc <- &ErrHistory{err}
		close(errc)
		return lines, errc
	}
	lines, inErr := h.Inner.Stream(ctx, args...)
	errc := make(chan error, 1)
	go func() {
		err := <-inErr
		_ = h.log(result(err), args)
		errc <- err
		close(errc)
	}()
	return lines, errc
}

// Command implements Execer. Interactive commands are logged when handed
// to the terminal; their exit status is not observed here.
func (h *History) Command(ctx context.Context, args ...string) (*exec.Cmd, error) {
	if IsMutating(args) {
		if err := h.log("exec", args); err != nil {
			return nil, &ErrHistory{err}
		}
	}
	return CommandFor(h.Inner, ctx, args...)
}

// DryRun passes read-only commands through and prints mutating ones instead
// of running them.
type DryRun struct {
	Inner Runner

	mu      sync.Mutex
	printed [][]string
}

// Printed returns every mutating command that was intercepted.
func (d *DryRun) Printed() [][]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.printed)
}

func (d *DryRun) intercept(args []string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.printed = append(d.printed, slices.Clone(args))
	return "[dry-run] " + Args(args).String()
}

// Run implements Runner.
func (d *DryRun) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	if !IsMutating(args) {
		return d.Inner.Run(ctx, args...)
	}
	return []byte(d.intercept(args) + "\n"), nil, nil
}

// Stream implements Runner.
func (d *DryRun) Stream(ctx context.Context, args ...string) (<-chan string, <-chan error) {
	if !IsMutating(args) {
		return d.Inner.Stream(ctx, args...)
	}
	lines, errc := make(chan string, 1), make(chan error, 1)
	lines <- d.intercept(args)
	close(lines)
	errc <- nil
	close(errc)
	return lines, errc
}

// Command implements Execer: interactive mutating commands are printed in
// the terminal instead of run.
func (d *DryRun) Command(ctx context.Context, args ...string) (*exec.Cmd, error) {
	if !IsMutating(args) {
		return CommandFor(d.Inner, ctx, args...)
	}
	msg := d.intercept(args)
	return exec.CommandContext(ctx, "sh", "-c", `printf '%s\n\nPress enter to return to lazyterminus...' "$1"; read -r _`, "sh", msg), nil
}
