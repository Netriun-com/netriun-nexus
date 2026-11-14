// SPDX-License-Identifier: AGPL-3.0-only

package sync

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/netriun/nexus/internal/cloud"
	"github.com/netriun/nexus/internal/jobs"
)

type attemptKey struct{}
type attempt struct {
	Payload Payload
	Job     jobs.Job
}

// fenceInventory locks the account and current lease before changing inventory.
// A provider response from an expired attempt cannot replace newer data.
func (s *Store) fenceInventory(ctx context.Context, tx pgx.Tx, account int64, whole bool, region string) error {
	a, ok := ctx.Value(attemptKey{}).(attempt)
	if !ok || a.Payload.Service != Compute || a.Payload.AccountID != account || whole != (a.Payload.Region == AccountWide) || (!whole && region != a.Payload.Region) {
		return jobs.ErrLeaseLost
	}
	var id int64
	if err := tx.QueryRow(ctx, "SELECT id FROM cloud_accounts WHERE id=$1 AND workspace_id=$2 FOR UPDATE", account, a.Payload.WorkspaceID).Scan(&id); err != nil {
		return err
	}
	if err := jobs.CheckLeaseTx(ctx, tx, a.Job); err != nil {
		return err
	}
	return tx.QueryRow(ctx, `SELECT id FROM sync_state WHERE id=$1 AND account_id=$2 AND service='compute' AND region=$3
   AND enabled AND last_job_id=$4 AND last_attempt=$5 FOR UPDATE`, a.Payload.StateID, account, a.Payload.Region, a.Job.ID, a.Job.Attempts).Scan(&id)
}

// ReconcileInventory preserves the existing upsert/delete semantics, with writes
// fenced by the job attempt carried in the context supplied by Handler.
func (s *Store) ReconcileInventory(ctx context.Context, accountID int64, items []cloud.Instance, wholeAccount bool, region string) error {
	// Reconcile only after every provider page succeeded; failed regions retain their inventory.
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if err := s.fenceInventory(ctx, tx, accountID, wholeAccount, region); err != nil {
		return err
	}
	ids := make([]string, 0, len(items))
	itemRegions := make([]string, 0, len(items))
	names := make([]string, 0, len(items))
	states := make([]string, 0, len(items))
	types := make([]string, 0, len(items))
	publicIPs := make([]string, 0, len(items))
	privateIPs := make([]string, 0, len(items))
	details := make([]string, 0, len(items))
	for _, i := range items {
		if i.Region == "" {
			return errors.New("provider returned an instance without a region")
		}
		ids = append(ids, i.ID)
		b, _ := json.Marshal(i.Details)
		itemRegions = append(itemRegions, i.Region)
		names = append(names, i.Name)
		states = append(states, i.State)
		types = append(types, i.Type)
		publicIPs = append(publicIPs, i.PublicIP)
		privateIPs = append(privateIPs, i.PrivateIP)
		details = append(details, string(b))
	}
	if len(items) > 0 {
		_, err = tx.Exec(ctx, `
INSERT INTO instances(account_id,instance_id,region,name,state,instance_type,public_ip,private_ip,details)
SELECT $1,item.id,item.region,item.name,item.state,item.instance_type,item.public_ip,item.private_ip,item.details::jsonb
FROM unnest($2::text[],$3::text[],$4::text[],$5::text[],$6::text[],$7::text[],$8::text[],$9::text[])
 AS item(id,region,name,state,instance_type,public_ip,private_ip,details)
ON CONFLICT(account_id,region,instance_id) DO UPDATE SET
 name=excluded.name,state=excluded.state,instance_type=excluded.instance_type,
 public_ip=excluded.public_ip,private_ip=excluded.private_ip,details=excluded.details,seen_at=now()`,
			accountID, ids, itemRegions, names, states, types, publicIPs, privateIPs, details)
		if err != nil {
			return err
		}
	}
	if wholeAccount {
		_, err = tx.Exec(ctx, "DELETE FROM instances WHERE account_id=$1 AND NOT(instance_id=ANY($2::text[]))", accountID, ids)
	} else {
		_, err = tx.Exec(ctx, "DELETE FROM instances WHERE account_id=$1 AND region=$2 AND NOT(instance_id=ANY($3::text[]))", accountID, region, ids)
	}
	if err != nil {
		return err
	}
	if err := jobs.CheckLeaseTx(ctx, tx, ctx.Value(attemptKey{}).(attempt).Job); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
