package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rithlath/lazyterminus/internal/terminus"
)

func mock(t *testing.T) *terminus.MockRunner {
	t.Helper()
	m, err := terminus.NewMockRunner(os.DirFS(filepath.Join("..", "..", "testdata")))
	if err != nil {
		t.Fatal(err)
	}
	m.MinLatency, m.MaxLatency, m.WorkflowDuration = 0, 0, 20*time.Millisecond
	return m
}

// drive feeds a job's messages back into the queue like the UI does.
func drive(t *testing.T, q *Queue, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	var msgs []tea.Msg
	deadline := time.Now().Add(5 * time.Second)
	for cmd != nil && time.Now().Before(deadline) {
		msg := cmd()
		if msg == nil {
			break
		}
		msgs = append(msgs, msg)
		cmd = q.Apply(msg)
	}
	return msgs
}

func TestJobRunsStepsInOrder(t *testing.T) {
	m := mock(t)
	q := New(m)
	steps := [][]string{{"env:wake", "acme-web.proj-19"}, {"env:clear-cache", "acme-web.proj-19"}}
	j, cmd := q.Start(context.Background(), "Clear cache", "acme-web", "proj-19", steps)
	if len(q.Running()) != 1 {
		t.Fatal("job should be running")
	}
	msgs := drive(t, q, cmd)
	if j.State != Succeeded || j.Err != nil || j.Ended.IsZero() {
		t.Fatalf("job = %+v", j)
	}
	var stepOrder []int
	for _, msg := range msgs {
		if s, ok := msg.(StepMsg); ok {
			stepOrder = append(stepOrder, s.Step)
		}
	}
	if len(stepOrder) != 2 || stepOrder[0] != 0 || stepOrder[1] != 1 {
		t.Errorf("steps = %v", stepOrder)
	}
	calls := m.MutatingCalls()
	if len(calls) != 2 || calls[0].Args[0] != "env:wake" || calls[1].Args[0] != "env:clear-cache" {
		t.Errorf("calls = %+v", calls)
	}
	if len(j.Output) == 0 {
		t.Error("no output captured")
	}
	if len(q.Running()) != 0 || len(q.Recent(5)) != 1 {
		t.Error("queue bookkeeping wrong")
	}
}

func TestJobStopsOnFailure(t *testing.T) {
	m := mock(t)
	m.FailNext("env:wake", "wake failed")
	q := New(m)
	j, cmd := q.Start(context.Background(), "x", "acme-web", "dev", [][]string{{"env:wake", "acme-web.dev"}, {"env:clear-cache", "acme-web.dev"}})
	drive(t, q, cmd)
	var ce *terminus.CommandError
	if j.State != Failed || !errors.As(j.Err, &ce) {
		t.Fatalf("job = %+v", j)
	}
	for _, c := range m.MutatingCalls() {
		if c.Args[0] == "env:clear-cache" {
			t.Error("later step ran after failure")
		}
	}
}
