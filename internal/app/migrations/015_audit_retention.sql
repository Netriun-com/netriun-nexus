-- SPDX-License-Identifier: AGPL-3.0-only

-- Persist cadence separately from job history, which has its own cleanup.
ALTER TABLE settings ADD COLUMN audit_retention_next_due_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX settings_audit_retention_due ON settings (audit_retention_next_due_at, workspace_id);
