ALTER TABLE venue_credentials
    ADD COLUMN last_error_at TIMESTAMPTZ,
    ADD COLUMN max_courts    INT NOT NULL DEFAULT 3;
