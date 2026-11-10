package service

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type UpstreamAttemptQueueStats struct {
	Enqueued  int64 `json:"enqueued"`
	Dropped   int64 `json:"dropped"`
	Persisted int64 `json:"persisted"`
	Failed    int64 `json:"failed"`
	Queued    int   `json:"queued"`
}

type upstreamAttemptJob struct {
	repository UpstreamAttemptRepository
	attempt    UpstreamAttempt
}

type upstreamAttemptQueue struct {
	mu        sync.RWMutex
	jobs      chan upstreamAttemptJob
	start     sync.Once
	workers   sync.WaitGroup
	stopped   bool
	enqueued  atomic.Int64
	dropped   atomic.Int64
	persisted atomic.Int64
	failed    atomic.Int64
}

var defaultUpstreamAttemptQueue = &upstreamAttemptQueue{jobs: make(chan upstreamAttemptJob, 1024)}

func (q *upstreamAttemptQueue) enqueue(job upstreamAttemptJob) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.stopped {
		q.dropped.Add(1)
		return
	}
	q.start.Do(func() {
		for range 2 {
			q.workers.Add(1)
			go q.run()
		}
	})
	select {
	case q.jobs <- job:
		q.enqueued.Add(1)
	default:
		q.dropped.Add(1)
	}
}

func (q *upstreamAttemptQueue) run() {
	defer q.workers.Done()
	for job := range q.jobs {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := job.repository.Insert(ctx, &job.attempt)
		cancel()
		if err != nil {
			q.failed.Add(1)
			slog.Warn("upstream_attempt_persist_failed", "attempt_id", job.attempt.AttemptID)
		} else {
			q.persisted.Add(1)
		}
	}
}

func (q *upstreamAttemptQueue) stop() {
	q.mu.Lock()
	if !q.stopped {
		q.stopped = true
		close(q.jobs)
	}
	q.mu.Unlock()
	done := make(chan struct{})
	go func() { q.workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		slog.Warn("upstream_attempt_queue_shutdown_timeout")
	}
}

func UpstreamAttemptQueueStatus() UpstreamAttemptQueueStats {
	q := defaultUpstreamAttemptQueue
	return UpstreamAttemptQueueStats{Enqueued: q.enqueued.Load(), Dropped: q.dropped.Load(), Persisted: q.persisted.Load(), Failed: q.failed.Load(), Queued: len(q.jobs)}
}

func StopUpstreamAttemptWorkers() { defaultUpstreamAttemptQueue.stop() }
