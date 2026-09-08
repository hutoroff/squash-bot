CREATE TABLE service_catalog (
    service_key       TEXT PRIMARY KEY,
    service_kind      TEXT NOT NULL,
    implementation    TEXT NOT NULL,
    scope             JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(scope) = 'object'),
    contracts         JSONB NOT NULL CHECK (jsonb_typeof(contracts) = 'array'),
    capabilities      JSONB NOT NULL CHECK (jsonb_typeof(capabilities) = 'array'),
    first_seen_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (char_length(service_key) BETWEEN 1 AND 128),
    CHECK (char_length(service_kind) BETWEEN 1 AND 128),
    CHECK (char_length(implementation) BETWEEN 1 AND 128),
    CHECK (pg_column_size(scope) <= 8192),
    CHECK (pg_column_size(contracts) <= 8192),
    CHECK (pg_column_size(capabilities) <= 32768)
);

CREATE TABLE service_instances (
    instance_id            UUID PRIMARY KEY,
    service_key            TEXT NOT NULL REFERENCES service_catalog(service_key) ON DELETE RESTRICT,
    descriptor_fingerprint TEXT NOT NULL CHECK (char_length(descriptor_fingerprint) = 64),
    descriptor             JSONB NOT NULL CHECK (jsonb_typeof(descriptor) = 'object' AND pg_column_size(descriptor) <= 65536),
    reported_state         TEXT NOT NULL CHECK (reported_state IN ('starting', 'ready', 'not_ready', 'draining')),
    terminal_state         TEXT CHECK (terminal_state IN ('draining', 'deregistered')),
    last_heartbeat_at      TIMESTAMPTZ NOT NULL,
    lease_expires_at       TIMESTAMPTZ NOT NULL,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_service_instances_live
    ON service_instances(service_key, lease_expires_at)
    WHERE terminal_state IS NULL;
CREATE INDEX idx_service_instances_expiry
    ON service_instances(lease_expires_at);
