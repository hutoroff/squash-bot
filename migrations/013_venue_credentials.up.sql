CREATE TABLE venue_credentials (
    id           BIGSERIAL PRIMARY KEY,
    venue_id     BIGINT NOT NULL REFERENCES venues(id) ON DELETE CASCADE,
    login        TEXT NOT NULL,
    enc_password TEXT NOT NULL,
    priority     INT NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(venue_id, login)
);
