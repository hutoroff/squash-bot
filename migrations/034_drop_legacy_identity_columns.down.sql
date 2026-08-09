-- Best-effort reconstruction from user_identities/users. Assumes every
-- players row has a telegram identity (the only provider so far) — take a DB
-- backup before deploy per the standard release flow.
ALTER TABLE players ADD COLUMN telegram_id BIGINT, ADD COLUMN username TEXT,
                    ADD COLUMN first_name TEXT, ADD COLUMN last_name TEXT;

UPDATE players p
SET telegram_id = ti.external_id::bigint,
    username    = ti.username,
    first_name  = ti.first_name,
    last_name   = ti.last_name
FROM user_identities ti
WHERE ti.user_id = p.user_id AND ti.provider = 'telegram';

ALTER TABLE players ALTER COLUMN telegram_id SET NOT NULL;
ALTER TABLE players ADD CONSTRAINT players_telegram_id_key UNIQUE (telegram_id);
ALTER TABLE players ALTER COLUMN user_id DROP NOT NULL;

CREATE TABLE user_preferences (
    telegram_id     BIGINT PRIMARY KEY,
    dm_language     VARCHAR(5)  NOT NULL DEFAULT '',
    results_opt_out BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO user_preferences (telegram_id, dm_language, results_opt_out, created_at, updated_at)
SELECT ti.external_id::bigint, u.dm_language, u.results_opt_out, u.created_at, u.updated_at
FROM users u
JOIN user_identities ti ON ti.user_id = u.id AND ti.provider = 'telegram'
WHERE u.dm_language <> '' OR u.results_opt_out = TRUE;
