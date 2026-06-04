ALTER TABLE user_preferences ALTER COLUMN dm_language SET DEFAULT '';
ALTER TABLE user_preferences ADD COLUMN results_opt_out BOOLEAN NOT NULL DEFAULT FALSE;
