package service

import (
	"context"
	"errors"

	"github.com/hutoroff/squash-bot/internal/featureflags"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
)

var ErrFeatureFlagForbidden = errors.New("feature flags require server-owner authority")
var ErrFeatureFlagScope = errors.New("invalid feature flag scope")

type FeatureFlagRepository interface {
	Get(context.Context, featureflags.Key, *int64) (featureflags.State, error)
	Set(context.Context, featureflags.Key, *int64, *bool) (*bool, error)
}
type flagOwnerRepository interface {
	IsServerOwner(context.Context, int64) (bool, error)
}

type FeatureFlagService struct {
	repo   FeatureFlagRepository
	owners flagOwnerRepository
	groups GroupRepository
	audit  *AuditService
}

func NewFeatureFlagService(repo FeatureFlagRepository, owners flagOwnerRepository, groups GroupRepository, audit *AuditService) *FeatureFlagService {
	return &FeatureFlagService{repo, owners, groups, audit}
}
func (s *FeatureFlagService) authorize(ctx context.Context, actor int64, group *int64) error {
	if actor <= 0 {
		return ErrFeatureFlagForbidden
	}
	owner, err := s.owners.IsServerOwner(ctx, actor)
	if err != nil {
		return err
	}
	if !owner {
		return ErrFeatureFlagForbidden
	}
	if group != nil {
		if *group == 0 {
			return ErrFeatureFlagScope
		}
		g, err := s.groups.GetByID(ctx, *group)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && g == nil) {
			return ErrFeatureFlagScope
		}
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *FeatureFlagService) List(ctx context.Context, actor int64, group *int64) ([]featureflags.State, error) {
	if err := s.authorize(ctx, actor, group); err != nil {
		return nil, err
	}
	out := make([]featureflags.State, 0)
	for _, d := range featureflags.Definitions() {
		state, err := s.repo.Get(ctx, d.Key, group)
		if err != nil {
			return nil, err
		}
		out = append(out, state)
	}
	return out, nil
}
func (s *FeatureFlagService) Set(ctx context.Context, actor int64, display string, key featureflags.Key, group *int64, enabled *bool) error {
	if err := s.authorize(ctx, actor, group); err != nil {
		return err
	}
	d, err := featureflags.Lookup(key)
	if err != nil {
		return err
	}
	if group != nil && !d.GroupScoped {
		return ErrFeatureFlagScope
	}
	old, err := s.repo.Set(ctx, key, group, enabled)
	if err != nil {
		return err
	}
	if (old == nil && enabled == nil) || (old != nil && enabled != nil && *old == *enabled) {
		return nil
	}
	s.audit.record(ctx, &models.AuditEvent{
		EventType:  models.AuditEventFeatureFlagChanged,
		Visibility: models.AuditVisibilityServerOwner,
		ActorKind:  models.AuditActorUser, ActorUserID: &actor, ActorDisplay: display,
		GroupID: group, SubjectType: "feature_flag", SubjectID: string(key),
		Description: "Feature flag override changed",
		Metadata:    map[string]any{"key": key, "old_override": old, "new_override": enabled, "group_id": group},
	})
	return nil
}
