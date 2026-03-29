CREATE TABLE venues (
    id         SERIAL PRIMARY KEY,
    group_id   BIGINT NOT NULL REFERENCES bot_groups(chat_id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    courts     TEXT NOT NULL,
    time_slots TEXT NOT NULL DEFAULT '',
    address    TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(group_id, name)
);

ALTER TABLE games ADD COLUMN venue_id INTEGER REFERENCES venues(id) ON DELETE SET NULL;
