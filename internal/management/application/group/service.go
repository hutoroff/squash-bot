package group

import (
	"context"

	"github.com/hutoroff/squash-bot/internal/management/application/ports/outbound"
	"github.com/hutoroff/squash-bot/internal/models"
)

// Service is a thin pass-through over GroupRepository so the HTTP handler
// does not depend directly on the outbound adapter type.
type Service struct {
	repo outbound.GroupRepository
}

func NewService(repo outbound.GroupRepository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Upsert(ctx context.Context, chatID int64, title string, botIsAdmin bool) error {
	return s.repo.Upsert(ctx, chatID, title, botIsAdmin)
}

func (s *Service) SetLanguage(ctx context.Context, chatID int64, language string) error {
	return s.repo.SetLanguage(ctx, chatID, language)
}

func (s *Service) SetTimezone(ctx context.Context, chatID int64, timezone string) error {
	return s.repo.SetTimezone(ctx, chatID, timezone)
}

func (s *Service) SetChangelogEnabled(ctx context.Context, chatID int64, enabled bool) error {
	return s.repo.SetChangelogEnabled(ctx, chatID, enabled)
}

func (s *Service) Remove(ctx context.Context, chatID int64) error {
	return s.repo.Remove(ctx, chatID)
}

func (s *Service) Exists(ctx context.Context, chatID int64) (bool, error) {
	return s.repo.Exists(ctx, chatID)
}

func (s *Service) GetByID(ctx context.Context, chatID int64) (*models.Group, error) {
	return s.repo.GetByID(ctx, chatID)
}

func (s *Service) GetAll(ctx context.Context) ([]models.Group, error) {
	return s.repo.GetAll(ctx)
}
