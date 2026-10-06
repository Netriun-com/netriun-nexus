-- SPDX-License-Identifier: AGPL-3.0-only

ALTER TABLE cloud_accounts ADD COLUMN IF NOT EXISTS sync_error_code text NOT NULL DEFAULT '';
