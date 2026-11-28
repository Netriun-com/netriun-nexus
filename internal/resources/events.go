// SPDX-License-Identifier: AGPL-3.0-only

package resources

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// DeleteExpiredEvents removes one workspace's expired replay history in a bounded
// batch. The daily retention worker supplies its transaction and lease fence.
// Event capture and one commit-time NOTIFY per workspace are installed by
// migration 019; all catalog writers, including relation cascades, participate.
// Resource field changes increase version; relation-only events reuse it.
func DeleteExpiredEvents(ctx context.Context, tx pgx.Tx, workspace int64, retention time.Duration, batch int) (int64, error) {
	if tx == nil || workspace <= 0 || retention < time.Second || batch < 1 {
		return 0, ErrInvalid
	}
	tag, err := tx.Exec(ctx, `WITH expired AS (
 SELECT id FROM resource_events WHERE workspace_id=$1 AND created_at < clock_timestamp()-$2::bigint*interval '1 microsecond'
 ORDER BY created_at,id LIMIT $3 FOR UPDATE SKIP LOCKED
) DELETE FROM resource_events WHERE workspace_id=$1 AND id IN (SELECT id FROM expired)`, workspace, retention.Microseconds(), batch)
	return tag.RowsAffected(), err
}
