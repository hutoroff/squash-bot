CREATE TABLE audit_events (
    id            BIGSERIAL PRIMARY KEY,
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    event_type    TEXT        NOT NULL,
    visibility    TEXT        NOT NULL,
    actor_kind    TEXT        NOT NULL,
    actor_tg_id   BIGINT,
    actor_display TEXT,
    group_id      BIGINT      REFERENCES bot_groups(chat_id) ON DELETE SET NULL,
    subject_type  TEXT        NOT NULL,
    subject_id    TEXT        NOT NULL,
    description   TEXT        NOT NULL,
    metadata      JSONB
);

CREATE INDEX audit_events_group_id_idx    ON audit_events (group_id,    occurred_at DESC);
CREATE INDEX audit_events_actor_tg_id_idx ON audit_events (actor_tg_id, occurred_at DESC);
CREATE INDEX audit_events_event_type_idx  ON audit_events (event_type,  occurred_at DESC);
