package inbound

import (
	"context"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

type AuditUseCases interface {
	Query(ctx context.Context, f models.AuditQueryFilter) ([]*models.AuditEvent, error)
	RunRetention(ctx context.Context, retentionDays int)
	// Game events
	RecordGameCreated(ctx context.Context, gameID, groupID, actorTgID int64, actorDisplay, courts string, gameDate time.Time)
	RecordCourtsReserved(ctx context.Context, gameID, groupID, actorTgID int64, actorDisplay, courts string)
	// Participation events
	RecordPlayerJoined(ctx context.Context, gameID, groupID, actorTgID int64, actorDisplay string)
	RecordPlayerSkipped(ctx context.Context, gameID, groupID, actorTgID int64, actorDisplay string)
	RecordGuestAdded(ctx context.Context, gameID, groupID, actorTgID int64, actorDisplay string)
	RecordGuestRemoved(ctx context.Context, gameID, groupID, actorTgID int64, actorDisplay string)
	RecordPlayerKicked(ctx context.Context, gameID, groupID, actorTgID, targetTgID int64, actorDisplay string)
	RecordGuestKicked(ctx context.Context, gameID, groupID, actorTgID, guestID int64, actorDisplay string)
	// Venue events
	RecordVenueCreated(ctx context.Context, venueID, groupID, actorTgID int64, actorDisplay, venueName string)
	RecordVenueUpdated(ctx context.Context, venueID, groupID, actorTgID int64, actorDisplay, venueName string)
	RecordVenueDeleted(ctx context.Context, venueID, groupID, actorTgID int64, actorDisplay, venueName string)
	RecordCredentialAdded(ctx context.Context, credID, venueID, groupID, actorTgID int64, actorDisplay, login string)
	RecordCredentialRemoved(ctx context.Context, credID, venueID, groupID, actorTgID int64, actorDisplay, login string)
	// Group events
	RecordBotAddedToGroup(ctx context.Context, groupID int64, groupTitle string, actorTgID int64, actorDisplay string)
	RecordBotRemovedFromGroup(ctx context.Context, groupID int64, groupTitle string, actorTgID int64, actorDisplay string)
	RecordGroupSettings(ctx context.Context, groupID, actorTgID int64, actorDisplay, setting, from, to string)
	RecordGroupChangelogToggled(ctx context.Context, groupID, actorTgID int64, actorDisplay string, enabled bool)
	// Scheduler (system) events
	RecordCourtBooked(ctx context.Context, venueID, groupID int64, venueName, courtLabel string, gameDate time.Time)
	RecordCourtCanceled(ctx context.Context, venueID, groupID int64, venueName, courtLabel string, gameDate time.Time)
}
