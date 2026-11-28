-- SPDX-License-Identifier: AGPL-3.0-only

CREATE TABLE resource_events (
    id bigserial PRIMARY KEY,
    workspace_id bigint NOT NULL,
    account_id bigint NOT NULL,
    type text NOT NULL CHECK (type IN ('resource.created','resource.updated','resource.deleted','sync.started','sync.finished')),
    service text NOT NULL,
    region text NOT NULL,
    -- Deliberately no resource FK: delete events outlive their resource.
    resource_id bigint,
    version bigint,
    success boolean,
    error_code text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY(account_id,workspace_id) REFERENCES cloud_accounts(id,workspace_id) ON DELETE CASCADE,
    CHECK ((type LIKE 'resource.%' AND resource_id IS NOT NULL AND version IS NOT NULL AND version > 0 AND success IS NULL AND error_code='')
        OR (type='sync.started' AND resource_id IS NULL AND version IS NULL AND success IS NULL AND error_code='')
        OR (type='sync.finished' AND resource_id IS NULL AND version IS NULL AND success IS NOT NULL
            AND ((success AND error_code='') OR (NOT success AND error_code<>''))))
);
CREATE INDEX resource_events_replay_idx ON resource_events(workspace_id,id);
CREATE INDEX resource_events_retention_idx ON resource_events(workspace_id,created_at,id);

-- Allocate IDs only AFTER taking the workspace transaction lock. Sequence
-- allocation alone is not commit ordered: a replay cursor could otherwise pass
-- an uncommitted lower ID. Different workspaces do not block one another.
ALTER TABLE resource_events ALTER COLUMN id DROP DEFAULT;
CREATE FUNCTION resource_event_id() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended(TG_TABLE_SCHEMA || ':nexus_events:' || NEW.workspace_id,0));
    NEW.id := nextval(pg_get_serial_sequence(format('%I.resource_events',TG_TABLE_SCHEMA),'id'));
    RETURN NEW;
END $$;
CREATE TRIGGER resource_event_id BEFORE INSERT ON resource_events
    FOR EACH ROW EXECUTE FUNCTION resource_event_id();

-- Several reconciles/kinds/regions can share a transaction. At commit, emit one
-- wakeup per workspace with its final maximum ID. Transaction-local settings
-- disappear on commit/rollback; event contents never enter NOTIFY.
CREATE FUNCTION resource_event_notify() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE setting_name text; newest bigint;
BEGIN
    setting_name := 'nexus_events.notified_' || NEW.workspace_id;
    IF current_setting(setting_name,true) = 'yes' THEN RETURN NULL; END IF;
    SELECT max(id) INTO newest FROM resource_events WHERE workspace_id=NEW.workspace_id;
    IF newest IS NOT NULL THEN
        PERFORM pg_notify('nexus_events',NEW.workspace_id || ':' || newest);
        PERFORM set_config(setting_name,'yes',true);
    END IF;
    RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER resource_event_notify AFTER INSERT ON resource_events
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION resource_event_notify();

-- Statement transition tables keep bulk reconciliation set-based. seen_at-only
-- changes emit nothing. Capturing deletes here also covers cascaded relation
-- removals and future store writers, without relying on HTTP handlers.
CREATE FUNCTION resource_created_events() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO resource_events(workspace_id,account_id,type,service,region,resource_id,version)
        SELECT workspace_id,account_id,'resource.created',service,region,id,version FROM new_resources;
    RETURN NULL;
END $$;
CREATE FUNCTION resource_updated_events() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO resource_events(workspace_id,account_id,type,service,region,resource_id,version)
        SELECT n.workspace_id,n.account_id,'resource.updated',n.service,n.region,n.id,n.version
        FROM new_resources n JOIN old_resources o USING(id) WHERE n.version<>o.version;
    RETURN NULL;
END $$;
CREATE FUNCTION resource_deleted_events() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO resource_events(workspace_id,account_id,type,service,region,resource_id,version)
        SELECT r.workspace_id,r.account_id,'resource.deleted',r.service,r.region,r.id,r.version
        FROM old_resources r JOIN cloud_accounts a ON a.id=r.account_id AND a.workspace_id=r.workspace_id;
    RETURN NULL;
END $$;
CREATE TRIGGER resource_created_events AFTER INSERT ON resources
    REFERENCING NEW TABLE AS new_resources FOR EACH STATEMENT EXECUTE FUNCTION resource_created_events();
CREATE TRIGGER resource_updated_events AFTER UPDATE ON resources
    REFERENCING OLD TABLE AS old_resources NEW TABLE AS new_resources FOR EACH STATEMENT EXECUTE FUNCTION resource_updated_events();
CREATE TRIGGER resource_deleted_events AFTER DELETE ON resources
    REFERENCING OLD TABLE AS old_resources FOR EACH STATEMENT EXECUTE FUNCTION resource_deleted_events();

-- A relation delta invalidates each surviving source once per statement. Its
-- content version stays unchanged: that version describes the resource fields.
-- The same trigger handles target-deletion cascades. Deleted sources are skipped.
CREATE FUNCTION resource_relation_events() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO resource_events(workspace_id,account_id,type,service,region,resource_id,version)
        SELECT DISTINCT r.workspace_id,r.account_id,'resource.updated',r.service,r.region,r.id,r.version
        FROM changed_relations e JOIN resources r ON r.id=e.from_id
        JOIN cloud_accounts a ON a.id=r.account_id AND a.workspace_id=r.workspace_id;
    RETURN NULL;
END $$;
CREATE TRIGGER resource_relation_insert_events AFTER INSERT ON resource_relations
    REFERENCING NEW TABLE AS changed_relations FOR EACH STATEMENT EXECUTE FUNCTION resource_relation_events();
CREATE TRIGGER resource_relation_delete_events AFTER DELETE ON resource_relations
    REFERENCING OLD TABLE AS changed_relations FOR EACH STATEMENT EXECUTE FUNCTION resource_relation_events();
