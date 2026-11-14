-- SPDX-License-Identifier: AGPL-3.0-only

-- A refresh is only a durable polling receipt, never a second collection job.
CREATE TABLE inventory_refreshes (
    id text PRIMARY KEY,
    workspace_id bigint NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    account_ids bigint[] NOT NULL,
    job_ids bigint[] NOT NULL CHECK (cardinality(job_ids) > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX inventory_refreshes_retention ON inventory_refreshes(created_at);

-- Discovery cadence survives worker restarts and is shared across replicas.
CREATE TABLE sync_discovery (
    account_id bigint PRIMARY KEY REFERENCES cloud_accounts(id) ON DELETE CASCADE,
    next_due_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- Compute jobs consume both account and provider limits without another job kind.
ALTER TABLE jobs ADD COLUMN started_at timestamptz;
ALTER TABLE jobs ADD COLUMN provider_key text NOT NULL DEFAULT '';
CREATE INDEX jobs_running_provider ON jobs(provider_key) WHERE status='running';
UPDATE jobs j SET provider_key='provider:'||a.provider FROM cloud_accounts a
WHERE j.kind='scheduled.sync' AND j.payload->>'service'='compute'
  AND j.payload->>'account_id'=a.id::text AND j.payload->>'workspace_id'=a.workspace_id::text
  AND j.status IN ('queued','running');
