package terminus

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// Runner executes Terminus commands. args never include the binary name.
type Runner interface {
	// Run executes a command to completion and returns its output.
	Run(ctx context.Context, args ...string) (stdout []byte, stderr []byte, err error)
	// Stream executes a command and sends each line of combined output as it
	// arrives. The line channel closes when the command exits; the error
	// channel then receives exactly one value (nil on success) and closes.
	Stream(ctx context.Context, args ...string) (<-chan string, <-chan error)
}

// Execer is implemented by runners that can hand the real terminal to a
// command (for tea.ExecProcess), e.g. remote:drush.
type Execer interface {
	Command(ctx context.Context, args ...string) (*exec.Cmd, error)
}

// CommandError is returned when Terminus exits non-zero. Message is the most
// useful line from stderr.
type CommandError struct {
	Args     []string
	ExitCode int
	Message  string
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("terminus %s: %s", Args(e.Args).Command(), e.Message)
}

// Args is a Terminus argument list.
type Args []string

// Command returns the command name (first non-flag argument).
func (a Args) Command() string {
	for _, s := range a {
		if !strings.HasPrefix(s, "-") {
			return s
		}
	}
	return ""
}

// Positionals returns non-flag arguments after the command name, stopping
// interpretation of flags after a bare "--".
func (a Args) Positionals() []string {
	var out []string
	seenCmd, rest := false, false
	for _, s := range a {
		switch {
		case rest:
			out = append(out, s)
		case s == "--":
			rest = true
		case strings.HasPrefix(s, "-"):
		case !seenCmd:
			seenCmd = true
		default:
			out = append(out, s)
		}
	}
	return out
}

// Flag returns the value of --name=value, or "true" for a bare --name.
func (a Args) Flag(name string) (string, bool) {
	for _, s := range a {
		if s == "--" {
			break
		}
		if s == "--"+name {
			return "true", true
		}
		if v, ok := strings.CutPrefix(s, "--"+name+"="); ok {
			return v, true
		}
	}
	return "", false
}

// HasFlag reports whether --name or --name=value is present.
func (a Args) HasFlag(name string) bool { _, ok := a.Flag(name); return ok }

// String renders the args as a shell-quoted terminus command line.
func (a Args) String() string { return "terminus " + ShellJoin(a) }

// ShellJoin quotes args for display and logs so they can be pasted into a
// POSIX shell.
func ShellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, s := range args {
		parts[i] = shellQuote(s)
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.,:/=@+%^", r)
	}
	if strings.IndexFunc(s, func(r rune) bool { return !safe(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ExecRunner runs the real terminus binary.
type ExecRunner struct {
	Path string // binary path; "terminus" if empty
}

func (r ExecRunner) path() string {
	if r.Path == "" {
		return "terminus"
	}
	return r.Path
}

// withNoInteraction appends --no-interaction so Terminus never blocks on a
// prompt the TUI can't answer. It goes before any "--" separator.
func withNoInteraction(args []string) []string {
	out := make([]string, 0, len(args)+1)
	for i, s := range args {
		if s == "--" {
			out = append(out, "--no-interaction")
			return append(out, args[i:]...)
		}
		out = append(out, s)
	}
	if len(args) > 0 && args[0] == "--version" {
		return out
	}
	return append(out, "--no-interaction")
}

// Run implements Runner.
func (r ExecRunner) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, r.path(), withNoInteraction(args)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), wrapExit(args, err, stderr.Bytes())
}

// Stream implements Runner.
func (r ExecRunner) Stream(ctx context.Context, args ...string) (<-chan string, <-chan error) {
	lines, errc := make(chan string, 64), make(chan error, 1)
	cmd := exec.CommandContext(ctx, r.path(), withNoInteraction(args)...)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	var tail tailBuffer
	go func() {
		defer close(errc)
		if err := cmd.Start(); err != nil {
			close(lines)
			errc <- err
			return
		}
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			sc := bufio.NewScanner(pr)
			sc.Buffer(make([]byte, 64*1024), 1024*1024)
			for sc.Scan() {
				tail.add(sc.Text())
				lines <- sc.Text()
			}
			_, _ = io.Copy(io.Discard, pr)
		}()
		err := cmd.Wait()
		_ = pw.Close()
		wg.Wait()
		close(lines)
		errc <- wrapExit(args, err, []byte(tail.String()))
	}()
	return lines, errc
}

// Command implements Execer. The returned command inherits the terminal.
func (r ExecRunner) Command(ctx context.Context, args ...string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, r.path(), args...), nil
}

func wrapExit(args []string, err error, stderr []byte) error {
	if err == nil {
		return nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return &CommandError{Args: args, ExitCode: ee.ExitCode(), Message: ErrorMessage(stderr)}
	}
	return err
}

// ErrorMessage extracts the most relevant message from Terminus stderr:
// the last "[error]" line, else the last non-empty line.
func ErrorMessage(stderr []byte) string {
	var last, lastErr string
	for l := range strings.SplitSeq(string(stderr), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		last = l
		if i := strings.Index(l, "[error]"); i >= 0 {
			lastErr = strings.TrimSpace(l[i+len("[error]"):])
		}
	}
	if lastErr != "" {
		return lastErr
	}
	if last == "" {
		return "command failed"
	}
	return last
}

type tailBuffer struct {
	mu    sync.Mutex
	lines []string
}

func (t *tailBuffer) add(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, s)
	if len(t.lines) > 50 {
		t.lines = t.lines[1:]
	}
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}

// Limited bounds the number of concurrent Terminus processes.
type Limited struct {
	Inner Runner
	sem   chan struct{}
}

// NewLimited wraps r so at most n commands run at once.
func NewLimited(r Runner, n int) *Limited {
	if n < 1 {
		n = 1
	}
	return &Limited{Inner: r, sem: make(chan struct{}, n)}
}

func (l *Limited) acquire(ctx context.Context) error {
	select {
	case l.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Run implements Runner.
func (l *Limited) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	if err := l.acquire(ctx); err != nil {
		return nil, nil, err
	}
	defer func() { <-l.sem }()
	return l.Inner.Run(ctx, args...)
}

// Stream implements Runner. The slot is held until the stream finishes.
func (l *Limited) Stream(ctx context.Context, args ...string) (<-chan string, <-chan error) {
	if err := l.acquire(ctx); err != nil {
		lines, errc := make(chan string), make(chan error, 1)
		close(lines)
		errc <- err
		close(errc)
		return lines, errc
	}
	inLines, inErr := l.Inner.Stream(ctx, args...)
	errc := make(chan error, 1)
	go func() {
		err := <-inErr
		<-l.sem
		errc <- err
		close(errc)
	}()
	return inLines, errc
}

// Command implements Execer. Interactive commands don't take a slot: they
// own the terminal while they run and the TUI is suspended.
func (l *Limited) Command(ctx context.Context, args ...string) (*exec.Cmd, error) {
	return CommandFor(l.Inner, ctx, args...)
}

// CommandFor asks r for an interactive command, failing if r can't provide one.
func CommandFor(r Runner, ctx context.Context, args ...string) (*exec.Cmd, error) {
	if e, ok := r.(Execer); ok {
		return e.Command(ctx, args...)
	}
	return nil, fmt.Errorf("runner %T cannot run interactive commands", r)
}
