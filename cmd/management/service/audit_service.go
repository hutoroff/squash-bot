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

func userActor(userID int64, display string) (*int64, string) {
	return &userID, display
}

func groupIDPtr(groupID int64) *int64 {
	if groupID == 0 {
		return nil
	}
	return &groupID
}

// Game events

func (s *AuditService) RecordGameCreated(ctx context.Context, gameID, groupID, actorUserID int64, actorDisplay, courts string, gameDate time.Time, sport ...string) {
	uid, display := userActor(actorUserID, actorDisplay)
	sportName := "squash"
	if len(sport) > 0 && sport[0] != "" {
		sportName = sport[0]
	}
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventGameCreated,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGame,
		SubjectID:    fmt.Sprintf("%d", gameID),
		Description:  fmt.Sprintf("Game created for %s (courts: %s)", gameDate.Format("2006-01-02"), courts),
		Metadata:     map[string]any{"game_date": gameDate.Format("2006-01-02"), "courts": courts, "sport": sportName},
	})
}

func (s *AuditService) RecordCourtsReserved(ctx context.Context, gameID, groupID, actorUserID int64, actorDisplay, courts string) {
	uid, display := userActor(actorUserID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventCourtsReserved,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGame,
		SubjectID:    fmt.Sprintf("%d", gameID),
		Description:  fmt.Sprintf("Courts reserved: %s", courts),
		Metadata:     map[string]any{"courts": courts},
	})
}

// Participation events

func (s *AuditService) RecordPlayerJoined(ctx context.Context, gameID, groupID, actorUserID int64, actorDisplay string) {
	uid, display := userActor(actorUserID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventPlayerJoined,
		Visibility:   models.AuditVisibilityPlayer,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGame,
		SubjectID:    fmt.Sprintf("%d", gameID),
		Description:  fmt.Sprintf("%s joined the game", display),
	})
}

func (s *AuditService) RecordPlayerSkipped(ctx context.Context, gameID, groupID, actorUserID int64, actorDisplay string) {
	uid, display := userActor(actorUserID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventPlayerSkipped,
		Visibility:   models.AuditVisibilityPlayer,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGame,
		SubjectID:    fmt.Sprintf("%d", gameID),
		Description:  fmt.Sprintf("%s skipped the game", display),
	})
}

func (s *AuditService) RecordGuestAdded(ctx context.Context, gameID, groupID, actorUserID int64, actorDisplay string) {
	uid, display := userActor(actorUserID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventGuestAdded,
		Visibility:   models.AuditVisibilityPlayer,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGame,
		SubjectID:    fmt.Sprintf("%d", gameID),
		Description:  fmt.Sprintf("%s added a +1", display),
	})
}

func (s *AuditService) RecordGuestRemoved(ctx context.Context, gameID, groupID, actorUserID int64, actorDisplay string) {
	uid, display := userActor(actorUserID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventGuestRemoved,
		Visibility:   models.AuditVisibilityPlayer,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGame,
		SubjectID:    fmt.Sprintf("%d", gameID),
		Description:  fmt.Sprintf("%s removed their +1", display),
	})
}

func (s *AuditService) RecordPlayerKicked(ctx context.Context, gameID, groupID, actorUserID, targetPlayerID int64, actorDisplay string) {
	uid, display := userActor(actorUserID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventPlayerKicked,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGame,
		SubjectID:    fmt.Sprintf("%d", gameID),
		Description:  fmt.Sprintf("%s kicked player (id:%d) from game", display, targetPlayerID),
		Metadata:     map[string]any{"target_player_id": targetPlayerID},
	})
}

func (s *AuditService) RecordGuestKicked(ctx context.Context, gameID, groupID, actorUserID, guestID int64, actorDisplay string) {
	uid, display := userActor(actorUserID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventGuestKicked,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGame,
		SubjectID:    fmt.Sprintf("%d", gameID),
		Description:  fmt.Sprintf("%s kicked guest (id:%d) from game", display, guestID),
		Metadata:     map[string]any{"guest_id": guestID},
	})
}

// Venue events

func (s *AuditService) RecordVenueCreated(ctx context.Context, venueID, groupID, actorUserID int64, actorDisplay, venueName string) {
	uid, display := userActor(actorUserID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventVenueCreated,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectVenue,
		SubjectID:    fmt.Sprintf("%d", venueID),
		Description:  fmt.Sprintf("Venue %q created", venueName),
		Metadata:     map[string]any{"name": venueName},
	})
}

func (s *AuditService) RecordVenueUpdated(ctx context.Context, venueID, groupID, actorUserID int64, actorDisplay, venueName string) {
	uid, display := userActor(actorUserID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventVenueUpdated,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectVenue,
		SubjectID:    fmt.Sprintf("%d", venueID),
		Description:  fmt.Sprintf("Venue %q updated", venueName),
		Metadata:     map[string]any{"name": venueName},
	})
}

func (s *AuditService) RecordVenueDeleted(ctx context.Context, venueID, groupID, actorUserID int64, actorDisplay, venueName string) {
	uid, display := userActor(actorUserID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventVenueDeleted,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectVenue,
		SubjectID:    fmt.Sprintf("%d", venueID),
		Description:  fmt.Sprintf("Venue %q deleted", venueName),
		Metadata:     map[string]any{"name": venueName},
	})
}

// Credential events

func (s *AuditService) RecordCredentialAdded(ctx context.Context, credID, venueID, groupID, actorUserID int64, actorDisplay, login string) {
	uid, display := userActor(actorUserID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventCredentialAdded,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectCredential,
		SubjectID:    fmt.Sprintf("%d", credID),
		Description:  fmt.Sprintf("Credential %q added to venue %d", login, venueID),
		Metadata:     map[string]any{"login": login, "venue_id": venueID},
	})
}

func (s *AuditService) RecordCredentialRemoved(ctx context.Context, credID, venueID, groupID, actorUserID int64, actorDisplay, login string) {
	uid, display := userActor(actorUserID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventCredentialRemoved,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectCredential,
		SubjectID:    fmt.Sprintf("%d", credID),
		Description:  fmt.Sprintf("Credential %q removed from venue %d", login, venueID),
		Metadata:     map[string]any{"login": login, "venue_id": venueID},
	})
}

// Group events

func (s *AuditService) RecordBotAddedToGroup(ctx context.Context, groupID int64, groupTitle string, actorUserID int64, actorDisplay string) {
	uid, display := userActor(actorUserID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventBotAddedToGroup,
		Visibility:   models.AuditVisibilityServerOwner,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGroup,
		SubjectID:    fmt.Sprintf("%d", groupID),
		Description:  fmt.Sprintf("Bot added to group %q", groupTitle),
		Metadata:     map[string]any{"title": groupTitle},
	})
}

func (s *AuditService) RecordBotRemovedFromGroup(ctx context.Context, groupID int64, groupTitle string, actorUserID int64, actorDisplay string) {
	uid, display := userActor(actorUserID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventBotRemovedFromGroup,
		Visibility:   models.AuditVisibilityServerOwner,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGroup,
		SubjectID:    fmt.Sprintf("%d", groupID),
		Description:  fmt.Sprintf("Bot removed from group %q", groupTitle),
		Metadata:     map[string]any{"title": groupTitle},
	})
}

func (s *AuditService) RecordGroupSettings(ctx context.Context, groupID, actorUserID int64, actorDisplay, setting, from, to string) {
	uid, display := userActor(actorUserID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventGroupSettings,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
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
func (s *AuditService) RecordGroupChangelogToggled(ctx context.Context, groupID, actorUserID int64, actorDisplay string, enabled bool) {
	uid, display := userActor(actorUserID, actorDisplay)
	newVal := "false"
	if enabled {
		newVal = "true"
	}
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventGroupChangelogToggled,
		Visibility:   models.AuditVisibilityServerOwner,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGroup,
		SubjectID:    fmt.Sprintf("%d", groupID),
		Description:  fmt.Sprintf("Changelog announcements set to %s for group %d", newVal, groupID),
		Metadata:     map[string]any{"enabled": enabled},
	})
}

// RecordGroupLeaderboardNotificationsToggled records when a group admin enables or disables post-game leaderboard notifications.
// Visibility is server_owner — group admins cannot inspect this in the audit log.
func (s *AuditService) RecordGroupLeaderboardNotificationsToggled(ctx context.Context, groupID, actorUserID int64, actorDisplay string, enabled bool) {
	uid, display := userActor(actorUserID, actorDisplay)
	newVal := "false"
	if enabled {
		newVal = "true"
	}
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventGroupLeaderboardNotificationsToggled,
		Visibility:   models.AuditVisibilityServerOwner,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGroup,
		SubjectID:    fmt.Sprintf("%d", groupID),
		Description:  fmt.Sprintf("Leaderboard notifications set to %s for group %d", newVal, groupID),
		Metadata:     map[string]any{"enabled": enabled},
	})
}

// RecordGroupAutoBookingAllowedToggled records when a server owner enables or disables auto-booking for a group.
func (s *AuditService) RecordGroupAutoBookingAllowedToggled(ctx context.Context, groupID, actorUserID int64, actorDisplay string, enabled bool, cascadedVenueIDs []int64) {
	uid, display := userActor(actorUserID, actorDisplay)
	newVal := "false"
	if enabled {
		newVal = "true"
	}
	meta := map[string]any{"enabled": enabled}
	if len(cascadedVenueIDs) > 0 {
		meta["cascaded_venue_ids"] = cascadedVenueIDs
	}
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventGroupAutoBookingAllowedToggled,
		Visibility:   models.AuditVisibilityServerOwner,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGroup,
		SubjectID:    fmt.Sprintf("%d", groupID),
		Description:  fmt.Sprintf("Auto-booking allowed set to %s for group %d", newVal, groupID),
		Metadata:     meta,
	})
}

// RecordGamePublished records that a game was published (announcement sent to group).
// When actorUserID == 0, the action is treated as a system event (e.g. BookingReminderJob).
func (s *AuditService) RecordGamePublished(ctx context.Context, gameID, groupID, actorUserID int64, actorDisplay string) {
	evt := &models.AuditEvent{
		EventType:   models.AuditEventGamePublished,
		Visibility:  models.AuditVisibilityGroupAdmin,
		GroupID:     groupIDPtr(groupID),
		SubjectType: models.AuditSubjectGame,
		SubjectID:   fmt.Sprintf("%d", gameID),
		Description: fmt.Sprintf("Game %d published", gameID),
	}
	if actorUserID != 0 {
		evt.ActorKind = models.AuditActorUser
		evt.ActorUserID = &actorUserID
		evt.ActorDisplay = actorDisplay
	} else {
		evt.ActorKind = models.AuditActorSystem
	}
	s.record(ctx, evt)
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

// RecordCourtsAutoBooked records that courts were auto-booked on-demand for an existing game.
func (s *AuditService) RecordCourtsAutoBooked(ctx context.Context, chatID, actorUserID int64, actorDisplay string, gameID int64, venueName, gameDate string, bookedCount, requested int, courtLabels []string) {
	uid, display := userActor(actorUserID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventCourtsAutoBooked,
		Visibility:   models.AuditVisibilityGroupAdmin,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(chatID),
		SubjectType:  models.AuditSubjectGame,
		SubjectID:    fmt.Sprintf("%d", gameID),
		Description:  fmt.Sprintf("Auto-booked %d of %d courts at %q for %s", bookedCount, requested, venueName, gameDate),
		Metadata: map[string]any{
			"requested":    requested,
			"booked_count": bookedCount,
			"court_labels": courtLabels,
			"game_date":    gameDate,
		},
	})
}

// Game result events

func (s *AuditService) RecordGameResultSubmitted(ctx context.Context, resultID, groupID, actorUserID int64, actorDisplay string) {
	uid, display := userActor(actorUserID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventGameResultSubmitted,
		Visibility:   models.AuditVisibilityPlayer,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGameResult,
		SubjectID:    fmt.Sprintf("%d", resultID),
		Description:  fmt.Sprintf("%s submitted game result %d", display, resultID),
	})
}

func (s *AuditService) RecordGameResultApproved(ctx context.Context, resultID, groupID, actorUserID int64, actorDisplay string) {
	uid, display := userActor(actorUserID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventGameResultApproved,
		Visibility:   models.AuditVisibilityPlayer,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGameResult,
		SubjectID:    fmt.Sprintf("%d", resultID),
		Description:  fmt.Sprintf("%s approved game result %d", display, resultID),
	})
}

func (s *AuditService) RecordGameResultRejected(ctx context.Context, resultID, groupID, actorUserID int64, actorDisplay string) {
	uid, display := userActor(actorUserID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventGameResultRejected,
		Visibility:   models.AuditVisibilityPlayer,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGameResult,
		SubjectID:    fmt.Sprintf("%d", resultID),
		Description:  fmt.Sprintf("%s rejected game result %d", display, resultID),
	})
}

func (s *AuditService) RecordGameResultCanceled(ctx context.Context, resultID, groupID, actorUserID int64, actorDisplay string) {
	uid, display := userActor(actorUserID, actorDisplay)
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventGameResultCanceled,
		Visibility:   models.AuditVisibilityPlayer,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  uid,
		ActorDisplay: display,
		GroupID:      groupIDPtr(groupID),
		SubjectType:  models.AuditSubjectGameResult,
		SubjectID:    fmt.Sprintf("%d", resultID),
		Description:  fmt.Sprintf("%s canceled game result %d", display, resultID),
	})
}

func (s *AuditService) RecordGameResultAutoApproved(ctx context.Context, resultID, groupID int64) {
	s.record(ctx, &models.AuditEvent{
		EventType:   models.AuditEventGameResultAutoApproved,
		Visibility:  models.AuditVisibilityPlayer,
		ActorKind:   models.AuditActorSystem,
		GroupID:     groupIDPtr(groupID),
		SubjectType: models.AuditSubjectGameResult,
		SubjectID:   fmt.Sprintf("%d", resultID),
		Description: fmt.Sprintf("Game result %d auto-approved after 48h", resultID),
	})
}

func (s *AuditService) RecordRatingUpdated(ctx context.Context, resultID, groupID, authorID int64, authorDelta float64, opponentID int64, opponentDelta float64) {
	s.record(ctx, &models.AuditEvent{
		EventType:   models.AuditEventGameRatingUpdated,
		Visibility:  models.AuditVisibilityPlayer,
		ActorKind:   models.AuditActorSystem,
		GroupID:     groupIDPtr(groupID),
		SubjectType: models.AuditSubjectGameResult,
		SubjectID:   fmt.Sprintf("%d", resultID),
		Description: fmt.Sprintf("Ratings updated for game result %d", resultID),
		Metadata: map[string]any{
			"author_id": authorID, "author_delta": authorDelta,
			"opponent_id": opponentID, "opponent_delta": opponentDelta,
		},
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

// User events

// RecordUserRoleChanged records a server-owner role grant or revocation.
// Visibility is server_owner — only server owners may view this in the audit log.
func (s *AuditService) RecordUserRoleChanged(ctx context.Context, actorUserID int64, actorDisplay string, targetUserID int64, targetDisplay string, enabled bool) {
	action := "revoked"
	if enabled {
		action = "granted"
	}
	s.record(ctx, &models.AuditEvent{
		EventType:    models.AuditEventUserRoleChanged,
		Visibility:   models.AuditVisibilityServerOwner,
		ActorKind:    models.AuditActorUser,
		ActorUserID:  &actorUserID,
		ActorDisplay: actorDisplay,
		SubjectType:  models.AuditSubjectUser,
		SubjectID:    fmt.Sprintf("%d", targetUserID),
		Description:  fmt.Sprintf("Server-owner role %s for %s by %s", action, targetDisplay, actorDisplay),
		Metadata:     map[string]any{"enabled": enabled, "target_user_id": targetUserID, "target_display": targetDisplay},
	})
}
