CREATE TABLE player_ratings (
    group_id      BIGINT NOT NULL,
    player_id     BIGINT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    rating        DOUBLE PRECISION NOT NULL DEFAULT 1500,
    rd            DOUBLE PRECISION NOT NULL DEFAULT 350,
    volatility    DOUBLE PRECISION NOT NULL DEFAULT 0.06,
    games_played  INT NOT NULL DEFAULT 0,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, player_id)
);

CREATE TABLE rating_changes (
    id             BIGSERIAL PRIMARY KEY,
    game_result_id BIGINT NOT NULL REFERENCES game_results(id) ON DELETE CASCADE,
    group_id       BIGINT NOT NULL,
    player_id      BIGINT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    old_rating     DOUBLE PRECISION NOT NULL,
    new_rating     DOUBLE PRECISION NOT NULL,
    old_rd         DOUBLE PRECISION NOT NULL,
    new_rd         DOUBLE PRECISION NOT NULL,
    delta          DOUBLE PRECISION NOT NULL,
    applied_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX rating_changes_group_player_applied
  ON rating_changes (group_id, player_id, applied_at DESC);
CREATE INDEX rating_changes_group_applied
  ON rating_changes (group_id, applied_at DESC);

ALTER TABLE bot_groups
  ADD COLUMN last_leaderboard_posted_for DATE;
