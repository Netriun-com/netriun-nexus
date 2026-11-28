// SPDX-License-Identifier: AGPL-3.0-only

package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/netriun/nexus/internal/jobs"
	"github.com/netriun/nexus/internal/worker"
)

type Store struct {
	DB     *pgxpool.Pool
	Config Config
	Queue  *jobs.Queue
}

func New(db *pgxpool.Pool, cfg Config) *Store {
	queue, _ := jobs.New(db, nil)
	return &Store{DB: db, Config: cfg, Queue: queue}
}

// Payload carries workspace ownership because jobs itself has no workspace
// column. Any future HTTP job-status endpoint must authorize WorkspaceID.
// StateID also fences jobs for a scope removed and recreated in the meantime.
type Payload struct {
	WorkspaceID int64  `json:"workspace_id"`
	AccountID   int64  `json:"account_id"`
	Service     string `json:"service"`
	Region      string `json:"region"`
	Provider    string `json:"provider,omitempty"`
	StateID     int64  `json:"state_id"`
}

func (p Payload) Key() string { return fmt.Sprintf("sync:%d:%s:%s", p.AccountID, p.Service, p.Region) }

// ProviderKey shares the provider cap across all collection services.
func (p Payload) ProviderKey() string {
	if p.Provider != "" {
		return "provider:" + p.Provider
	}
	return ""
}

type Account struct {
	ID, WorkspaceID int64
	Provider        string
	Regions         []string
}

// AccountWide is used for global compute APIs and OSS ListBuckets, which has no region filter.
const AccountWide = "*"

// EnqueueDue locks due rows and inserts deduplicated jobs in one transaction.
// Active keys are excluded so a slow first batch cannot starve later due rows.
func (s *Store) EnqueueDue(ctx context.Context) (int, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, `SELECT s.id,a.workspace_id,s.account_id,s.service,s.region,a.provider
        FROM sync_state s JOIN cloud_accounts a ON a.id=s.account_id
        WHERE s.enabled AND NOT(s.region='*' AND s.service IN ('eds.users','ecs.security_groups')) AND s.next_due_at<=clock_timestamp()
        AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.status IN ('queued','running')
            AND j.dedupe_key='sync:'||s.account_id||':'||s.service||':'||s.region)
        ORDER BY s.next_due_at,s.id LIMIT 100 FOR UPDATE OF s SKIP LOCKED`)
	if err != nil {
		return 0, err
	}
	var due []Payload
	for rows.Next() {
		var p Payload
		if err := rows.Scan(&p.StateID, &p.WorkspaceID, &p.AccountID, &p.Service, &p.Region, &p.Provider); err != nil {
			rows.Close()
			return 0, err
		}
		due = append(due, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, p := range due {
		raw, _ := json.Marshal(p)
		if _, err := s.Queue.EnqueueTx(ctx, tx, jobs.Params{Kind: JobKind, Payload: raw, DedupeKey: p.Key(), ConcurrencyKey: fmt.Sprintf("account:%d", p.AccountID), ProviderKey: p.ProviderKey()}); err != nil {
			return 0, err
		}
	}
	return len(due), tx.Commit(ctx)
}

// Collector invokes existing collection code and reports whether its complete,
// successful result was empty. Partial failures must return an error.
type Collector func(context.Context, Payload, Account) (bool, error)

func (s *Store) Handler(collect Collector) worker.Handler {
	return func(ctx context.Context, j jobs.Job) (result error) {
		var p Payload
		if json.Unmarshal(j.Payload, &p) != nil || p.StateID <= 0 || p.AccountID <= 0 || p.WorkspaceID <= 0 {
			return errors.New("Invalid scheduled sync payload")
		}
		ac := Account{ID: p.AccountID, WorkspaceID: p.WorkspaceID}
		err := s.recordStart(ctx, p, j, &ac)
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, jobs.ErrLeaseLost) {
			return nil
		} // obsolete/disabled scope, or lost lease
		if err != nil {
			return errors.New("Could not start scheduled sync")
		}
		empty := false
		defer func() {
			if recover() != nil {
				result = errors.New("Collection interrupted")
			}
			if p.Region == AccountWide {
				empty = false
			}
			code, message := ComputeError(result)
			// Never reuse a cancelled provider context for recording failure.
			save, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			interval := s.Config.Interval(p.Service, result == nil && empty)
			saveErr := s.recordResult(save, p, j, message, code, empty, interval)
			if result != nil {
				result = errors.New(message)
			}
			if saveErr != nil {
				result = errors.New("Could not record scheduled sync result")
			}
		}()
		empty, result = collect(context.WithValue(ctx, attemptKey{}, attempt{Payload: p, Job: j}), p, ac)
		if result == nil {
			result = ctx.Err()
		}
		return result
	}
}
