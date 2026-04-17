ALTER TABLE auto_booking_results ADD COLUMN game_time TEXT NOT NULL DEFAULT '';
ALTER TABLE auto_booking_results ADD COLUMN game_id BIGINT REFERENCES games(id) ON DELETE SET NULL;
ALTER TABLE auto_booking_results DROP CONSTRAINT auto_booking_results_venue_id_game_date_key;
ALTER TABLE auto_booking_results ADD CONSTRAINT auto_booking_results_venue_id_game_date_game_time_key
    UNIQUE (venue_id, game_date, game_time);
