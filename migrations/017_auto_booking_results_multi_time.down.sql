ALTER TABLE auto_booking_results DROP CONSTRAINT auto_booking_results_venue_id_game_date_game_time_key;
ALTER TABLE auto_booking_results ADD CONSTRAINT auto_booking_results_venue_id_game_date_key
    UNIQUE (venue_id, game_date);
ALTER TABLE auto_booking_results DROP COLUMN game_id;
ALTER TABLE auto_booking_results DROP COLUMN game_time;
