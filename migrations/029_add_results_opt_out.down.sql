ALTER TABLE user_preferences DROP COLUMN IF EXISTS results_opt_out;
ALTER TABLE user_preferences ALTER COLUMN dm_language DROP DEFAULT;
