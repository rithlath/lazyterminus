package terminus

import (
	"context"
	"os/exec"
	"slices"
	"sync"
	"time"
)

// DefaultRecorderMax is how many commands a Recorder keeps when Max is 0.
// Polling runs a few commands a minute, so this covers several hours.
const DefaultRecorderMax = 2000

// Recorded is one command a Recorder saw run.
type Recorded struct {
	Args     []string
	Started  time.Time
	Duration time.Duration // zero while running
	Done     bool
	Err      error // nil on success
	Mutating bool
	// Interactive commands are handed to the terminal; their exit status
	// is not observed.
	Interactive bool
}

// Recorder keeps every command it passes through, reads included, in
// memory for the session's History view.
type Recorder struct {
	Inner Runner
	Max   int
	Now   func() time.Time

	mu      sync.Mutex
	entries []Recorded
	first   int // index of entries[0] since the recorder started
}

func (r *Recorder) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// begin records a starting command and returns its sequence number.
func (r *Recorder) begin(args []string, interactive bool) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, Recorded{Args: slices.Clone(args), Started: r.now(),
		Mutating: IsMutating(args), Interactive: interactive, Done: interactive})
	max := r.Max
	if max <= 0 {
		max = DefaultRecorderMax
	}
	if n := len(r.entries) - max; n > 0 {
		r.entries = slices.Delete(r.entries, 0, n)
		r.first += n
	}
	return r.first + len(r.entries) - 1
}

func (r *Recorder) end(seq int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := seq - r.first
	if i < 0 || i >= len(r.entries) {
		return // dropped while running
	}
	e := &r.entries[i]
	e.Duration, e.Done, e.Err = r.now().Sub(e.Started), true, err
}

// Entries returns the recorded commands, newest first.
func (r *Recorder) Entries() []Recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := slices.Clone(r.entries)
	slices.Reverse(out)
	return out
}

// Run implements Runner.
func (r *Recorder) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	seq := r.begin(args, false)
	out, stderr, err := r.Inner.Run(ctx, args...)
	r.end(seq, err)
	return out, stderr, err
}

// Stream implements Runner.
func (r *Recorder) Stream(ctx context.Context, args ...string) (<-chan string, <-chan error) {
	seq := r.begin(args, false)
	lines, inErr := r.Inner.Stream(ctx, args...)
	errc := make(chan error, 1)
	go func() {
		err := <-inErr
		r.end(seq, err)
		errc <- err
		close(errc)
	}()
	return lines, errc
}

// Command implements Execer.
func (r *Recorder) Command(ctx context.Context, args ...string) (*exec.Cmd, error) {
	cmd, err := CommandFor(r.Inner, ctx, args...)
	if err == nil {
		r.begin(args, true)
	}
	return cmd, err
}
