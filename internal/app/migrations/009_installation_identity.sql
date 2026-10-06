-- SPDX-License-Identifier: AGPL-3.0-only

CREATE TABLE installation_identity (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    installation_id varchar(128) NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);
