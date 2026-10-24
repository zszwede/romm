package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func waitState(t *testing.T, q *Queue, id int64, want State) Job {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := q.Get(id); ok && (j.State == Done || j.State == Failed) {
			if j.State != want {
				t.Fatalf("job %d state = %s (err=%q), want %s", id, j.State, j.Err, want)
			}
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %d did not reach a terminal state", id)
	return Job{}
}

func TestQueue_RunsJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := New(ctx, 2, 8)

	var ran atomic.Bool
	id := q.Enqueue("ok", func(context.Context) error { ran.Store(true); return nil })
	j := waitState(t, q, id, Done)
	if !ran.Load() {
		t.Error("job fn did not run")
	}
	if j.Finished.Before(j.Started) {
		t.Error("finished before started")
	}
}

func TestQueue_FailedJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := New(ctx, 1, 8)
	id := q.Enqueue("boom", func(context.Context) error { return errors.New("kaboom") })
	j := waitState(t, q, id, Failed)
	if j.Err != "kaboom" {
		t.Errorf("err = %q, want kaboom", j.Err)
	}
}

func TestQueue_ConcurrentJobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := New(ctx, 4, 64)
	var n atomic.Int64
	ids := make([]int64, 20)
	for i := range ids {
		ids[i] = q.Enqueue("n", func(context.Context) error { n.Add(1); return nil })
	}
	for _, id := range ids {
		waitState(t, q, id, Done)
	}
	if n.Load() != 20 {
		t.Errorf("ran %d jobs, want 20", n.Load())
	}
	if len(q.List()) != 20 {
		t.Errorf("List len = %d, want 20", len(q.List()))
	}
}
