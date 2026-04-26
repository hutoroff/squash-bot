package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

// AuditService records user and system actions to the audit log.
// Record* methods are best-effort: errors are logged but never returned to callers.
type AuditService struct {
	repo   AuditEventRepository
	logger *slog.Logger
}

func NewAuditService(repo AuditEventRepository, logger *slog.Logger) *AuditService {
	return &AuditService{repo: repo, logger: logger}
}

func (s *AuditService) record(ctx context.Context, evt *models.AuditEvent) {
	if err := s.repo.Insert(ctx, evt); err != nil {
		s.logger.Error("audit: insert failed", "error", err, "event_type", evt.EventType)
	}
}

// Query returns audit events matching the given filter.
func (s *AuditService) Query(ctx context.Context, f models.AuditQueryFilter) ([]*models.AuditEvent, error) {
	return s.repo.Query(ctx, f)
}

// RunRetention deletes audit events older than retentionDays. Errors are logged, not returned.
func (s *AuditService) RunRetention(ctx context.Context, retentionDays int) {
	if retentionDays <= 0 {
		s.logger.Error("audit: invalid retention_days, skipping retention run", "days", retentionDays)
		return
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	deleted, err := s.repo.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		s.logger.Error("audit: retention delete failed", "error", err)
		return
	}
	if deleted > 0 {
		s.logger.Info("audit: retention deleted old events", "count", deleted, "cutoff", cutoff.Format("2006-01-02"))
	}
}

func userActor(tgID int64, display string) (*int64, string) {
	return &tgID, display
}

func groupIDPtr(groupID int64) *int64 {
	if groupID == 0 {
		return nil
	}
	return &groupID
}

// Game events

func (s *AuditService) RecordGameCreated(ctx context.Context, gameID, groupID, actorTgID int64, actorDisplay, courts string, gameDate time.Time) {
	tgID, display := userActor(actorTgID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventGameCreated,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorTgID:    tgID,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGame,
		SubjectID:    fmt.Sprintf("%d", gameID),
		Description:  fmt.Sprintf("Game created for %s (courts: %s)", gameDate.Format("2006-01-02"), courts),
		Metadata:     map[string]any{"game_date": gameDate.Format("2006-01-02"), "courts": courts},
	})
}

func (s *AuditService) RecordCourtsReserved(ctx context.Context, gameID, groupID, actorTgID int64, actorDisplay, courts string) {
	tgID, display := userActor(actorTgID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventCourtsReserved,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorTgID:    tgID,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGame,
		SubjectID:    fmt.Sprintf("%d", gameID),
		Description:  fmt.Sprintf("Courts reserved: %s", courts),
		Metadata:     map[string]any{"courts": courts},
	})
}

// Participation events

func (s *AuditService) RecordPlayerJoined(ctx context.Context, gameID, groupID, actorTgID int64, actorDisplay string) {
	tgID, display := userActor(actorTgID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventPlayerJoined,
		Visibility:   models.AuditVisibilityPlayer,
		ActorKind:    models.AuditActorUser,
		ActorTgID:    tgID,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGame,
		SubjectID:    fmt.Sprintf("%d", gameID),
		Description:  fmt.Sprintf("%s joined the game", display),
	})
}

func (s *AuditService) RecordPlayerSkipped(ctx context.Context, gameID, groupID, actorTgID int64, actorDisplay string) {
	tgID, display := userActor(actorTgID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventPlayerSkipped,
		Visibility:   models.AuditVisibilityPlayer,
		ActorKind:    models.AuditActorUser,
		ActorTgID:    tgID,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGame,
		SubjectID:    fmt.Sprintf("%d", gameID),
		Description:  fmt.Sprintf("%s skipped the game", display),
	})
}

func (s *AuditService) RecordGuestAdded(ctx context.Context, gameID, groupID, actorTgID int64, actorDisplay string) {
	tgID, display := userActor(actorTgID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventGuestAdded,
		Visibility:   models.AuditVisibilityPlayer,
		ActorKind:    models.AuditActorUser,
		ActorTgID:    tgID,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGame,
		SubjectID:    fmt.Sprintf("%d", gameID),
		Description:  fmt.Sprintf("%s added a +1", display),
	})
}

func (s *AuditService) RecordGuestRemoved(ctx context.Context, gameID, groupID, actorTgID int64, actorDisplay string) {
	tgID, display := userActor(actorTgID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventGuestRemoved,
		Visibility:   models.AuditVisibilityPlayer,
		ActorKind:    models.AuditActorUser,
		ActorTgID:    tgID,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGame,
		SubjectID:    fmt.Sprintf("%d", gameID),
		Description:  fmt.Sprintf("%s removed their +1", display),
	})
}

func (s *AuditService) RecordPlayerKicked(ctx context.Context, gameID, groupID, actorTgID, targetTgID int64, actorDisplay string) {
	tgID, display := userActor(actorTgID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventPlayerKicked,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorTgID:    tgID,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGame,
		SubjectID:    fmt.Sprintf("%d", gameID),
		Description:  fmt.Sprintf("%s kicked player (tg:%d) from game", display, targetTgID),
		Metadata:     map[string]any{"target_tg_id": targetTgID},
	})
}

func (s *AuditService) RecordGuestKicked(ctx context.Context, gameID, groupID, actorTgID, guestID int64, actorDisplay string) {
	tgID, display := userActor(actorTgID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventGuestKicked,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorTgID:    tgID,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGame,
		SubjectID:    fmt.Sprintf("%d", gameID),
		Description:  fmt.Sprintf("%s kicked guest (id:%d) from game", display, guestID),
		Metadata:     map[string]any{"guest_id": guestID},
	})
}

// Venue events

func (s *AuditService) RecordVenueCreated(ctx context.Context, venueID, groupID, actorTgID int64, actorDisplay, venueName string) {
	tgID, display := userActor(actorTgID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventVenueCreated,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorTgID:    tgID,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectVenue,
		SubjectID:    fmt.Sprintf("%d", venueID),
		Description:  fmt.Sprintf("Venue %q created", venueName),
		Metadata:     map[string]any{"name": venueName},
	})
}

func (s *AuditService) RecordVenueUpdated(ctx context.Context, venueID, groupID, actorTgID int64, actorDisplay, venueName string) {
	tgID, display := userActor(actorTgID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventVenueUpdated,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorTgID:    tgID,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectVenue,
		SubjectID:    fmt.Sprintf("%d", venueID),
		Description:  fmt.Sprintf("Venue %q updated", venueName),
		Metadata:     map[string]any{"name": venueName},
	})
}

func (s *AuditService) RecordVenueDeleted(ctx context.Context, venueID, groupID, actorTgID int64, actorDisplay, venueName string) {
	tgID, display := userActor(actorTgID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventVenueDeleted,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorTgID:    tgID,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectVenue,
		SubjectID:    fmt.Sprintf("%d", venueID),
		Description:  fmt.Sprintf("Venue %q deleted", venueName),
		Metadata:     map[string]any{"name": venueName},
	})
}

// Credential events

func (s *AuditService) RecordCredentialAdded(ctx context.Context, credID, venueID, groupID, actorTgID int64, actorDisplay, login string) {
	tgID, display := userActor(actorTgID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventCredentialAdded,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorTgID:    tgID,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectCredential,
		SubjectID:    fmt.Sprintf("%d", credID),
		Description:  fmt.Sprintf("Credential %q added to venue %d", login, venueID),
		Metadata:     map[string]any{"login": login, "venue_id": venueID},
	})
}

func (s *AuditService) RecordCredentialRemoved(ctx context.Context, credID, venueID, groupID, actorTgID int64, actorDisplay, login string) {
	tgID, display := userActor(actorTgID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventCredentialRemoved,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorTgID:    tgID,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectCredential,
		SubjectID:    fmt.Sprintf("%d", credID),
		Description:  fmt.Sprintf("Credential %q removed from venue %d", login, venueID),
		Metadata:     map[string]any{"login": login, "venue_id": venueID},
	})
}

// Group events

func (s *AuditService) RecordBotAddedToGroup(ctx context.Context, groupID int64, groupTitle string, actorTgID int64, actorDisplay string) {
	tgID, display := userActor(actorTgID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventBotAddedToGroup,
		Visibility:   models.AuditVisibilityServerOwner,
		ActorKind:    models.AuditActorUser,
		ActorTgID:    tgID,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGroup,
		SubjectID:    fmt.Sprintf("%d", groupID),
		Description:  fmt.Sprintf("Bot added to group %q", groupTitle),
		Metadata:     map[string]any{"title": groupTitle},
	})
}

func (s *AuditService) RecordBotRemovedFromGroup(ctx context.Context, groupID int64, groupTitle string, actorTgID int64, actorDisplay string) {
	tgID, display := userActor(actorTgID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventBotRemovedFromGroup,
		Visibility:   models.AuditVisibilityServerOwner,
		ActorKind:    models.AuditActorUser,
		ActorTgID:    tgID,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGroup,
		SubjectID:    fmt.Sprintf("%d", groupID),
		Description:  fmt.Sprintf("Bot removed from group %q", groupTitle),
		Metadata:     map[string]any{"title": groupTitle},
	})
}

func (s *AuditService) RecordGroupSettings(ctx context.Context, groupID, actorTgID int64, actorDisplay, setting, from, to string) {
	tgID, display := userActor(actorTgID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventGroupSettings,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorTgID:    tgID,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGroup,
		SubjectID:    fmt.Sprintf("%d", groupID),
		Description:  fmt.Sprintf("Group setting %q changed from %q to %q", setting, from, to),
		Metadata:     map[string]any{"setting": setting, "from": from, "to": to},
	})
}

// RecordGroupChangelogToggled records when a group admin enables or disables changelog announcements.
// Visibility is server_owner — group admins cannot inspect this in the audit log.
func (s *AuditService) RecordGroupChangelogToggled(ctx context.Context, groupID, actorTgID int64, actorDisplay string, enabled bool) {
	tgID, display := userActor(actorTgID, actorDisplay)
	newVal := "false"
	if enabled {
		newVal = "true"
	}
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventGroupChangelogToggled,
		Visibility:   models.AuditVisibilityServerOwner,
		ActorKind:    models.AuditActorUser,
		ActorTgID:    tgID,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGroup,
		SubjectID:    fmt.Sprintf("%d", groupID),
		Description:  fmt.Sprintf("Changelog announcements set to %s for group %d", newVal, groupID),
		Metadata:     map[string]any{"enabled": enabled},
	})
}

// Scheduler (system) events

func (s *AuditService) RecordCourtBooked(ctx context.Context, venueID, groupID int64, venueName, courtLabel string, gameDate time.Time) {
	s.record(ctx, &models.AuditEvent{
		EventType:   models.AuditEventCourtBooked,
		Visibility:  models.AuditVisibilityGroupAdmin,
		ActorKind:   models.AuditActorSystem,
		GroupID:     groupIDPtr(groupID),
		SubjectType: models.AuditSubjectCourtSlot,
		SubjectID:   fmt.Sprintf("%d", venueID),
		Description: fmt.Sprintf("Court %s booked at %q for %s", courtLabel, venueName, gameDate.Format("2006-01-02")),
		Metadata:    map[string]any{"venue_id": venueID, "court_label": courtLabel, "game_date": gameDate.Format("2006-01-02")},
	})
}

func (s *AuditService) RecordCourtCanceled(ctx context.Context, venueID, groupID int64, venueName, courtLabel string, gameDate time.Time) {
	s.record(ctx, &models.AuditEvent{
		EventType:   models.AuditEventCourtCanceled,
		Visibility:  models.AuditVisibilityGroupAdmin,
		ActorKind:   models.AuditActorSystem,
		GroupID:     groupIDPtr(groupID),
		SubjectType: models.AuditSubjectCourtSlot,
		SubjectID:   fmt.Sprintf("%d", venueID),
		Description: fmt.Sprintf("Court %s canceled at %q for %s", courtLabel, venueName, gameDate.Format("2006-01-02")),
		Metadata:    map[string]any{"venue_id": venueID, "court_label": courtLabel, "game_date": gameDate.Format("2006-01-02")},
	})
}
