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
	"strings"
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
	return exec.CommandContext(ctx, "sh", "-c", `printf '%s\n' "$1"`, "sh", msg), nil
}

// Logged is one command from the history log, with its start and result
// lines folded together.
type Logged struct {
	At      time.Time
	User    string
	Command string // as logged: "terminus remote:drush site.live -- cim -y"
	// Result is "ok", "failed…" (e.g. "failed(exit=1)"), "exec" for a
	// command handed to the terminal (its exit isn't logged), or "" while
	// only its start is logged (running, or lazyterminus was killed).
	Result string
}

// Involves reports whether the command names site.env as an argument.
func (l Logged) Involves(siteEnv string) bool {
	return slices.Contains(strings.Fields(l.Command), siteEnv)
}

// Describe says what the command did to siteEnv in plain words: "clear
// cache", "drush cim -y", "deploy --note=…"; anything else is the command
// with "terminus" and siteEnv left out.
func (l Logged) Describe(siteEnv string) string {
	f := strings.Fields(strings.TrimPrefix(l.Command, "terminus "))
	if len(f) == 0 {
		return l.Command
	}
	cmd, rest := f[0], slices.DeleteFunc(f[1:], func(s string) bool { return s == siteEnv })
	tail := func(words ...string) string { return strings.Join(append(words, rest...), " ") }
	switch cmd {
	case "remote:drush", "drush", "remote:wp", "wp":
		tool := strings.TrimPrefix(cmd, "remote:")
		if len(rest) > 0 && rest[0] == "--" {
			rest = rest[1:]
		}
		return tail(tool)
	case "env:clear-cache", "env:cc", "cc":
		return tail("clear cache")
	case "env:deploy", "deploy":
		return tail("deploy")
	case "env:wake":
		return tail("wake")
	case "env:commit":
		return tail("commit SFTP changes")
	case "backup:create":
		return tail("backup")
	case "connection:set":
		return tail("set connection mode")
	case "env:clone-content":
		if len(rest) > 0 { // source is siteEnv here; rest[0] is the target
			return "clone content to " + strings.Join(rest, " ")
		}
	}
	return tail(cmd)
}

// ParseHistoryLog reads the history log, oldest first. Lines it can't parse
// are skipped.
func ParseHistoryLog(data []byte) []Logged {
	var out []Logged
	pending := map[string]int{} // command → index of its unfinished start
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.SplitN(line, "\t", 4)
		if len(f) != 4 {
			continue
		}
		at, err := time.Parse(time.RFC3339, f[0])
		if err != nil {
			continue
		}
		event, cmd := f[2], f[3]
		switch {
		case event == "start":
			pending[cmd] = len(out)
			out = append(out, Logged{At: at, User: strings.TrimPrefix(f[1], "user="), Command: cmd})
		case event == "ok" || strings.HasPrefix(event, "failed"):
			if i, ok := pending[cmd]; ok {
				out[i].Result = event
				delete(pending, cmd)
			}
		case event == "exec":
			out = append(out, Logged{At: at, User: strings.TrimPrefix(f[1], "user="), Command: cmd, Result: "exec"})
		}
	}
	return out
}
