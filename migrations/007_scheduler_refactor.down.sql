ALTER TABLE venues
    DROP COLUMN IF EXISTS grace_period_hours,
    DROP COLUMN IF EXISTS game_days,
    DROP COLUMN IF EXISTS booking_opens_days,
    DROP COLUMN IF EXISTS last_booking_reminder_at;

ALTER TABLE bot_groups
    DROP COLUMN IF EXISTS timezone;
