-- SPDX-License-Identifier: AGPL-3.0-only

COMMENT ON TABLE instances IS 'Deprecated: inventory is stored in resources; retained for upgrade inspection, no longer written or read by Nexus.';
COMMENT ON TABLE service_snapshots IS 'Deprecated: service inventory is stored in resources; retained for upgrade inspection, no longer written or read by Nexus.';
