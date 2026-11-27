// SPDX-License-Identifier: AGPL-3.0-only

// Package resources owns the resource catalog, read by the resources API;
// compute, Alibaba service and network sync reconcile it as the inventory source.
// Reconcile accepts a caller-owned pgx.Tx and a complete, successful collection
// of one Scope. Callers must fence that transaction with
// their job lease before/after reconciliation and roll it back on any failure.
// A failed provider collection must never be passed as an empty successful one.
//
// Outgoing relations belong to the reconciled scope; other incoming relations
// remain unless an endpoint is deleted. References are account-local and missing
// targets are skipped so services can discover resources in either order.
// List returns summaries; Get adds details and authorized relations in both
// directions. Both require a workspace and explicit accessible account IDs.
// Pagination uses an opaque name/ID cursor and the first page's ID ceiling:
// later inserts appear on a new traversal, not between existing pages. This is
// not an MVCC snapshot across calls: renames, deletes, and previously uncommitted
// inserts can still change membership. The caller must keep filters/access fixed.
package resources

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalid = errors.New("invalid resource input")
	ErrCursor  = errors.New("invalid resource cursor or changed scope")
)

type Store struct{ db *pgxpool.Pool }

func New(db *pgxpool.Pool) *Store { return &Store{db: db} }

type Scope struct {
	WorkspaceID, AccountID          int64
	Provider, Service, Region, Kind string
}

type Item struct {
	ProviderID string          `json:"provider_id"`
	Name       string          `json:"name"`
	State      string          `json:"state"`
	Tags       json.RawMessage `json:"tags"`
	Summary    json.RawMessage `json:"summary"`
	Details    json.RawMessage `json:"details"`
}

type Reference struct {
	Service    string `json:"service"`
	Region     string `json:"region"`
	Kind       string `json:"kind"`
	ProviderID string `json:"provider_id"`
}

type Relation struct {
	From     Reference `json:"from"`
	To       Reference `json:"to"`
	Relation string    `json:"relation"`
}

// Resource deliberately has no Details field: List cannot leak full payloads.
type Resource struct {
	ID          int64           `json:"id"`
	WorkspaceID int64           `json:"workspace_id"`
	AccountID   int64           `json:"account_id"`
	Provider    string          `json:"provider"`
	Service     string          `json:"service"`
	Kind        string          `json:"kind"`
	Region      string          `json:"region"`
	ProviderID  string          `json:"provider_id"`
	Name        string          `json:"name"`
	State       string          `json:"state"`
	Tags        json.RawMessage `json:"tags"`
	Summary     json.RawMessage `json:"summary"`
	SeenAt      time.Time       `json:"seen_at"`
	Version     int64           `json:"version"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type Related struct {
	FromID   int64  `json:"from_id"`
	ToID     int64  `json:"to_id"`
	Relation string `json:"relation"`
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
}

type Detail struct {
	Resource
	Details   json.RawMessage `json:"details"`
	Relations []Related       `json:"relations"`
}

type Filter struct{ Kind, Service, Region, State, Q string }
type Page struct {
	Data       []Resource `json:"data"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

const columns = `r.id,r.workspace_id,r.account_id,r.provider,r.service,r.kind,r.region,r.provider_id,
r.name,r.state,r.tags,r.summary,r.seen_at,r.version,r.created_at,r.updated_at`

func fields(r *Resource) []any {
	return []any{&r.ID, &r.WorkspaceID, &r.AccountID, &r.Provider, &r.Service, &r.Kind, &r.Region, &r.ProviderID,
		&r.Name, &r.State, &r.Tags, &r.Summary, &r.SeenAt, &r.Version, &r.CreatedAt, &r.UpdatedAt}
}

// Get returns pgx.ErrNoRows for missing or inaccessible resources. Both ends of
// every relation are checked within the same statement snapshot as the resource.
func (s *Store) Get(ctx context.Context, workspace int64, accounts []int64, id int64) (Detail, error) {
	out := Detail{Relations: []Related{}}
	if workspace <= 0 || len(accounts) == 0 || id <= 0 {
		return out, pgx.ErrNoRows
	}
	var relations []byte
	args := append(fields(&out.Resource), &out.Details, &relations)
	err := s.db.QueryRow(ctx, `SELECT `+columns+`,r.details,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('from_id',rr.from_id,'to_id',rr.to_id,'relation',rr.relation,
 'id',other.id,'kind',other.kind,'name',other.name) ORDER BY rr.from_id,rr.to_id,rr.relation)
 FROM resource_relations rr JOIN resources other ON other.id=CASE WHEN rr.from_id=r.id THEN rr.to_id ELSE rr.from_id END
 WHERE (rr.from_id=r.id OR rr.to_id=r.id) AND other.workspace_id=$1 AND other.account_id=ANY($2) AND EXISTS(SELECT 1 FROM cloud_accounts a WHERE a.workspace_id=$1 AND a.id=other.account_id AND a.provider=other.provider)), '[]'::jsonb)
 FROM resources r WHERE r.workspace_id=$1 AND r.account_id=ANY($2) AND r.id=$3 AND EXISTS(SELECT 1 FROM cloud_accounts a WHERE a.workspace_id=$1 AND a.id=r.account_id AND a.provider=r.provider)`, workspace, accounts, id).Scan(args...)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(relations, &out.Relations)
	return out, err
}
