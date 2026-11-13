-- SPDX-License-Identifier: AGPL-3.0-only

CREATE TABLE jobs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind text NOT NULL CHECK (kind <> ''),
    payload jsonb NOT NULL DEFAULT '{}',
    status text NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    priority integer NOT NULL DEFAULT 0,
    attempts integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL DEFAULT 3 CHECK (max_attempts > 0),
    run_after timestamptz NOT NULL DEFAULT now(),
    locked_by text,
    locked_until timestamptz,
    concurrency_key text NOT NULL DEFAULT '',
    dedupe_key text CHECK (dedupe_key <> ''),
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (attempts >= 0 AND attempts <= max_attempts),
    CHECK ((status = 'running' AND locked_by IS NOT NULL AND locked_by <> '' AND locked_until IS NOT NULL)
        OR (status <> 'running' AND locked_by IS NULL AND locked_until IS NULL))
);

CREATE UNIQUE INDEX jobs_active_dedupe ON jobs (dedupe_key)
    WHERE status IN ('queued', 'running');
CREATE INDEX jobs_due ON jobs (priority DESC, run_after, id)
    WHERE status = 'queued';
CREATE INDEX jobs_running_key ON jobs (concurrency_key) WHERE status = 'running';
CREATE INDEX jobs_expired_lease ON jobs (locked_until, id) WHERE status = 'running';
