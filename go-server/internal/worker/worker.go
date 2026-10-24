// Package worker is a tiny in-process job queue: a fixed pool of goroutines
// draining a channel, with per-job state tracking. It replaces the Python
// app's Redis/RQ queue for background work (scans, downloads) so the single
// binary needs no external broker.
package worker

import (
	"context"
	"sync"
	"time"
)

type State string

const (
	Queued  State = "queued"
	Running State = "running"
	Done    State = "done"
	Failed  State = "failed"
)

type Job struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	State    State     `json:"state"`
	Err      string    `json:"error,omitempty"`
	Enqueued time.Time `json:"enqueued"`
	Started  time.Time `json:"started,omitempty"`
	Finished time.Time `json:"finished,omitempty"`
}

type task struct {
	id int64
	fn func(context.Context) error
}

type Queue struct {
	ctx    context.Context
	ch     chan task
	mu     sync.Mutex
	jobs   map[int64]*Job
	nextID int64
	wg     sync.WaitGroup
}

// New starts `workers` goroutines. `buffer` bounds the pending queue.
func New(ctx context.Context, workers, buffer int) *Queue {
	if workers < 1 {
		workers = 1
	}
	q := &Queue{ctx: ctx, ch: make(chan task, buffer), jobs: map[int64]*Job{}}
	for i := 0; i < workers; i++ {
		q.wg.Add(1)
		go q.loop()
	}
	return q
}

func (q *Queue) loop() {
	defer q.wg.Done()
	for {
		select {
		case <-q.ctx.Done():
			return
		case t, ok := <-q.ch:
			if !ok {
				return
			}
			q.run(t)
		}
	}
}

func (q *Queue) run(t task) {
	q.set(t.id, func(j *Job) { j.State = Running; j.Started = time.Now() })
	err := t.fn(q.ctx)
	q.set(t.id, func(j *Job) {
		j.Finished = time.Now()
		if err != nil {
			j.State, j.Err = Failed, err.Error()
		} else {
			j.State = Done
		}
	})
}

// Enqueue registers a job and returns its id. It never blocks: if the buffer
// is full the job is marked failed immediately.
func (q *Queue) Enqueue(name string, fn func(context.Context) error) int64 {
	q.mu.Lock()
	q.nextID++
	id := q.nextID
	q.jobs[id] = &Job{ID: id, Name: name, State: Queued, Enqueued: time.Now()}
	q.mu.Unlock()

	select {
	case q.ch <- task{id: id, fn: fn}:
	default:
		q.set(id, func(j *Job) { j.State, j.Err, j.Finished = Failed, "queue full", time.Now() })
	}
	return id
}

func (q *Queue) set(id int64, mut func(*Job)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if j, ok := q.jobs[id]; ok {
		mut(j)
	}
}

func (q *Queue) Get(id int64) (Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[id]
	if !ok {
		return Job{}, false
	}
	return *j, true
}

func (q *Queue) List() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Job, 0, len(q.jobs))
	for _, j := range q.jobs {
		out = append(out, *j)
	}
	return out
}
