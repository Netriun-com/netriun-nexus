-- SPDX-License-Identifier: AGPL-3.0-only

ALTER TABLE installation_identity
ADD COLUMN license_time_floor timestamptz NOT NULL DEFAULT now();
