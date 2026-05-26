ALTER TABLE venues ADD COLUMN auto_booking_courts_count INT NOT NULL DEFAULT 3;
ALTER TABLE venues DROP COLUMN auto_booking_games_count;
