-- Empty means unknown, not points. Never infer the meaning of historical scores.
ALTER TABLE game_results ADD COLUMN score_kind TEXT NOT NULL DEFAULT ''
    CHECK (score_kind IN ('', 'points', 'games'));

-- Existing history stays explicitly legacy; new applications record their inputs.
ALTER TABLE rating_changes ADD COLUMN policy_version TEXT NOT NULL DEFAULT 'glicko2-v1';
ALTER TABLE rating_changes ADD COLUMN evidence_weight DOUBLE PRECISION NOT NULL DEFAULT 1
    CHECK (evidence_weight >= 0.75 AND evidence_weight <= 1.25);
ALTER TABLE rating_changes ADD COLUMN score_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE rating_changes ADD COLUMN policy_reason TEXT NOT NULL DEFAULT 'legacy';
ALTER TABLE rating_changes ADD COLUMN score_aware_enabled BOOLEAN NOT NULL DEFAULT FALSE;
