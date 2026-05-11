package outbound

import (
	"context"

	"github.com/hutoroff/squash-bot/internal/models"
)

type GroupRepository interface {
	Upsert(ctx context.Context, chatID int64, title string, botIsAdmin bool) error
	SetLanguage(ctx context.Context, chatID int64, language string) error
	SetTimezone(ctx context.Context, chatID int64, timezone string) error
	SetChangelogEnabled(ctx context.Context, chatID int64, enabled bool) error
	Remove(ctx context.Context, chatID int64) error
	Exists(ctx context.Context, chatID int64) (bool, error)
	GetByID(ctx context.Context, chatID int64) (*models.Group, error)
	GetAll(ctx context.Context) ([]models.Group, error)
}
