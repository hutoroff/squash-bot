CREATE TABLE IF NOT EXISTS games (
    id                   BIGSERIAL PRIMARY KEY,
    chat_id              BIGINT NOT NULL,
    message_id           BIGINT,
    game_date            TIMESTAMPTZ NOT NULL,
    courts_count         INT NOT NULL,
    notified_day_before  BOOLEAN NOT NULL DEFAULT false,
    completed            BOOLEAN NOT NULL DEFAULT false,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS players (
    id          BIGSERIAL PRIMARY KEY,
    telegram_id BIGINT NOT NULL UNIQUE,
    username    TEXT,
    first_name  TEXT,
    last_name   TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS game_participations (
    id        BIGSERIAL PRIMARY KEY,
    game_id   BIGINT NOT NULL REFERENCES games(id),
    player_id BIGINT NOT NULL REFERENCES players(id),
    status    TEXT NOT NULL CHECK (status IN ('registered', 'skipped')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (game_id, player_id)
);
