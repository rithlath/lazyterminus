// Package jobs runs confirmed Terminus commands in the background and
// reports progress to Bubble Tea as messages. A job is a sequence of steps
// (e.g. env:wake, then env:clone-content) run one after another.
package jobs

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rithlath/lazyterminus/internal/terminus"
)

// State of a job.
type State int

// Job states.
const (
	Running State = iota
	Succeeded
	Failed
)

// Job is one background operation.
type Job struct {
	ID      int
	Title   string
	Site    string
	Env     string
	Steps   [][]string
	Step    int // index of the running step
	State   State
	Started time.Time
	Ended   time.Time
	Err     error
	Output  []string

	events chan tea.Msg
	cancel context.CancelFunc
}

// Messages sent while a job runs. Each carries the job ID; the UI passes
// them to Queue.Apply and runs the returned command to keep listening.
type (
	// StepMsg reports that a step started.
	StepMsg struct {
		ID, Step int
		Args     []string
	}
	// LineMsg is one line of output.
	LineMsg struct {
		ID   int
		Line string
	}
	// DoneMsg reports that the job finished.
	DoneMsg struct {
		ID  int
		Err error
	}
)

// Queue tracks jobs. It is owned by the UI model and only mutated from
// Update; job goroutines communicate through messages.
type Queue struct {
	R   terminus.Runner
	Now func() time.Time

	jobs   []*Job
	nextID int
}

// New returns a queue that runs commands through r.
func New(r terminus.Runner) *Queue { return &Queue{R: r} }

func (q *Queue) now() time.Time {
	if q.Now != nil {
		return q.Now()
	}
	return time.Now()
}

// Start launches a job. ctx must carry any approval the steps need.
func (q *Queue) Start(ctx context.Context, title, site, env string, steps [][]string) (*Job, tea.Cmd) {
	q.nextID++
	ctx, cancel := context.WithCancel(ctx)
	j := &Job{ID: q.nextID, Title: title, Site: site, Env: env, Steps: steps, State: Running,
		Started: q.now(), events: make(chan tea.Msg, 64), cancel: cancel}
	q.jobs = append(q.jobs, j)
	go q.run(ctx, j)
	return j, listen(j.events)
}

func (q *Queue) run(ctx context.Context, j *Job) {
	defer close(j.events)
	defer j.cancel()
	for i, step := range j.Steps {
		j.events <- StepMsg{ID: j.ID, Step: i, Args: step}
		lines, errc := q.R.Stream(ctx, step...)
		for l := range lines {
			j.events <- LineMsg{ID: j.ID, Line: l}
		}
		if err := <-errc; err != nil {
			j.events <- DoneMsg{ID: j.ID, Err: fmt.Errorf("%s: %w", terminus.Args(step).Command(), err)}
			return
		}
	}
	j.events <- DoneMsg{ID: j.ID}
}

func listen(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// Get returns a job by ID.
func (q *Queue) Get(id int) *Job {
	for _, j := range q.jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}

// Apply records a job message and returns the command that keeps
// listening for the job's next event.
func (q *Queue) Apply(msg tea.Msg) tea.Cmd {
	var j *Job
	switch msg := msg.(type) {
	case StepMsg:
		if j = q.Get(msg.ID); j != nil {
			j.Step = msg.Step
		}
	case LineMsg:
		if j = q.Get(msg.ID); j != nil {
			j.Output = append(j.Output, msg.Line)
		}
	case DoneMsg:
		if j = q.Get(msg.ID); j != nil {
			j.Ended = q.now()
			j.Err = msg.Err
			j.State = Succeeded
			if msg.Err != nil {
				j.State = Failed
			}
		}
	}
	if j == nil {
		return nil
	}
	return listen(j.events)
}

// Running returns jobs that have not finished.
func (q *Queue) Running() []*Job {
	var out []*Job
	for _, j := range q.jobs {
		if j.State == Running {
			out = append(out, j)
		}
	}
	return out
}

// Recent returns running jobs and the last n finished ones, newest first.
func (q *Queue) Recent(n int) []*Job {
	var running, done []*Job
	for i := len(q.jobs) - 1; i >= 0; i-- {
		j := q.jobs[i]
		if j.State == Running {
			running = append(running, j)
		} else if len(done) < n {
			done = append(done, j)
		}
	}
	return append(running, done...)
}

// CancelAll stops every running job's local process. Pantheon workflows
// already started continue server-side.
func (q *Queue) CancelAll() {
	for _, j := range q.Running() {
		j.cancel()
	}
}
