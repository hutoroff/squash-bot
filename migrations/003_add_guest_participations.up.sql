CREATE TABLE guest_participations (
    id                  BIGSERIAL PRIMARY KEY,
    game_id             BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    invited_by_player_id BIGINT NOT NULL REFERENCES players(id),
    created_at          TIMESTAMPTZ DEFAULT now()
);
