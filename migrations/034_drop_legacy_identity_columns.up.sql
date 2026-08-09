-- players.user_id is now populated by PlayerRepo.Upsert on every insert;
-- safe to enforce NOT NULL (deferred from migration 033).
ALTER TABLE players ALTER COLUMN user_id SET NOT NULL;

ALTER TABLE players DROP COLUMN telegram_id, DROP COLUMN username,
                    DROP COLUMN first_name, DROP COLUMN last_name;

DROP TABLE user_preferences;
