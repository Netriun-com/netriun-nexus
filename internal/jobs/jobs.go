// SPDX-License-Identifier: AGPL-3.0-only

// Package jobs provides a PostgreSQL-backed, at-least-once job queue. New wraps
// an existing pool; Enqueue deduplicates queued/running jobs by nonempty key.
// Claim returns one due job (higher priority first), or nil when none is
// available, and reclaims expired leases. Each claim consumes an attempt.
// Workers must Renew before expiry, then Complete or Fail using the claimed
// Job as their lease token. Its ID, LockedBy and Attempts fence stale workers;
// ErrLeaseLost means they must stop. Handlers must tolerate repeated execution.
// Fail retries after 1s, 2s, 4s, ... capped at 15m, until MaxAttempts. Expired
// leases retry immediately, or become failed if their attempts are exhausted.
//
// Nonempty concurrency keys default to one running job across all workers;
// New accepts per-key overrides. Every worker must use the same limits.
// Keys of the form "account:123" can also match an "account:*" limit; exact
// overrides take precedence over these namespace limits. Empty
// keys are unconstrained. Optional ProviderKey consumes a second limit using the
// same locking protocol, so both account and provider caps apply. Claim skips
// locked rows and busy/full keys; callers
// should poll again after nil. Database time governs scheduling and leases.
// This package does not start workers or call providers.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrLeaseLost = errors.New("job lease is no longer owned")

// Queue is safe for concurrent use. The caller owns the pool's lifetime.
type Queue struct {
	db     *pgxpool.Pool
	limits map[string]int
}

func New(db *pgxpool.Pool, limits map[string]int) (*Queue, error) {
	if db == nil {
		return nil, errors.New("jobs: nil pool")
	}
	q := &Queue{db: db, limits: make(map[string]int, len(limits))}
	for key, limit := range limits {
		if key == "" || limit < 1 {
			return nil, errors.New("jobs: limits require nonempty keys and positive values")
		}
		q.limits[key] = limit
	}
	return q, nil
}

type Job struct {
	ID             int64
	Kind           string
	Payload        json.RawMessage
	Status         string
	Priority       int
	Attempts       int
	MaxAttempts    int
	RunAfter       time.Time
	LockedBy       string
	LockedUntil    *time.Time
	ConcurrencyKey string
	ProviderKey    string
	DedupeKey      string
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Params describes a new job. Defaults: {} payload, three attempts, due now.
// Empty DedupeKey disables deduplication. Existing active jobs are unchanged.
type Params struct {
	Kind           string
	Payload        json.RawMessage
	Priority       int
	MaxAttempts    int
	RunAfter       time.Time
	ConcurrencyKey string
	ProviderKey    string
	DedupeKey      string
}

const columns = `id, kind, payload, status, priority, attempts, max_attempts,
    run_after, COALESCE(locked_by, ''), locked_until, concurrency_key,
    COALESCE(dedupe_key, ''), last_error, created_at, updated_at, provider_key`

func scan(row pgx.Row) (*Job, error) {
	j := new(Job)
	err := row.Scan(&j.ID, &j.Kind, &j.Payload, &j.Status, &j.Priority,
		&j.Attempts, &j.MaxAttempts, &j.RunAfter, &j.LockedBy, &j.LockedUntil,
		&j.ConcurrencyKey, &j.DedupeKey, &j.LastError, &j.CreatedAt, &j.UpdatedAt, &j.ProviderKey)
	if err != nil {
		return nil, err
	}
	return j, nil
}

func (q *Queue) Enqueue(ctx context.Context, p Params) (*Job, error) {
	return enqueueJob(ctx, q.db, p)
}

// EnqueueTx atomically adds a job alongside scheduling state in the caller's transaction.
func (q *Queue) EnqueueTx(ctx context.Context, tx pgx.Tx, p Params) (*Job, error) {
	return enqueueJob(ctx, tx, p)
}

// PromoteTx raises the priority of a queued job without bypassing retry backoff
// or modifying a running job. Manual refreshes use it when joining a schedule.
func (q *Queue) PromoteTx(ctx context.Context, tx pgx.Tx, id int64, priority int) error {
	_, err := tx.Exec(ctx, `UPDATE jobs SET priority=GREATEST(priority,$2) WHERE id=$1 AND status='queued'`, id, priority)
	return err
}

func enqueueJob(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, p Params) (*Job, error) {
	if p.Kind == "" || p.MaxAttempts < 0 {
		return nil, errors.New("jobs: kind and positive max attempts required")
	}
	if p.MaxAttempts == 0 {
		p.MaxAttempts = 3
	}
	if len(p.Payload) == 0 {
		p.Payload = json.RawMessage(`{}`)
	}
	if !json.Valid(p.Payload) {
		return nil, errors.New("jobs: invalid JSON payload")
	}
	var due any
	if !p.RunAfter.IsZero() {
		due = p.RunAfter
	}
	return scan(db.QueryRow(ctx, `INSERT INTO jobs
        (kind, payload, priority, max_attempts, run_after, concurrency_key, dedupe_key, provider_key)
        VALUES ($1, $2, $3, $4, COALESCE($5::timestamptz, clock_timestamp()), $6, NULLIF($7, ''), $8)
        ON CONFLICT (dedupe_key) WHERE status IN ('queued', 'running')
        DO UPDATE SET dedupe_key = jobs.dedupe_key RETURNING `+columns,
		p.Kind, p.Payload, p.Priority, p.MaxAttempts, due, p.ConcurrencyKey, p.DedupeKey, p.ProviderKey))
}

// Cleanup removes one bounded batch of terminal jobs, using database time and
// completion/update time rather than enqueue time. Active jobs are never deleted.
func (q *Queue) Cleanup(ctx context.Context, retention time.Duration, batch int) (int64, error) {
	if retention <= 0 || batch < 1 {
		return 0, errors.New("jobs: positive retention and batch required")
	}
	tag, err := q.db.Exec(ctx, `WITH expired AS (
        SELECT id FROM jobs WHERE status IN ('succeeded', 'failed')
            AND updated_at < clock_timestamp() - $1::bigint * interval '1 microsecond'
        ORDER BY updated_at, id LIMIT $2 FOR UPDATE SKIP LOCKED
    ) DELETE FROM jobs WHERE id IN (SELECT id FROM expired)`, retention.Microseconds(), batch)
	return tag.RowsAffected(), err
}

// Claim reserves at most one job, optionally restricted to the supplied kinds.
// No kinds means all kinds. Lease must be at least one microsecond.
func (q *Queue) Claim(ctx context.Context, worker string, lease time.Duration, kinds ...string) (*Job, error) {
	if worker == "" || lease < time.Microsecond {
		return nil, errors.New("jobs: worker and positive lease required")
	}
	if _, err := q.ReclaimExpired(ctx); err != nil {
		return nil, err
	}
	tx, err := q.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	excluded := []string{}
	if kinds == nil {
		kinds = []string{}
	}
	for {
		var id int64
		var key, providerKey string
		err = tx.QueryRow(ctx, `SELECT id, concurrency_key, provider_key FROM jobs
            WHERE status = 'queued' AND run_after <= clock_timestamp() AND attempts < max_attempts
                AND (cardinality($2::text[]) = 0 OR kind = ANY($2))
                AND NOT (concurrency_key = ANY($1::text[]) OR provider_key = ANY($1::text[]))
            ORDER BY priority DESC, run_after, id LIMIT 1 FOR UPDATE SKIP LOCKED`, excluded, kinds).Scan(&id, &key, &providerKey)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		keys := []string{key, providerKey}
		slices.Sort(keys)
		blocked := false
		for _, key := range slices.Compact(keys) {
			if key == "" {
				continue
			}
			var locked bool
			// A row lock alone cannot serialize two claims of different jobs
			// with the same key. Hash collisions only reduce parallelism.
			err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('nexus.jobs:' || $1, 0))`, key).Scan(&locked)
			if err != nil {
				return nil, err
			}
			if !locked {
				excluded = append(excluded, key)
				blocked = true
				break
			}
			limit := 1
			if namespace, _, ok := strings.Cut(key, ":"); ok {
				if n, found := q.limits[namespace+":*"]; found {
					limit = n
				}
			}
			if n, ok := q.limits[key]; ok {
				limit = n
			}
			var running int
			// Separate READ COMMITTED statement after acquiring the key lock
			// sees claims committed by the previous holder. Count even expired
			// leases until reclaimed, conservatively preserving the row limit.
			err = tx.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE status = 'running' AND (concurrency_key = $1 OR provider_key = $1)`, key).Scan(&running)
			if err != nil {
				return nil, err
			}
			if running >= limit {
				excluded = append(excluded, key)
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		j, err := scan(tx.QueryRow(ctx, `UPDATE jobs SET status = 'running', attempts = attempts + 1,
            started_at=COALESCE(started_at,clock_timestamp()),
            locked_by = $2, locked_until = clock_timestamp() + $3::bigint * interval '1 microsecond',
            updated_at = clock_timestamp() WHERE id = $1 RETURNING `+columns, id, worker, lease.Microseconds()))
		if err != nil {
			return nil, err
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return j, nil
	}
}

const owned = `id = $1 AND locked_by = $2 AND attempts = $3
    AND status = 'running' AND locked_until > clock_timestamp()`

// CheckLeaseTx locks a lease, then checks ownership using database time. A
// handler can fence its persisted effects in the same short transaction.
func CheckLeaseTx(ctx context.Context, tx pgx.Tx, j Job) error {
	var id int64
	if err := tx.QueryRow(ctx, `SELECT id FROM jobs WHERE id=$1 FOR UPDATE`, j.ID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLeaseLost
		}
		return err
	}
	err := tx.QueryRow(ctx, `SELECT id FROM jobs WHERE `+owned, j.ID, j.LockedBy, j.Attempts).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	return err
}

// Renew extends a live lease from database time; it cannot resurrect an expired lease.
func (q *Queue) Renew(ctx context.Context, j Job, lease time.Duration) error {
	if lease < time.Microsecond {
		return errors.New("jobs: positive lease required")
	}
	return q.updateLease(ctx, `UPDATE jobs SET
        locked_until = GREATEST(locked_until, clock_timestamp() + $4::bigint * interval '1 microsecond'),
        updated_at = clock_timestamp() WHERE `+owned, j, lease.Microseconds())
}

func (q *Queue) Complete(ctx context.Context, j Job) error {
	return q.updateLease(ctx, `UPDATE jobs SET status = 'succeeded', locked_by = NULL,
        locked_until = NULL, updated_at = clock_timestamp() WHERE `+owned, j)
}

// Fail records a safe diagnostic, releases the lease, and schedules a retry
// unless all attempts are used. Callers must not include secrets in message.
func (q *Queue) Fail(ctx context.Context, j Job, message string) error {
	return q.updateLease(ctx, `UPDATE jobs SET
        status = CASE WHEN attempts < max_attempts THEN 'queued' ELSE 'failed' END,
        run_after = CASE WHEN attempts < max_attempts THEN clock_timestamp()
            + LEAST(900, power(2, LEAST(attempts - 1, 10))) * interval '1 second' ELSE run_after END,
        locked_by = NULL, locked_until = NULL, last_error = $4, updated_at = clock_timestamp()
        WHERE `+owned, j, message)
}

func (q *Queue) updateLease(ctx context.Context, sql string, j Job, extra ...any) error {
	tx, err := q.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var id int64
	// Check the expiry only after obtaining the row lock. Otherwise a lease
	// could expire while an UPDATE waits for another transaction's row lock.
	err = tx.QueryRow(ctx, `SELECT id FROM jobs WHERE id = $1 FOR UPDATE`, j.ID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	args := append([]any{j.ID, j.LockedBy, j.Attempts}, extra...)
	tag, err := tx.Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("jobs: update lease: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return tx.Commit(ctx)
}

// ReclaimExpired releases expired leases in a nonblocking batch. Claim invokes
// it automatically; it is also safe for a separate maintenance loop to call.
func (q *Queue) ReclaimExpired(ctx context.Context) (int64, error) {
	tag, err := q.db.Exec(ctx, `WITH expired AS (
        SELECT id FROM jobs WHERE status = 'running' AND locked_until <= clock_timestamp()
        ORDER BY locked_until, id LIMIT 100 FOR UPDATE SKIP LOCKED
    ) UPDATE jobs SET status = CASE WHEN attempts < max_attempts THEN 'queued' ELSE 'failed' END,
        locked_by = NULL, locked_until = NULL, run_after = clock_timestamp(),
        last_error = 'lease expired', updated_at = clock_timestamp()
        WHERE id IN (SELECT id FROM expired)`)
	return tag.RowsAffected(), err
}
