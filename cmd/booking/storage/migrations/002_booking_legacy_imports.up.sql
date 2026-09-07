CREATE TABLE booking_imports (
    import_id           UUID PRIMARY KEY,
    request_fingerprint TEXT NOT NULL,
    service_key         TEXT NOT NULL,
    provider            TEXT NOT NULL,
    facility_id         TEXT NOT NULL,
    facility_uuid       TEXT NOT NULL,
    facility_slug       TEXT NOT NULL,
    sport               TEXT NOT NULL,
    account_id          BIGINT NOT NULL REFERENCES booking_accounts(id),
    court_uuid          TEXT NOT NULL,
    start_at            TIMESTAMPTZ NOT NULL,
    end_at              TIMESTAMPTZ NOT NULL,
    start_provider      TEXT NOT NULL,
    end_provider        TEXT NOT NULL,
    match_id            TEXT NOT NULL,
    provider_state      TEXT NOT NULL CHECK (provider_state IN ('active', 'cancelled')),
    actor               TEXT NOT NULL,
    reason              TEXT NOT NULL,
    revision            BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (char_length(request_fingerprint) BETWEEN 1 AND 128),
    CHECK (char_length(service_key) BETWEEN 1 AND 128),
    CHECK (char_length(provider) BETWEEN 1 AND 200),
    CHECK (char_length(facility_id) BETWEEN 1 AND 200),
    CHECK (char_length(facility_uuid) BETWEEN 1 AND 200),
    CHECK (char_length(facility_slug) BETWEEN 1 AND 200),
    CHECK (char_length(sport) BETWEEN 1 AND 200),
    CHECK (char_length(court_uuid) BETWEEN 1 AND 200),
    CHECK (char_length(start_provider) BETWEEN 1 AND 100),
    CHECK (char_length(end_provider) BETWEEN 1 AND 100),
    CHECK (char_length(match_id) BETWEEN 1 AND 200),
    CHECK (char_length(actor) BETWEEN 1 AND 128),
    CHECK (char_length(reason) BETWEEN 1 AND 256),
    CHECK (end_at > start_at)
);
CREATE UNIQUE INDEX booking_imports_provider_match
    ON booking_imports(provider, facility_uuid, match_id);
CREATE INDEX booking_imports_account
    ON booking_imports(account_id, created_at);

ALTER TABLE booking_provider_bookings
    ALTER COLUMN create_operation_id DROP NOT NULL,
    ADD COLUMN import_id UUID UNIQUE REFERENCES booking_imports(import_id) ON DELETE RESTRICT,
    ADD CONSTRAINT booking_provider_bookings_one_owner
        CHECK ((create_operation_id IS NOT NULL)::int + (import_id IS NOT NULL)::int = 1);

ALTER TABLE booking_resource_claims
    ALTER COLUMN operation_id DROP NOT NULL,
    ADD COLUMN import_id UUID REFERENCES booking_imports(import_id) ON DELETE RESTRICT,
    ADD CONSTRAINT booking_resource_claims_one_owner
        CHECK ((operation_id IS NOT NULL)::int + (import_id IS NOT NULL)::int = 1);
CREATE UNIQUE INDEX booking_resource_claims_import_type
    ON booking_resource_claims(import_id, claim_type)
    WHERE import_id IS NOT NULL;

-- P5 previously introduced logical tombstones before physical payload
-- compaction. Upgrade existing tombstones without changing their revisions.
UPDATE booking_operation_steps steps
SET evidence = '{}'::jsonb
FROM booking_operations operations
WHERE operations.operation_id = steps.operation_id
  AND operations.result_expired_at IS NOT NULL
  AND steps.evidence <> '{}'::jsonb;
