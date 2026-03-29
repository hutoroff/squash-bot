-- Venue scheduling configuration fields
ALTER TABLE venues
    ADD COLUMN grace_period_hours INT NOT NULL DEFAULT 24,
    ADD COLUMN game_days TEXT NOT NULL DEFAULT '',
    ADD COLUMN booking_opens_days INT NOT NULL DEFAULT 14,
    ADD COLUMN last_booking_reminder_at TIMESTAMPTZ;

-- Group timezone for locale-aware scheduling (day-after cleanup at 3 AM, booking reminder at 10 AM)
ALTER TABLE bot_groups
    ADD COLUMN timezone VARCHAR(64) NOT NULL DEFAULT 'UTC';
