-- SPDX-License-Identifier: AGPL-3.0-only

CREATE TABLE sync_state (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id bigint NOT NULL REFERENCES cloud_accounts(id) ON DELETE CASCADE,
    service text NOT NULL CHECK (service IN ('compute', 'eds.desktops', 'eds.users', 'oss.buckets', 'ecs.security_groups')),
    region text NOT NULL CHECK (region <> ''),
    "interval" interval NOT NULL CHECK ("interval" > interval '0 seconds'),
    last_started_at timestamptz,
    last_success_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    last_error_code text NOT NULL DEFAULT '',
    next_due_at timestamptz NOT NULL DEFAULT now(),
    empty boolean NOT NULL DEFAULT false CHECK (NOT empty OR region <> '*'),
    enabled boolean NOT NULL DEFAULT true,
    last_job_id bigint,
    last_attempt integer,
    UNIQUE (account_id, service, region)
);

CREATE INDEX sync_state_due ON sync_state (next_due_at, id) WHERE enabled;
COMMENT ON COLUMN sync_state.region IS
    '* denotes an account-wide collector: Azure/GCP compute, Alibaba OSS, security groups and EDS users. It is never an empty region. T14 replaces Alibaba account-wide rows as collectors become regional.';
CREATE INDEX jobs_finished_retention ON jobs (updated_at, id)
    WHERE status IN ('succeeded', 'failed');
