// SPDX-License-Identifier: AGPL-3.0-only

// Package worker dispatches durable jobs to registered handlers. Run stops
// claiming when its context is cancelled, allows ShutdownTimeout to drain,
// then cancels handlers and leaves unfinished leases to expire. Handlers must
// honor cancellation, tolerate at-least-once execution, and return only safe
// error messages (no credentials or provider response bodies).
package worker

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/netriun/nexus/internal/jobs"
)

type Handler func(context.Context, jobs.Job) error
type Registry map[string]Handler

type Config struct {
	Concurrency     int
	Lease           time.Duration
	PollInterval    time.Duration
	ShutdownTimeout time.Duration
}

func DefaultConfig() Config {
	return Config{Concurrency: 8, Lease: 30 * time.Second, PollInterval: 500 * time.Millisecond, ShutdownTimeout: 15 * time.Second}
}

type Worker struct {
	queue    *jobs.Queue
	handlers Registry
	kinds    []string
	config   Config
	id       string
	alive    atomic.Bool
	started  atomic.Bool
}

func New(queue *jobs.Queue, handlers Registry, config Config) (*Worker, error) {
	if queue == nil || config.Concurrency < 1 || config.Lease < 3*time.Millisecond || config.PollInterval <= 0 || config.ShutdownTimeout <= 0 {
		return nil, errors.New("worker: positive concurrency, lease, poll interval and shutdown timeout required")
	}
	w := &Worker{queue: queue, handlers: Registry{}, config: config, id: rand.Text()}
	for kind, handler := range handlers {
		if kind == "" || handler == nil {
			return nil, errors.New("worker: empty kind or nil handler")
		}
		w.handlers[kind] = handler
		w.kinds = append(w.kinds, kind)
	}
	return w, nil
}

// Alive reports runtime liveness, not database or provider readiness.
func (w *Worker) Alive() bool { return w.alive.Load() }

// Run may be called once. Its context stops claims, not active handlers; a
// separate context lets them finish during the drain interval.
func (w *Worker) Run(ctx context.Context) error {
	if !w.started.CompareAndSwap(false, true) {
		return errors.New("worker: already started")
	}
	work, cancelWork := context.WithCancel(context.Background())
	defer cancelWork()
	w.alive.Store(true)
	defer w.alive.Store(false)
	var wg sync.WaitGroup
	for i := range w.config.Concurrency {
		wg.Go(func() { w.loop(ctx, work, fmt.Sprintf("%s/%d", w.id, i)) })
	}
	<-ctx.Done()
	w.alive.Store(false)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	timer := time.NewTimer(w.config.ShutdownTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		cancelWork()
		<-done
	}
	return nil
}

func (w *Worker) loop(claims, work context.Context, id string) {
	for claims.Err() == nil {
		// An empty registry must not consume unknown jobs during rollout.
		if len(w.kinds) == 0 {
			pause(claims, w.config.PollInterval)
			continue
		}
		attempt, cancel := context.WithTimeout(claims, w.config.Lease/3)
		j, err := w.queue.Claim(attempt, id, w.config.Lease, w.kinds...)
		cancel()
		if claims.Err() != nil {
			return
		} // any committed claim is reclaimed after expiry
		if err != nil {
			slog.Warn("worker claim failed") // avoid logging database connection details
		} else if j != nil {
			w.execute(work, *j)
			continue
		}
		pause(claims, w.config.PollInterval)
	}
}

func pause(ctx context.Context, interval time.Duration) {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func (w *Worker) execute(work context.Context, j jobs.Job) {
	ctx, cancel := context.WithCancel(work)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		// A bad handler must not kill other workers or bypass durable retry.
		defer func() {
			if recover() != nil {
				result <- errors.New("job handler panicked")
			}
		}()
		result <- w.handlers[j.Kind](ctx, j)
	}()
	ticker := time.NewTicker(w.config.Lease / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return // shutdown deadline: do not Fail; leave the lease for recovery
		case <-ticker.C:
			renewal, stop := context.WithTimeout(ctx, w.config.Lease/3)
			err := w.queue.Renew(renewal, j, w.config.Lease)
			stop()
			if err != nil {
				return
			} // cancel work on lease loss or uncertain renewal
		case err := <-result:
			if ctx.Err() != nil {
				return
			}
			finish, stop := context.WithTimeout(ctx, w.config.Lease/3)
			defer stop()
			if err == nil {
				err = w.queue.Complete(finish, j)
			} else {
				err = w.queue.Fail(finish, j, err.Error())
			}
			if err != nil {
				slog.Warn("worker could not finish job", "job_id", j.ID)
			}
			return
		}
	}
}
