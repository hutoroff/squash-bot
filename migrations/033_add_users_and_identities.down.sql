CREATE INDEX audit_events_actor_tg_id_idx ON audit_events (actor_tg_id, occurred_at DESC);
DROP INDEX audit_events_actor_user_id_idx;
ALTER TABLE audit_events DROP COLUMN actor_user_id;

ALTER TABLE players DROP CONSTRAINT players_user_id_key;
ALTER TABLE players DROP CONSTRAINT players_user_id_fkey;
ALTER TABLE players DROP COLUMN user_id;

DROP TABLE user_identities;
DROP TABLE users;
