CREATE TABLE management_booking_intents (
    id                  BIGSERIAL PRIMARY KEY,
    request_key         TEXT NOT NULL UNIQUE,
    request_fingerprint TEXT NOT NULL,
    action              TEXT NOT NULL CHECK (action IN ('create', 'cancel')),
    source              TEXT NOT NULL CHECK (source IN ('scheduled', 'manual', 'recovery')),
    service_key         TEXT NOT NULL,
    provider            TEXT NOT NULL,
    facility_id         TEXT NOT NULL,
    facility_uuid       TEXT NOT NULL,
    facility_slug       TEXT NOT NULL,
    sport               TEXT NOT NULL,
    group_id            BIGINT NOT NULL,
    venue_id            BIGINT NOT NULL,
    game_id             BIGINT REFERENCES games(id) ON DELETE SET NULL,
    game_date           DATE NOT NULL,
    game_time           TEXT NOT NULL,
    requested_count     INTEGER NOT NULL CHECK (requested_count > 0),
    generation          INTEGER NOT NULL DEFAULT 1 CHECK (generation > 0),
    state               TEXT NOT NULL DEFAULT 'planned'
        CHECK (state IN ('planned', 'active', 'completed', 'partial', 'unknown')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (char_length(request_key) BETWEEN 1 AND 200),
    CHECK (char_length(request_fingerprint) = 64),
    CHECK (char_length(game_time) BETWEEN 1 AND 16)
);

CREATE INDEX idx_management_booking_intents_venue_date
    ON management_booking_intents(venue_id, game_date, game_time);
CREATE INDEX idx_management_booking_intents_unfinished
    ON management_booking_intents(updated_at)
    WHERE state IN ('planned', 'active', 'unknown');
CREATE INDEX idx_management_booking_intents_venue_unfinished
    ON management_booking_intents(venue_id)
    WHERE state IN ('planned', 'active', 'unknown');

CREATE TABLE management_booking_operations (
    operation_id        UUID PRIMARY KEY,
    intent_id           BIGINT NOT NULL REFERENCES management_booking_intents(id) ON DELETE RESTRICT,
    dedup_key           TEXT NOT NULL,
    request_fingerprint TEXT NOT NULL,
    kind                TEXT NOT NULL CHECK (kind IN ('create', 'cancel')),
    court_uuid          TEXT,
    court_label         TEXT NOT NULL DEFAULT '',
    start_provider      TEXT,
    end_provider        TEXT,
    target_match_id     TEXT,
    credential_id       BIGINT REFERENCES venue_credentials(id) ON DELETE SET NULL,
    parent_operation_id UUID REFERENCES management_booking_operations(operation_id) ON DELETE RESTRICT,
    root_operation_id   UUID NOT NULL REFERENCES management_booking_operations(operation_id) ON DELETE RESTRICT,
    risk_reason         TEXT NOT NULL DEFAULT '',
    state               TEXT NOT NULL DEFAULT 'planned'
        CHECK (state IN ('planned', 'accepted', 'running', 'not_dispatched', 'succeeded', 'rejected_no_effect', 'outcome_unknown')),
    phase               TEXT NOT NULL DEFAULT 'admission',
    outcome             TEXT NOT NULL DEFAULT 'pending'
        CHECK (outcome IN ('pending', 'success', 'success_with_warning', 'no_effect', 'unknown')),
    code                TEXT NOT NULL DEFAULT '',
    warnings            JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(warnings) = 'array'),
    result_expired      BOOLEAN NOT NULL DEFAULT FALSE,
    provider_state      TEXT NOT NULL DEFAULT '',
    booking_uuid        TEXT NOT NULL DEFAULT '',
    booking_id          BIGINT,
    payment_id          BIGINT,
    match_id            TEXT NOT NULL DEFAULT '',
    revision            BIGINT NOT NULL DEFAULT 0 CHECK (revision >= 0),
    completed_at        TIMESTAMPTZ,
    last_recovery_at    TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(intent_id, dedup_key),
    CHECK (char_length(dedup_key) BETWEEN 1 AND 200),
    CHECK (char_length(request_fingerprint) = 64),
    CHECK (
        (kind = 'create' AND court_uuid IS NOT NULL AND start_provider IS NOT NULL AND end_provider IS NOT NULL AND target_match_id IS NULL)
        OR
        (kind = 'cancel' AND target_match_id IS NOT NULL AND court_uuid IS NULL AND start_provider IS NULL AND end_provider IS NULL)
    ),
    CHECK (parent_operation_id IS NULL OR parent_operation_id <> operation_id),
    CHECK (char_length(risk_reason) <= 500)
);

CREATE INDEX idx_management_booking_operations_intent
    ON management_booking_operations(intent_id, created_at);
CREATE UNIQUE INDEX uq_management_booking_operations_risk_parent
    ON management_booking_operations(parent_operation_id)
    WHERE parent_operation_id IS NOT NULL;
CREATE INDEX idx_management_booking_operations_credential_unfinished
    ON management_booking_operations(credential_id, updated_at)
    WHERE state IN ('planned', 'accepted', 'running', 'outcome_unknown');
CREATE INDEX idx_management_booking_operations_recovery
    ON management_booking_operations(last_recovery_at, updated_at)
    WHERE state IN ('planned', 'accepted', 'running', 'outcome_unknown')
       OR (kind = 'create' AND state = 'succeeded' AND match_id <> '');
CREATE INDEX idx_management_booking_operations_match
    ON management_booking_operations(target_match_id)
    WHERE target_match_id IS NOT NULL;

ALTER TABLE court_bookings
    ADD COLUMN management_create_operation_id UUID REFERENCES management_booking_operations(operation_id) ON DELETE RESTRICT,
    ADD COLUMN booking_service_key TEXT,
    ADD COLUMN booking_provider TEXT,
    ADD COLUMN booking_facility_id TEXT,
    ADD COLUMN booking_facility_uuid TEXT,
    ADD COLUMN booking_facility_slug TEXT,
    ADD COLUMN booking_sport TEXT,
    ADD CONSTRAINT court_bookings_reliable_origin_complete CHECK (
        (management_create_operation_id IS NULL
            AND booking_service_key IS NULL AND booking_provider IS NULL AND booking_facility_id IS NULL
            AND booking_facility_uuid IS NULL AND booking_facility_slug IS NULL AND booking_sport IS NULL)
        OR
        (management_create_operation_id IS NOT NULL
            AND booking_service_key IS NOT NULL AND booking_provider IS NOT NULL AND booking_facility_id IS NOT NULL
            AND booking_facility_uuid IS NOT NULL AND booking_facility_slug IS NOT NULL AND booking_sport IS NOT NULL)
    );
CREATE INDEX idx_court_bookings_management_create_operation
    ON court_bookings(management_create_operation_id)
    WHERE management_create_operation_id IS NOT NULL;
