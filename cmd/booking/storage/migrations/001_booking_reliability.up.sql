CREATE TABLE booking_metadata (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (char_length(key) BETWEEN 1 AND 100),
    CHECK (char_length(value) BETWEEN 1 AND 1000)
);

CREATE TABLE booking_accounts (
    id                   BIGSERIAL PRIMARY KEY,
    provider             TEXT NOT NULL,
    identity             TEXT NOT NULL,
    state                TEXT NOT NULL DEFAULT 'available'
                         CHECK (state IN ('available', 'quarantined')),
    active_operation_id  UUID,
    quarantine_operation_id UUID,
    provider_user_alias  TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (provider, identity),
    CHECK (char_length(provider) BETWEEN 1 AND 200),
    CHECK (char_length(identity) BETWEEN 1 AND 200),
    CHECK (provider_user_alias IS NULL OR char_length(provider_user_alias) BETWEEN 1 AND 200),
    CHECK ((state = 'quarantined') = (quarantine_operation_id IS NOT NULL))
);

CREATE UNIQUE INDEX booking_accounts_provider_alias
    ON booking_accounts(provider, provider_user_alias)
    WHERE provider_user_alias IS NOT NULL;

CREATE TABLE booking_operations (
    operation_id        UUID PRIMARY KEY,
    request_fingerprint TEXT NOT NULL,
    kind                TEXT NOT NULL CHECK (kind IN ('create', 'cancel')),
    service_key         TEXT NOT NULL,
    provider            TEXT NOT NULL,
    facility_id         TEXT NOT NULL,
    facility_uuid       TEXT NOT NULL,
    facility_slug       TEXT NOT NULL,
    sport               TEXT NOT NULL,
    account_id          BIGINT NOT NULL REFERENCES booking_accounts(id),
    court_uuid          TEXT,
    start_at            TIMESTAMPTZ,
    end_at              TIMESTAMPTZ,
    start_provider      TEXT,
    end_provider        TEXT,
    target_match_id     TEXT,
    state               TEXT NOT NULL CHECK (state IN (
                            'accepted', 'running', 'not_dispatched',
                            'rejected_no_effect', 'succeeded', 'outcome_unknown'
                        )),
    phase               TEXT NOT NULL CHECK (phase IN (
                            'admission', 'login', 'reserve', 'payment',
                            'match_attachment', 'cancellation', 'finalization'
                        )),
    outcome             TEXT NOT NULL CHECK (outcome IN (
                            'pending', 'success', 'success_with_warning',
                            'no_effect', 'unknown'
                        )),
    executor_id         UUID,
    revision            BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    deadline_at         TIMESTAMPTZ NOT NULL,
    parent_operation_id UUID REFERENCES booking_operations(operation_id)
                        DEFERRABLE INITIALLY DEFERRED,
    root_operation_id   UUID NOT NULL REFERENCES booking_operations(operation_id)
                        DEFERRABLE INITIALLY DEFERRED,
    risk_reason         TEXT,
    result_expired_at   TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (char_length(request_fingerprint) BETWEEN 1 AND 128),
    CHECK (char_length(service_key) BETWEEN 1 AND 128),
    CHECK (char_length(provider) BETWEEN 1 AND 200),
    CHECK (char_length(facility_id) BETWEEN 1 AND 200),
    CHECK (char_length(facility_uuid) BETWEEN 1 AND 200),
    CHECK (char_length(facility_slug) BETWEEN 1 AND 200),
    CHECK (char_length(sport) BETWEEN 1 AND 200),
    CHECK (court_uuid IS NULL OR char_length(court_uuid) BETWEEN 1 AND 200),
    CHECK (start_provider IS NULL OR char_length(start_provider) BETWEEN 1 AND 100),
    CHECK (end_provider IS NULL OR char_length(end_provider) BETWEEN 1 AND 100),
    CHECK (target_match_id IS NULL OR char_length(target_match_id) BETWEEN 1 AND 200),
    CHECK (risk_reason IS NULL OR char_length(risk_reason) BETWEEN 1 AND 256),
    CHECK (
        (state IN ('accepted', 'running') AND executor_id IS NOT NULL
         AND outcome = 'pending' AND completed_at IS NULL)
        OR
        (state = 'not_dispatched' AND executor_id IS NULL
         AND outcome = 'no_effect' AND completed_at IS NOT NULL)
        OR
        (state = 'rejected_no_effect' AND executor_id IS NULL
         AND outcome = 'no_effect' AND completed_at IS NOT NULL)
        OR
        (state = 'succeeded' AND executor_id IS NULL
         AND outcome IN ('success', 'success_with_warning') AND completed_at IS NOT NULL)
        OR
        (state = 'outcome_unknown' AND executor_id IS NULL
         AND outcome = 'unknown' AND completed_at IS NOT NULL)
    ),
    CHECK (result_expired_at IS NULL OR state IN ('not_dispatched', 'rejected_no_effect', 'succeeded')),
    CHECK (
        (kind = 'create' AND court_uuid IS NOT NULL AND start_at IS NOT NULL AND end_at IS NOT NULL
         AND start_provider IS NOT NULL AND end_provider IS NOT NULL AND target_match_id IS NULL
         AND end_at > start_at)
        OR
        (kind = 'cancel' AND court_uuid IS NULL AND start_at IS NULL AND end_at IS NULL
         AND start_provider IS NULL AND end_provider IS NULL AND target_match_id IS NOT NULL)
    ),
    CHECK (
        (parent_operation_id IS NULL AND risk_reason IS NULL AND root_operation_id = operation_id)
        OR
        (parent_operation_id IS NOT NULL AND risk_reason IS NOT NULL AND root_operation_id <> operation_id)
    )
);

ALTER TABLE booking_accounts
    ADD CONSTRAINT booking_accounts_active_operation_fk
    FOREIGN KEY (active_operation_id) REFERENCES booking_operations(operation_id)
    DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE booking_accounts
    ADD CONSTRAINT booking_accounts_quarantine_operation_fk
    FOREIGN KEY (quarantine_operation_id) REFERENCES booking_operations(operation_id)
    DEFERRABLE INITIALLY DEFERRED;

CREATE UNIQUE INDEX booking_operations_one_child_per_parent
    ON booking_operations(parent_operation_id)
    WHERE parent_operation_id IS NOT NULL;
CREATE UNIQUE INDEX booking_operations_one_risk_child_per_root
    ON booking_operations(root_operation_id)
    WHERE parent_operation_id IS NOT NULL;
CREATE INDEX booking_operations_account_state
    ON booking_operations(account_id, state);
CREATE INDEX booking_operations_state_updated
    ON booking_operations(state, updated_at);

CREATE TABLE booking_operation_steps (
    id             BIGSERIAL PRIMARY KEY,
    operation_id   UUID NOT NULL REFERENCES booking_operations(operation_id) ON DELETE RESTRICT,
    phase          TEXT NOT NULL CHECK (phase IN (
                       'login', 'reserve', 'payment', 'match_attachment', 'cancellation'
                   )),
    attempt        INT NOT NULL CHECK (attempt > 0),
    status         TEXT NOT NULL CHECK (status IN ('intent', 'evidence')),
    effect         TEXT CHECK (effect IN ('no_effect', 'unknown', 'confirmed')),
    code           TEXT,
    evidence       JSONB NOT NULL DEFAULT '{}'::jsonb,
    executor_id    UUID NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (operation_id, phase, attempt, status),
    CHECK (char_length(code) IS NULL OR char_length(code) BETWEEN 1 AND 100),
    CHECK (octet_length(evidence::text) <= 16384),
    CHECK ((status = 'intent' AND effect IS NULL AND code IS NULL)
           OR (status = 'evidence' AND effect IS NOT NULL))
);
CREATE INDEX booking_operation_steps_operation
    ON booking_operation_steps(operation_id, id);

CREATE TABLE booking_resource_claims (
    id             BIGSERIAL PRIMARY KEY,
    operation_id   UUID NOT NULL REFERENCES booking_operations(operation_id) ON DELETE RESTRICT,
    claim_type     TEXT NOT NULL CHECK (claim_type IN ('occupancy', 'cancellation')),
    provider       TEXT NOT NULL,
    facility_uuid  TEXT NOT NULL,
    court_uuid     TEXT,
    start_at       TIMESTAMPTZ,
    end_at         TIMESTAMPTZ,
    match_id       TEXT,
    state          TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'released')),
    released_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (operation_id, claim_type),
    CHECK (char_length(provider) BETWEEN 1 AND 200),
    CHECK (char_length(facility_uuid) BETWEEN 1 AND 200),
    CHECK (court_uuid IS NULL OR char_length(court_uuid) BETWEEN 1 AND 200),
    CHECK (match_id IS NULL OR char_length(match_id) BETWEEN 1 AND 200),
    CHECK (
        (claim_type = 'occupancy' AND court_uuid IS NOT NULL AND start_at IS NOT NULL
         AND end_at IS NOT NULL AND match_id IS NULL AND end_at > start_at)
        OR
        (claim_type = 'cancellation' AND court_uuid IS NULL AND start_at IS NULL
         AND end_at IS NULL AND match_id IS NOT NULL)
    ),
    CHECK ((state = 'active' AND released_at IS NULL)
           OR (state = 'released' AND released_at IS NOT NULL))
);
CREATE INDEX booking_resource_claims_occupancy_lookup
    ON booking_resource_claims(provider, facility_uuid, court_uuid, start_at, end_at)
    WHERE claim_type = 'occupancy' AND state = 'active';
CREATE INDEX booking_resource_claims_cancellation_lookup
    ON booking_resource_claims(provider, facility_uuid, match_id)
    WHERE claim_type = 'cancellation' AND state = 'active';

CREATE TABLE booking_provider_bookings (
    id                    BIGSERIAL PRIMARY KEY,
    create_operation_id   UUID NOT NULL UNIQUE REFERENCES booking_operations(operation_id) ON DELETE RESTRICT,
    provider              TEXT NOT NULL,
    facility_uuid         TEXT NOT NULL,
    booking_uuid          TEXT,
    booking_id            BIGINT,
    payment_id            BIGINT,
    match_id              TEXT,
    payment_state         TEXT NOT NULL DEFAULT 'unknown'
                          CHECK (payment_state IN ('unknown', 'confirmed', 'rejected')),
    booking_state         TEXT NOT NULL DEFAULT 'unknown'
                          CHECK (booking_state IN ('unknown', 'reserved', 'confirmed', 'cancelled')),
    cancellation_operation_id UUID REFERENCES booking_operations(operation_id) ON DELETE RESTRICT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (char_length(provider) BETWEEN 1 AND 200),
    CHECK (char_length(facility_uuid) BETWEEN 1 AND 200),
    CHECK (booking_uuid IS NULL OR char_length(booking_uuid) BETWEEN 1 AND 200),
    CHECK (booking_id IS NULL OR booking_id > 0),
    CHECK (payment_id IS NULL OR payment_id > 0),
    CHECK (match_id IS NULL OR char_length(match_id) BETWEEN 1 AND 200)
);
CREATE UNIQUE INDEX booking_provider_bookings_booking_uuid
    ON booking_provider_bookings(provider, facility_uuid, booking_uuid)
    WHERE booking_uuid IS NOT NULL;
CREATE UNIQUE INDEX booking_provider_bookings_match_id
    ON booking_provider_bookings(provider, facility_uuid, match_id)
    WHERE match_id IS NOT NULL;

CREATE TABLE booking_operation_events (
    id             BIGSERIAL PRIMARY KEY,
    operation_id   UUID NOT NULL REFERENCES booking_operations(operation_id) ON DELETE RESTRICT,
    event_key      UUID NOT NULL,
    event_type     TEXT NOT NULL,
    expected_revision BIGINT,
    resulting_revision BIGINT NOT NULL,
    reason         TEXT,
    evidence       JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (operation_id, event_key),
    CHECK (char_length(event_type) BETWEEN 1 AND 100),
    CHECK (reason IS NULL OR char_length(reason) BETWEEN 1 AND 256),
    CHECK (octet_length(evidence::text) <= 16384),
    CHECK (expected_revision IS NULL OR expected_revision > 0),
    CHECK (resulting_revision > 0),
    CHECK (expected_revision IS NULL OR resulting_revision = expected_revision + 1)
);
CREATE INDEX booking_operation_events_operation
    ON booking_operation_events(operation_id, id);
