package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AuditEventRepo struct {
	pool *pgxpool.Pool
}

func NewAuditEventRepo(pool *pgxpool.Pool) *AuditEventRepo {
	return &AuditEventRepo{pool: pool}
}

func (r *AuditEventRepo) Insert(ctx context.Context, evt *models.AuditEvent) error {
	var metaJSON []byte
	if evt.Metadata != nil {
		var err error
		metaJSON, err = json.Marshal(evt.Metadata)
		if err != nil {
			return fmt.Errorf("marshal metadata: %w", err)
		}
	}

	const q = `
		INSERT INTO audit_events
			(event_type, visibility, actor_kind, actor_tg_id, actor_display,
			 group_id, subject_type, subject_id, description, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, occurred_at`

	return r.pool.QueryRow(ctx, q,
		evt.EventType, evt.Visibility, evt.ActorKind,
		evt.ActorTgID, evt.ActorDisplay,
		evt.GroupID, evt.SubjectType, evt.SubjectID,
		evt.Description, metaJSON,
	).Scan(&evt.ID, &evt.OccurredAt)
}

func (r *AuditEventRepo) Query(ctx context.Context, f models.AuditQueryFilter) ([]*models.AuditEvent, error) {
	conds := []string{}
	args := []any{}
	n := 1

	add := func(cond string, val any) {
		conds = append(conds, fmt.Sprintf(cond, n))
		args = append(args, val)
		n++
	}

	if f.GroupID != nil {
		add("group_id = $%d", *f.GroupID)
	}
	if f.ActorTgID != nil {
		add("actor_tg_id = $%d", *f.ActorTgID)
	}
	if f.EventType != "" {
		add("event_type = $%d", f.EventType)
	}
	if f.From != nil {
		add("occurred_at >= $%d", *f.From)
	}
	if f.To != nil {
		add("occurred_at < $%d", *f.To)
	}
	if len(f.Visibilities) > 0 {
		placeholders := make([]string, len(f.Visibilities))
		for i, v := range f.Visibilities {
			placeholders[i] = fmt.Sprintf("$%d", n)
			args = append(args, v)
			n++
		}
		conds = append(conds, fmt.Sprintf("visibility IN (%s)", strings.Join(placeholders, ",")))
	}
	if f.BeforeID != nil {
		add("id < $%d", *f.BeforeID)
	}

	where := "TRUE"
	if len(conds) > 0 {
		where = strings.Join(conds, " AND ")
	}

	limit := 50
	if f.Limit >= 1 && f.Limit <= 200 {
		limit = f.Limit
	}

	q := fmt.Sprintf(`
		SELECT id, occurred_at, event_type, visibility, actor_kind,
		       actor_tg_id, actor_display, group_id, subject_type,
		       subject_id, description, metadata
		FROM audit_events
		WHERE %s
		ORDER BY id DESC
		LIMIT %d`, where, limit)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*models.AuditEvent
	for rows.Next() {
		evt, err := scanAuditEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, evt)
	}
	return events, rows.Err()
}

func (r *AuditEventRepo) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	const q = `DELETE FROM audit_events WHERE occurred_at < $1`
	tag, err := r.pool.Exec(ctx, q, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanAuditEvent(row scannable) (*models.AuditEvent, error) {
	var evt models.AuditEvent
	var metaRaw []byte
	if err := row.Scan(
		&evt.ID, &evt.OccurredAt, &evt.EventType, &evt.Visibility, &evt.ActorKind,
		&evt.ActorTgID, &evt.ActorDisplay, &evt.GroupID, &evt.SubjectType,
		&evt.SubjectID, &evt.Description, &metaRaw,
	); err != nil {
		return nil, err
	}
	if metaRaw != nil {
		if err := json.Unmarshal(metaRaw, &evt.Metadata); err != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", err)
		}
	}
	return &evt, nil
}
