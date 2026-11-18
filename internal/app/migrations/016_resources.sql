-- SPDX-License-Identifier: AGPL-3.0-only

-- Extensions are database-wide, including when tests use isolated schemas.
-- Keep pg_trgm outside disposable application schemas and serialize concurrent
-- installers (some migration test fixtures execute files directly).
SELECT pg_advisory_xact_lock(hashtextextended('nexus.pg_trgm.install', 0));
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

CREATE TABLE resources (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id bigint NOT NULL,
    account_id bigint NOT NULL,
    provider text NOT NULL CHECK (provider <> ''),
    service text NOT NULL CHECK (service <> ''),
    kind text NOT NULL CHECK (kind <> ''),
    region text NOT NULL CHECK (region <> ''),
    provider_id text NOT NULL CHECK (provider_id <> ''),
    name text NOT NULL DEFAULT '',
    state text NOT NULL DEFAULT '',
    tags jsonb NOT NULL DEFAULT '{}',
    summary jsonb NOT NULL DEFAULT '{}',
    details jsonb NOT NULL DEFAULT '{}',
    seen_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY(account_id, workspace_id) REFERENCES cloud_accounts(id, workspace_id) ON DELETE CASCADE,
    UNIQUE(account_id, service, region, kind, provider_id)
);
CREATE INDEX resources_scope_idx ON resources(workspace_id, account_id, service, region);
CREATE INDEX resources_name_idx ON resources(workspace_id, kind, name, id);
-- Respect a DBA's existing installation of pg_trgm in a different schema.
DO $$
DECLARE extension_schema text;
BEGIN
    SELECT n.nspname INTO extension_schema FROM pg_extension e
        JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='pg_trgm';
    EXECUTE format('CREATE INDEX resources_name_trgm_idx ON resources USING gin (name %I.gin_trgm_ops)', extension_schema);
    EXECUTE format('CREATE INDEX resources_provider_id_trgm_idx ON resources USING gin (provider_id %I.gin_trgm_ops)', extension_schema);
END $$;

CREATE TABLE resource_relations (
    from_id bigint NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    to_id bigint NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    relation text NOT NULL CHECK (relation <> ''),
    PRIMARY KEY(from_id, to_id, relation)
);
-- The primary key supplies the from_id index.
CREATE INDEX resource_relations_to_idx ON resource_relations(to_id);
