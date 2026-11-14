// SPDX-License-Identifier: AGPL-3.0-only

package sync

import (
	"context"
	"log/slog"
	"time"
)

// Scheduler periodically reconciles account scopes, removes one batch of old
// terminal jobs, and enqueues due scopes. Run belongs only in worker/all roles.
// A cancelled context stops maintenance immediately; jobs already committed
// remain in the durable queue. Multiple replicas may run the same loop.
type Scheduler struct {
	Store     *Store
	Reconcile func(context.Context) error
	Discovery Discovery
}

func (s Scheduler) Run(ctx context.Context) {
	// Region discovery may wait on a provider. Keep it off the due-job loop so
	// existing scopes continue scheduling while another account is discovered.
	done := make(chan struct{})
	go func() { defer close(done); s.maintain(ctx) }()
	defer func() { <-done }()
	discovered := make(chan struct{})
	go func() { defer close(discovered); s.discover(ctx) }()
	defer func() { <-discovered }()
	ticker := time.NewTicker(s.Store.Config.Tick)
	defer ticker.Stop()
	for ctx.Err() == nil {
		tick, stop := context.WithTimeout(ctx, 30*time.Second)
		if _, err := s.Store.EnqueueDue(tick); err != nil && tick.Err() == nil {
			slog.Warn("sync scheduling failed")
		}
		stop()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s Scheduler) maintain(ctx context.Context) {
	ticker := time.NewTicker(s.Store.Config.Reconcile)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := s.Reconcile(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("sync scope reconciliation failed")
		}
		cleanup, stop := context.WithTimeout(ctx, 10*time.Second)
		if _, err := s.Store.Queue.Cleanup(cleanup, s.Store.Config.Retention, 500); err != nil && ctx.Err() == nil {
			slog.Warn("job retention cleanup failed")
		}
		if err := s.Store.CleanupRefreshes(cleanup); err != nil && ctx.Err() == nil {
			slog.Warn("refresh receipt cleanup failed")
		}
		stop()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s Scheduler) discover(ctx context.Context) {
	if s.Discovery == nil {
		return
	}
	// Poll the persisted due times. The hourly cadence is per account, shared
	// by replicas; new accounts can be discovered at the next minute tick.
	ticker := time.NewTicker(s.Store.Config.Reconcile)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := s.Store.Discover(ctx, s.Discovery); err != nil && ctx.Err() == nil {
			slog.Warn("sync region discovery failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
