package models

import "time"

type AuditVisibility string
type AuditEventType string
type AuditActorKind string

const (
	// Visibility levels — who can view an event (hierarchy: ServerOwner ≥ GroupAdmin ≥ Player).
	AuditVisibilityPlayer      AuditVisibility = "player"       // actor + group admins + server owner
	AuditVisibilityGroupAdmin  AuditVisibility = "group_admin"  // group admins + server owner
	AuditVisibilityServerOwner AuditVisibility = "server_owner" // server owner only

	AuditActorUser   AuditActorKind = "user"
	AuditActorSystem AuditActorKind = "system"

	// Event types.
	AuditEventGameCreated                          AuditEventType = "game.created"
	AuditEventCourtsReserved                       AuditEventType = "game.courts_reserved"
	AuditEventPlayerJoined                         AuditEventType = "participation.joined"
	AuditEventPlayerSkipped                        AuditEventType = "participation.skipped"
	AuditEventGuestAdded                           AuditEventType = "participation.guest_added"
	AuditEventGuestRemoved                         AuditEventType = "participation.guest_removed"
	AuditEventPlayerKicked                         AuditEventType = "participation.player_kicked"
	AuditEventGuestKicked                          AuditEventType = "participation.guest_kicked"
	AuditEventCredentialAdded                      AuditEventType = "credential.added"
	AuditEventCredentialRemoved                    AuditEventType = "credential.removed"
	AuditEventVenueCreated                         AuditEventType = "venue.created"
	AuditEventVenueUpdated                         AuditEventType = "venue.updated"
	AuditEventVenueDeleted                         AuditEventType = "venue.deleted"
	AuditEventBotAddedToGroup                      AuditEventType = "group.bot_added"
	AuditEventBotRemovedFromGroup                  AuditEventType = "group.bot_removed"
	AuditEventGroupSettings                        AuditEventType = "group.settings_changed"
	AuditEventGroupChangelogToggled                AuditEventType = "group.changelog_toggled"
	AuditEventGroupLeaderboardNotificationsToggled AuditEventType = "group.leaderboard_notifications_toggled"
	AuditEventGroupAutoBookingAllowedToggled       AuditEventType = "group.auto_booking_allowed_toggled"
	AuditEventCourtBooked                          AuditEventType = "court.booked"
	AuditEventCourtCanceled                        AuditEventType = "court.canceled"
	AuditEventGamePublished                        AuditEventType = "game.published"
	AuditEventCourtsAutoBooked                     AuditEventType = "game.courts_auto_booked"

	// Game result events.
	AuditEventGameResultSubmitted    AuditEventType = "game.result_submitted"
	AuditEventGameResultApproved     AuditEventType = "game.result_approved"
	AuditEventGameResultRejected     AuditEventType = "game.result_rejected"
	AuditEventGameResultAutoApproved AuditEventType = "game.result_auto_approved"
	AuditEventGameResultCanceled     AuditEventType = "game.result_canceled"

	// Rating events.
	AuditEventGameRatingUpdated AuditEventType = "game.rating_updated"

	// User events.
	AuditEventUserRoleChanged AuditEventType = "user.role_changed"

	// Subject types.
	AuditSubjectGame       = "game"
	AuditSubjectVenue      = "venue"
	AuditSubjectCredential = "credential"
	AuditSubjectGroup      = "group"
	AuditSubjectCourtSlot  = "court_slot"
	AuditSubjectGameResult = "game_result"
	AuditSubjectUser       = "user"
)

// AuditQueryFilter defines constraints for listing audit events.
type AuditQueryFilter struct {
	GroupID      *int64
	ActorTgID    *int64
	ActorUserID  *int64
	EventType    string
	From         *time.Time
	To           *time.Time
	Visibilities []AuditVisibility
	// OwnTgID, when set, includes events with visibility='player' AND actor_tg_id=*OwnTgID.
	OwnTgID *int64
	// OwnUserID, when set, includes events with visibility='player' AND actor_user_id=*OwnUserID.
	OwnUserID *int64
	// AdminGroupIDs, when non-empty, includes events with visibility IN ('player','group_admin')
	// AND group_id IN AdminGroupIDs.
	AdminGroupIDs []int64
	// Limit is clamped to [1, 200]; defaults to 50.
	Limit    int
	BeforeID *int64
}

type AuditEvent struct {
	ID           int64           `json:"id"`
	OccurredAt   time.Time       `json:"occurred_at"`
	EventType    AuditEventType  `json:"event_type"`
	Visibility   AuditVisibility `json:"visibility"`
	ActorKind    AuditActorKind  `json:"actor_kind"`
	ActorTgID    *int64          `json:"actor_tg_id,omitempty"`
	ActorUserID  *int64          `json:"actor_user_id,omitempty"`
	ActorDisplay string          `json:"actor_display,omitempty"`
	GroupID      *int64          `json:"group_id,omitempty"`
	SubjectType  string          `json:"subject_type"`
	SubjectID    string          `json:"subject_id"`
	Description  string          `json:"description"`
	Metadata     map[string]any  `json:"metadata,omitempty"`
}
