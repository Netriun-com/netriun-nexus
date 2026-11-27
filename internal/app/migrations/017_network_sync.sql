-- SPDX-License-Identifier: AGPL-3.0-only
ALTER TABLE sync_state DROP CONSTRAINT sync_state_service_check;
ALTER TABLE sync_state ADD CONSTRAINT sync_state_service_check CHECK
 (service IN ('compute','eds.desktops','eds.users','oss.buckets','ecs.security_groups','network'));
-- A network job can discover edges whose source belongs to compute. Preserve
-- those edges when compute independently reconciles its own outgoing relations.
ALTER TABLE resource_relations ADD COLUMN owner_service text NOT NULL DEFAULT '';
UPDATE resource_relations rr SET owner_service=r.service FROM resources r WHERE r.id=rr.from_id;
COMMENT ON COLUMN resource_relations.owner_service IS 'Service whose complete collection owns this relation; independent resource syncs preserve it.';
