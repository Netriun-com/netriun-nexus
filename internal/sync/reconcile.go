// SPDX-License-Identifier: AGPL-3.0-only

package sync

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
)

// Discovery supplies regional scopes only for accounts whose region selection
// is empty (= all available regions). Provider calls occur in worker maintenance,
// never in ReconcileAccountTx or an HTTP account save. Missing service entries
// preserve existing rows after a failed discovery; an explicit empty slice removes them.
type Discovery func(context.Context, Account) map[string][]string

func (s *Store) Accounts(ctx context.Context) ([]Account, error) {
	rows, err := s.DB.Query(ctx, "SELECT id,workspace_id,provider,regions FROM cloud_accounts ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var accounts []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.WorkspaceID, &a.Provider, &a.Regions); err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

func (s *Store) Reconcile(ctx context.Context, discover Discovery) error {
	accounts, err := s.Accounts(ctx)
	if err != nil {
		return err
	}
	for _, a := range accounts {
		var found map[string][]string
		if len(a.Regions) == 0 && (a.Provider == "aws" || a.Provider == "alibaba") {
			found = discover(ctx, a)
		}
		tx, err := s.DB.Begin(ctx)
		if err != nil {
			return err
		}
		err = s.reconcileAccount(ctx, tx, a.ID, found, &a)
		if err == nil {
			err = tx.Commit(ctx)
		}
		_ = tx.Rollback(context.Background())
		if err != nil {
			return err
		}
	}
	return nil
}

// ReconcileAccountTx runs in the same transaction as account creation/update.
// Account-wide rows keep their identity across region selection changes. New
// all-region accounts acquire regional rows on the worker's first discovery.
func (s *Store) ReconcileAccountTx(ctx context.Context, tx pgx.Tx, id int64) error {
	return s.reconcileAccount(ctx, tx, id, nil, nil)
}

// ResetAccountTx invalidates old provider scopes when an account changes provider.
func (s *Store) ResetAccountTx(ctx context.Context, tx pgx.Tx, id int64) error {
	_, err := tx.Exec(ctx, "DELETE FROM sync_state WHERE account_id=$1", id)
	return err
}

func (s *Store) reconcileAccount(ctx context.Context, tx pgx.Tx, id int64, found map[string][]string, expected *Account) error {
	var a Account
	err := tx.QueryRow(ctx, "SELECT id,workspace_id,provider,regions FROM cloud_accounts WHERE id=$1 FOR UPDATE", id).Scan(&a.ID, &a.WorkspaceID, &a.Provider, &a.Regions)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if expected != nil && (a.Provider != expected.Provider || !slices.Equal(a.Regions, expected.Regions)) {
		return nil
	}
	desired := map[string][]string{}
	if a.Provider == "azure" || a.Provider == "gcp" {
		desired[Compute] = []string{AccountWide}
	} else {
		desired[Compute] = a.Regions
		if len(a.Regions) == 0 {
			desired[Compute] = found[Compute]
		}
	}
	if a.Provider == "alibaba" {
		desired[Desktops] = a.Regions
		if len(a.Regions) == 0 {
			desired[Desktops] = found[Desktops]
		}
		for _, service := range []string{Users, Buckets, SecurityGroups} {
			desired[service] = []string{AccountWide}
		}
	}
	services := make([]string, 0, len(desired))
	for service, regions := range desired {
		services = append(services, service)
		if regions != nil {
			if _, err := tx.Exec(ctx, `DELETE FROM sync_state WHERE account_id=$1 AND service=$2 AND NOT(region=ANY($3::text[]))`, id, service, regions); err != nil {
				return err
			}
			for _, region := range regions {
				if _, err := tx.Exec(ctx, `INSERT INTO sync_state(account_id,service,region,"interval") VALUES($1,$2,$3,$4::bigint*interval '1 microsecond')
                    ON CONFLICT(account_id,service,region) DO NOTHING`, id, service, region, s.Config.Interval(service, false).Microseconds()); err != nil {
					return err
				}
			}
		}
		// Apply configured intervals without resetting state or disabled rows.
		_, err := tx.Exec(ctx, `UPDATE sync_state SET
            next_due_at=LEAST(next_due_at,COALESCE(last_success_at,clock_timestamp())+
                CASE WHEN empty AND region<>'*' AND last_error='' THEN $4::bigint ELSE $3::bigint END*interval '1 microsecond'),
            "interval"=CASE WHEN empty AND region<>'*' AND last_error='' THEN $4::bigint ELSE $3::bigint END*interval '1 microsecond'
            WHERE account_id=$1 AND service=$2 AND "interval"<>
                CASE WHEN empty AND region<>'*' AND last_error='' THEN $4::bigint ELSE $3::bigint END*interval '1 microsecond'`, id, service, s.Config.Interval(service, false).Microseconds(), s.Config.Empty.Microseconds())
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, "DELETE FROM sync_state WHERE account_id=$1 AND NOT(service=ANY($2::text[]))", id, services)
	return err
}
