-- Drop the FK constraint on audit_events.group_id so that deleting a bot_groups row
-- does not null-out historical audit records for that group.
-- group_id is now a plain BIGINT — a stored value, not a live reference.
ALTER TABLE audit_events DROP CONSTRAINT IF EXISTS audit_events_group_id_fkey;
