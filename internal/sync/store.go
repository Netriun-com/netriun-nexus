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
	StateID     int64  `json:"state_id"`
}

func (p Payload) Key() string { return fmt.Sprintf("sync:%d:%s:%s", p.AccountID, p.Service, p.Region) }

type Account struct {
	ID, WorkspaceID int64
	Provider        string
	Regions         []string
}

// AccountWide is the existing collectors' whole-account scope. TODO(T14):
// replace Alibaba OSS/security-group/user '*' rows when collectors are regional.
const AccountWide = "*"

// EnqueueDue locks due rows and inserts deduplicated jobs in one transaction.
// Active keys are excluded so a slow first batch cannot starve later due rows.
func (s *Store) EnqueueDue(ctx context.Context) (int, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, `SELECT s.id,a.workspace_id,s.account_id,s.service,s.region
        FROM sync_state s JOIN cloud_accounts a ON a.id=s.account_id
        WHERE s.enabled AND s.next_due_at<=clock_timestamp()
        AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.status IN ('queued','running')
            AND j.dedupe_key='sync:'||s.account_id||':'||s.service||':'||s.region)
        ORDER BY s.next_due_at,s.id LIMIT 100 FOR UPDATE OF s SKIP LOCKED`)
	if err != nil {
		return 0, err
	}
	var due []Payload
	for rows.Next() {
		var p Payload
		if err := rows.Scan(&p.StateID, &p.WorkspaceID, &p.AccountID, &p.Service, &p.Region); err != nil {
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
		if _, err := s.Queue.EnqueueTx(ctx, tx, jobs.Params{Kind: JobKind, Payload: raw, DedupeKey: p.Key(), ConcurrencyKey: fmt.Sprintf("account:%d", p.AccountID)}); err != nil {
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
		err := s.DB.QueryRow(ctx, `UPDATE sync_state s SET last_started_at=clock_timestamp(),last_job_id=$1,last_attempt=$2
            FROM cloud_accounts a,jobs j WHERE s.id=$3 AND s.account_id=$4 AND s.service=$5 AND s.region=$6 AND s.enabled
            AND a.id=s.account_id AND a.workspace_id=$7 AND j.id=$1 AND j.attempts=$2 AND j.locked_by=$8
            AND j.status='running' AND j.locked_until>clock_timestamp()
            RETURNING a.provider,a.regions`, j.ID, j.Attempts, p.StateID, p.AccountID, p.Service, p.Region, p.WorkspaceID, j.LockedBy).Scan(&ac.Provider, &ac.Regions)
		if errors.Is(err, pgx.ErrNoRows) {
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
			code, message := "", ""
			if result != nil {
				code, message = "NX-SYNC-FAILED", "Collection failed. Check credentials, permissions and selected regions."
				if errors.Is(result, context.Canceled) {
					code, message = "NX-SYNC-CANCELLED", "Collection interrupted; retry is scheduled."
				}
				if errors.Is(result, context.DeadlineExceeded) {
					code, message = "NX-SYNC-TIMEOUT", "Collection timed out; retry is scheduled."
				}
			}
			// Never reuse a cancelled provider context for recording failure.
			save, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			interval := s.Config.Interval(p.Service, result == nil && empty)
			_, saveErr := s.DB.Exec(save, `UPDATE sync_state s SET
                last_success_at=CASE WHEN $4='' THEN clock_timestamp() ELSE last_success_at END,
                last_error=$4,last_error_code=$5,empty=CASE WHEN $4='' THEN $6 ELSE empty END,
                "interval"=$7::bigint*interval '1 microsecond',
                next_due_at=clock_timestamp()+$7::bigint*interval '1 microsecond'
                FROM jobs j WHERE s.id=$1 AND s.last_job_id=$2 AND s.last_attempt=$3
                AND j.id=$2 AND j.attempts=$3 AND j.locked_by=$8 AND j.status='running' AND j.locked_until>clock_timestamp()`,
				p.StateID, j.ID, j.Attempts, message, code, empty, interval.Microseconds(), j.LockedBy)
			if result != nil {
				result = errors.New(message)
			}
			if saveErr != nil {
				result = errors.New("Could not record scheduled sync result")
			}
		}()
		empty, result = collect(ctx, p, ac)
		if result == nil {
			result = ctx.Err()
		}
		return result
	}
}
