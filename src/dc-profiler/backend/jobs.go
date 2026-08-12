package main

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// jobs is the process-wide store of in-flight and recently-finished generation
// runs. Set in main().
var jobs *jobStore

// job holds the state of one async suggestion run, polled by the client. The
// pipeline runs in a detached goroutine, so the state is guarded by a mutex.
type job struct {
	mu      sync.Mutex
	status  string // "running" | "done" | "error"
	steps   []Progress
	result  map[string]any
	errMsg  string
	updated time.Time
}

func (j *job) addStep(p Progress) {
	j.mu.Lock()
	j.steps = append(j.steps, p)
	j.updated = time.Now()
	j.mu.Unlock()
}

func (j *job) finish(result map[string]any, err error) {
	j.mu.Lock()
	if err != nil {
		j.status = "error"
		j.errMsg = err.Error()
	} else {
		j.status = "done"
		j.result = result
	}
	j.updated = time.Now()
	j.mu.Unlock()
}

// snapshot returns a JSON-serializable view of the job's current state.
func (j *job) snapshot() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	steps := make([]Progress, len(j.steps))
	copy(steps, j.steps)
	out := map[string]any{"status": j.status, "steps": steps}
	switch j.status {
	case "done":
		out["result"] = j.result
	case "error":
		out["error"] = j.errMsg
	}
	return out
}

type jobStore struct {
	mu   sync.Mutex
	jobs map[string]*job
}

func newJobStore() *jobStore {
	s := &jobStore{jobs: map[string]*job{}}
	go s.reap()
	return s
}

func (s *jobStore) create() (string, *job) {
	id := newJobID()
	j := &job{status: "running", updated: time.Now()}
	s.mu.Lock()
	s.jobs[id] = j
	s.mu.Unlock()
	return id, j
}

func (s *jobStore) get(id string) (*job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	return j, ok
}

// reap drops jobs that haven't been touched for a while so the map doesn't grow
// without bound; a client only needs a job until it has read the terminal state.
func (s *jobStore) reap() {
	t := time.NewTicker(5 * time.Minute)
	for range t.C {
		cutoff := time.Now().Add(-30 * time.Minute)
		s.mu.Lock()
		for id, j := range s.jobs {
			j.mu.Lock()
			stale := j.updated.Before(cutoff)
			j.mu.Unlock()
			if stale {
				delete(s.jobs, id)
			}
		}
		s.mu.Unlock()
	}
}

func newJobID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
