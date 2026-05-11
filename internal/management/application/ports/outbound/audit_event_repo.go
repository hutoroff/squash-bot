package outbound

import (
	"context"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

type AuditEventRepository interface {
	Insert(ctx context.Context, evt *models.AuditEvent) error
	Query(ctx context.Context, f models.AuditQueryFilter) ([]*models.AuditEvent, error)
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}
