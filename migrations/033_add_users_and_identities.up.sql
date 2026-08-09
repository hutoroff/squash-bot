CREATE TABLE users (
    id              BIGSERIAL PRIMARY KEY,
    display_name    TEXT        NOT NULL DEFAULT '',
    is_server_owner BOOLEAN     NOT NULL DEFAULT FALSE,
    dm_language     VARCHAR(5)  NOT NULL DEFAULT '',
    results_opt_out BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    tmp_tg          BIGINT -- migration scaffolding, dropped at end of this migration
);

CREATE TABLE user_identities (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider    TEXT   NOT NULL,
    external_id TEXT   NOT NULL,
    username    TEXT,
    first_name  TEXT,
    last_name   TEXT,
    photo_url   TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (provider, external_id)
);

CREATE INDEX user_identities_user_id_idx ON user_identities (user_id);

-- 1. One user per players row, folding in user_preferences via telegram_id.
INSERT INTO users (display_name, dm_language, results_opt_out, tmp_tg)
SELECT
    CASE WHEN p.username IS NOT NULL AND p.username <> ''
             THEN '@' || p.username
         ELSE TRIM(COALESCE(p.first_name, '') || ' ' || COALESCE(p.last_name, ''))
    END,
    COALESCE(up.dm_language, ''),
    COALESCE(up.results_opt_out, FALSE),
    p.telegram_id
FROM players p
LEFT JOIN user_preferences up ON up.telegram_id = p.telegram_id;

-- 2. Users for orphan user_preferences rows (prefs set, never joined a game).
INSERT INTO users (display_name, dm_language, results_opt_out, tmp_tg)
SELECT '', up.dm_language, up.results_opt_out, up.telegram_id
FROM user_preferences up
WHERE NOT EXISTS (SELECT 1 FROM players p WHERE p.telegram_id = up.telegram_id);

-- 3. Telegram identity for every backfilled user.
INSERT INTO user_identities (user_id, provider, external_id, username, first_name, last_name)
SELECT u.id, 'telegram', u.tmp_tg::text, p.username, p.first_name, p.last_name
FROM users u
LEFT JOIN players p ON p.telegram_id = u.tmp_tg
WHERE u.tmp_tg IS NOT NULL;

-- 4. players.user_id. NOT NULL is deferred to migration 034 (lands with the
-- Step 3 rekey of PlayerRepo.Upsert) so this migration stays additive: old
-- code inserting players without user_id keeps working until Upsert is rekeyed.
ALTER TABLE players ADD COLUMN user_id BIGINT;
UPDATE players p SET user_id = u.id FROM users u WHERE u.tmp_tg = p.telegram_id;
ALTER TABLE players ADD CONSTRAINT players_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id);
ALTER TABLE players ADD CONSTRAINT players_user_id_key UNIQUE (user_id);

-- 5. audit_events.actor_user_id (actor_tg_id kept as read-only history).
ALTER TABLE audit_events ADD COLUMN actor_user_id BIGINT REFERENCES users(id);
UPDATE audit_events ae SET actor_user_id = u.id FROM users u WHERE u.tmp_tg = ae.actor_tg_id;
CREATE INDEX audit_events_actor_user_id_idx ON audit_events (actor_user_id, occurred_at DESC);
DROP INDEX audit_events_actor_tg_id_idx;

-- 6. Drop migration scaffolding column.
ALTER TABLE users DROP COLUMN tmp_tg;
