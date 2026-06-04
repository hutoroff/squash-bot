CREATE TABLE user_preferences (
    telegram_id BIGINT PRIMARY KEY,
    dm_language VARCHAR(5) NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
