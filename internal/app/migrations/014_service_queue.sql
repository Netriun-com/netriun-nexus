-- SPDX-License-Identifier: AGPL-3.0-only
-- Receipts preserve the existing polling contracts; executable work remains scheduled.sync.
ALTER TABLE inventory_refreshes ADD COLUMN service text NOT NULL DEFAULT 'compute';
ALTER TABLE inventory_refreshes ADD COLUMN region text NOT NULL DEFAULT '';
UPDATE jobs j SET provider_key='provider:'||a.provider FROM cloud_accounts a
WHERE j.kind='scheduled.sync' AND j.status IN ('queued','running')
 AND j.payload->>'account_id'=a.id::text AND j.payload->>'workspace_id'=a.workspace_id::text;
COMMENT ON COLUMN sync_state.region IS 'Provider region; EDS users use directory endpoint regions. * denotes a global API (Azure/GCP compute or OSS ListBuckets).';
