CREATE TABLE game_results (
    id                  BIGSERIAL PRIMARY KEY,
    game_id             BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    group_id            BIGINT NOT NULL,
    author_id           BIGINT NOT NULL REFERENCES players(id),
    opponent_id         BIGINT NOT NULL REFERENCES players(id),
    winner_id           BIGINT REFERENCES players(id),
    score               TEXT NOT NULL DEFAULT '',
    status              TEXT NOT NULL,
    submitted_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at          TIMESTAMPTZ,
    approval_chat_id    BIGINT,
    approval_message_id INT,
    CHECK (author_id <> opponent_id),
    CHECK (winner_id IS NULL OR winner_id IN (author_id, opponent_id)),
    CHECK (status IN ('pending','approved','auto_approved','rejected','canceled'))
);

CREATE INDEX game_results_game ON game_results (game_id);
CREATE INDEX game_results_group_status_decided
  ON game_results (group_id, status, decided_at DESC);
CREATE INDEX game_results_pending_submitted
  ON game_results (status, submitted_at)
  WHERE status = 'pending';
