-- Restore the FK (only if all group_id values in audit_events still exist in bot_groups).
ALTER TABLE audit_events
    ADD CONSTRAINT audit_events_group_id_fkey
    FOREIGN KEY (group_id) REFERENCES bot_groups(chat_id) ON DELETE SET NULL;
