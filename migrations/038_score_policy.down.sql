ALTER TABLE rating_changes DROP COLUMN score_aware_enabled, DROP COLUMN policy_reason, DROP COLUMN score_kind, DROP COLUMN evidence_weight, DROP COLUMN policy_version;
ALTER TABLE game_results DROP COLUMN score_kind;
