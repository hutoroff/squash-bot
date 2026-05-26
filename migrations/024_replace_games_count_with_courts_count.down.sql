ALTER TABLE venues ADD COLUMN auto_booking_games_count INT NOT NULL DEFAULT 0;
ALTER TABLE venues DROP COLUMN auto_booking_courts_count;
