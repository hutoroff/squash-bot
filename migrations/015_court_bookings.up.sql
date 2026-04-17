CREATE TABLE court_bookings (
    id            BIGSERIAL PRIMARY KEY,
    venue_id      BIGINT NOT NULL REFERENCES venues(id) ON DELETE CASCADE,
    game_date     DATE NOT NULL,
    court_uuid    TEXT NOT NULL,
    court_label   TEXT NOT NULL,
    match_id      TEXT NOT NULL,
    booking_uuid  TEXT NOT NULL,
    credential_id BIGINT REFERENCES venue_credentials(id) ON DELETE SET NULL,
    canceled_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(match_id)
);
CREATE INDEX idx_court_bookings_venue_date ON court_bookings(venue_id, game_date);
