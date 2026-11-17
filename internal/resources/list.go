// SPDX-License-Identifier: AGPL-3.0-only

package resources

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

type pageCursor struct {
	Version int    `json:"v"`
	Name    string `json:"n"`
	ID      int64  `json:"i"`
	Ceiling int64  `json:"m"`
	Scope   string `json:"s"`
}

func cursorScope(workspace int64, accounts []int64, filter Filter) string {
	ids := slices.Clone(accounts)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	raw, _ := json.Marshal(struct {
		Workspace int64
		Accounts  []int64
		Filter    Filter
	}{workspace, ids, filter})
	hash := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

// List sorts by name using the database collation, then ID. Nil/empty accounts grant no access.
// Limit defaults to 50, is capped at 200, and must not be negative. A cursor is
// bound to the workspace, account set and filters, never used as authorization.
func (s *Store) List(ctx context.Context, workspace int64, accounts []int64, filter Filter, cursor string, limit int) (Page, error) {
	out := Page{Data: []Resource{}}
	if limit < 0 {
		return out, ErrInvalid
	}
	if limit == 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	scope := cursorScope(workspace, accounts, filter)
	position := pageCursor{Version: 1, Scope: scope}
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(raw, &position) != nil || position.Version != 1 || position.ID <= 0 || position.Ceiling < position.ID || position.Scope != scope {
			return out, ErrCursor
		}
	}
	if workspace <= 0 || len(accounts) == 0 {
		return out, nil
	}
	if cursor == "" {
		if err := s.db.QueryRow(ctx, `SELECT COALESCE(max(id),0) FROM resources WHERE workspace_id=$1 AND account_id=ANY($2)`, workspace, accounts).Scan(&position.Ceiling); err != nil {
			return out, err
		}
	}
	query, args := listQuery(workspace, accounts, filter, position, limit+1)
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var resource Resource
		if err := rows.Scan(fields(&resource)...); err != nil {
			return out, err
		}
		out.Data = append(out.Data, resource)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(out.Data) > limit {
		out.Data = out.Data[:limit]
		last := out.Data[len(out.Data)-1]
		position.Name, position.ID = last.Name, last.ID
		raw, err := json.Marshal(position)
		if err != nil {
			return out, err
		}
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return out, nil
}

// Keep the search predicate directly on indexed columns. User input is always
// bound, and LIKE metacharacters are escaped so q means a literal substring.
func listQuery(workspace int64, accounts []int64, filter Filter, cursor pageCursor, limit int) (string, []any) {
	args := []any{workspace, accounts, cursor.Ceiling}
	query := `SELECT ` + columns + ` FROM resources r WHERE r.workspace_id=$1 AND r.account_id=ANY($2) AND r.id<=$3`
	for _, part := range []struct{ column, value string }{{"kind", filter.Kind}, {"service", filter.Service}, {"region", filter.Region}, {"state", filter.State}} {
		if part.value != "" {
			args = append(args, part.value)
			query += fmt.Sprintf(" AND r.%s=$%d", part.column, len(args))
		}
	}
	if filter.Q != "" {
		pattern := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(filter.Q) + "%"
		args = append(args, pattern)
		query += fmt.Sprintf(" AND (r.name ILIKE $%d OR r.provider_id ILIKE $%d)", len(args), len(args))
	}
	if cursor.ID > 0 {
		args = append(args, cursor.Name, cursor.ID)
		query += fmt.Sprintf(" AND (r.name,r.id)>($%d::text,$%d)", len(args)-1, len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY r.name,r.id LIMIT $%d", len(args))
	return query, args
}
