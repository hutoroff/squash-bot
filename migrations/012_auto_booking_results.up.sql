CREATE TABLE auto_booking_results (
    id           SERIAL PRIMARY KEY,
    venue_id     BIGINT NOT NULL REFERENCES venues(id) ON DELETE CASCADE,
    game_date    DATE NOT NULL,
    courts       TEXT NOT NULL,
    courts_count INT NOT NULL,
    created_at   TIMESTAMPTZ DEFAULT now(),
    UNIQUE (venue_id, game_date)
);
