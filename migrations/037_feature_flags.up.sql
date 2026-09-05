-- NULL group_id is the global setting; no rows are seeded: every flag defaults off.
CREATE TABLE feature_flag_overrides (
    key TEXT NOT NULL,
    group_id BIGINT REFERENCES bot_groups(chat_id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL,
    UNIQUE NULLS NOT DISTINCT (key, group_id)
);
