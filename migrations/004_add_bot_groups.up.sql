CREATE TABLE bot_groups (
    chat_id    BIGINT      PRIMARY KEY,
    title      TEXT        NOT NULL DEFAULT '',
    bot_is_admin BOOLEAN   NOT NULL DEFAULT FALSE,
    added_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
