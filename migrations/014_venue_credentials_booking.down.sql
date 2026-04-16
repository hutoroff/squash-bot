ALTER TABLE venue_credentials
    DROP COLUMN IF EXISTS last_error_at,
    DROP COLUMN IF EXISTS max_courts;
