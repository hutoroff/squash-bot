package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

// captureRepo is a stub AuditEventRepository that records Insert calls.
type captureRepo struct {
	inserted []*models.AuditEvent
}

func (r *captureRepo) Insert(_ context.Context, evt *models.AuditEvent) error {
	r.inserted = append(r.inserted, evt)
	return nil
}

func (r *captureRepo) Query(_ context.Context, _ models.AuditQueryFilter) ([]*models.AuditEvent, error) {
	return nil, nil
}

func (r *captureRepo) DeleteOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

func newCaptureAuditSvc() (*AuditService, *captureRepo) {
	repo := &captureRepo{}
	return NewAuditService(repo, testLogger), repo
}

// assertEventBase checks the core shape that all user-actor events must satisfy.
func assertEventBase(t *testing.T, evt *models.AuditEvent,
	wantType models.AuditEventType,
	wantVis models.AuditVisibility,
	wantSubjType string,
) {
	t.Helper()
	if evt.EventType != wantType {
		t.Errorf("EventType: got %q, want %q", evt.EventType, wantType)
	}
	if evt.Visibility != wantVis {
		t.Errorf("Visibility: got %q, want %q", evt.Visibility, wantVis)
	}
	if evt.SubjectType != wantSubjType {
		t.Errorf("SubjectType: got %q, want %q", evt.SubjectType, wantSubjType)
	}
}

func assertUserActor(t *testing.T, evt *models.AuditEvent, tgID int64, display string) {
	t.Helper()
	if evt.ActorKind != models.AuditActorUser {
		t.Errorf("ActorKind: got %q, want user", evt.ActorKind)
	}
	if evt.ActorTgID == nil || *evt.ActorTgID != tgID {
		t.Errorf("ActorTgID: got %v, want %d", evt.ActorTgID, tgID)
	}
	if evt.ActorDisplay != display {
		t.Errorf("ActorDisplay: got %q, want %q", evt.ActorDisplay, display)
	}
}

// ── groupIDPtr ────────────────────────────────────────────────────────────────

func TestGroupIDPtr_Zero(t *testing.T) {
	if groupIDPtr(0) != nil {
		t.Error("groupIDPtr(0) should be nil")
	}
}

func TestGroupIDPtr_NonZero(t *testing.T) {
	p := groupIDPtr(42)
	if p == nil || *p != 42 {
		t.Errorf("groupIDPtr(42): got %v, want ptr to 42", p)
	}
}

// ── participation events ──────────────────────────────────────────────────────

func TestRecordPlayerJoined(t *testing.T) {
	svc, repo := newCaptureAuditSvc()
	svc.RecordPlayerJoined(context.Background(), 10, 20, 99, "@alice")

	if len(repo.inserted) != 1 {
		t.Fatalf("want 1 event, got %d", len(repo.inserted))
	}
	evt := repo.inserted[0]
	assertEventBase(t, evt, models.AuditEventPlayerJoined, models.AuditVisibilityPlayer, models.AuditSubjectGame)
	assertUserActor(t, evt, 99, "@alice")
	if evt.SubjectID != "10" {
		t.Errorf("SubjectID: got %q, want %q", evt.SubjectID, "10")
	}
	if evt.GroupID == nil || *evt.GroupID != 20 {
		t.Errorf("GroupID: got %v, want 20", evt.GroupID)
	}
}

func TestRecordPlayerKicked(t *testing.T) {
	svc, repo := newCaptureAuditSvc()
	svc.RecordPlayerKicked(context.Background(), 10, 20, 99, 101, "@admin")

	if len(repo.inserted) != 1 {
		t.Fatalf("want 1 event, got %d", len(repo.inserted))
	}
	evt := repo.inserted[0]
	assertEventBase(t, evt, models.AuditEventPlayerKicked, models.AuditVisibilityGroupAdmin, models.AuditSubjectGame)
	assertUserActor(t, evt, 99, "@admin")
	if evt.SubjectID != "10" {
		t.Errorf("SubjectID: got %q, want %q", evt.SubjectID, "10")
	}
	if evt.GroupID == nil || *evt.GroupID != 20 {
		t.Errorf("GroupID: got %v, want 20", evt.GroupID)
	}
	if evt.Metadata["target_tg_id"] != int64(101) {
		t.Errorf("Metadata[target_tg_id]: got %v, want 101", evt.Metadata["target_tg_id"])
	}
}

func TestRecordGuestKicked(t *testing.T) {
	svc, repo := newCaptureAuditSvc()
	svc.RecordGuestKicked(context.Background(), 10, 20, 99, 7, "@admin")

	if len(repo.inserted) != 1 {
		t.Fatalf("want 1 event, got %d", len(repo.inserted))
	}
	evt := repo.inserted[0]
	assertEventBase(t, evt, models.AuditEventGuestKicked, models.AuditVisibilityGroupAdmin, models.AuditSubjectGame)
	assertUserActor(t, evt, 99, "@admin")
	if evt.Metadata["guest_id"] != int64(7) {
		t.Errorf("Metadata[guest_id]: got %v, want 7", evt.Metadata["guest_id"])
	}
}

func TestRecordPlayerKicked_NilGroupIDWhenZero(t *testing.T) {
	svc, repo := newCaptureAuditSvc()
	svc.RecordPlayerKicked(context.Background(), 10, 0, 99, 101, "@admin")

	evt := repo.inserted[0]
	if evt.GroupID != nil {
		t.Errorf("GroupID: expected nil when groupID=0, got %v", evt.GroupID)
	}
}

// ── group membership events ───────────────────────────────────────────────────

func TestRecordBotAddedToGroup(t *testing.T) {
	svc, repo := newCaptureAuditSvc()
	svc.RecordBotAddedToGroup(context.Background(), 300, "Squash Club", 99, "@admin")

	if len(repo.inserted) != 1 {
		t.Fatalf("want 1 event, got %d", len(repo.inserted))
	}
	evt := repo.inserted[0]
	assertEventBase(t, evt, models.AuditEventBotAddedToGroup, models.AuditVisibilityServerOwner, models.AuditSubjectGroup)
	assertUserActor(t, evt, 99, "@admin")
	if evt.SubjectID != "300" {
		t.Errorf("SubjectID: got %q, want %q", evt.SubjectID, "300")
	}
	if evt.GroupID == nil || *evt.GroupID != 300 {
		t.Errorf("GroupID: got %v, want 300", evt.GroupID)
	}
	if evt.Metadata["title"] != "Squash Club" {
		t.Errorf("Metadata[title]: got %v, want %q", evt.Metadata["title"], "Squash Club")
	}
}

func TestRecordBotRemovedFromGroup(t *testing.T) {
	svc, repo := newCaptureAuditSvc()
	svc.RecordBotRemovedFromGroup(context.Background(), 300, "Squash Club", 99, "@admin")

	if len(repo.inserted) != 1 {
		t.Fatalf("want 1 event, got %d", len(repo.inserted))
	}
	evt := repo.inserted[0]
	assertEventBase(t, evt, models.AuditEventBotRemovedFromGroup, models.AuditVisibilityServerOwner, models.AuditSubjectGroup)
	assertUserActor(t, evt, 99, "@admin")
	if evt.Metadata["title"] != "Squash Club" {
		t.Errorf("Metadata[title]: got %v, want %q", evt.Metadata["title"], "Squash Club")
	}
}

// ── group changelog toggle ────────────────────────────────────────────────────

func TestRecordGroupChangelogToggled_Enabled(t *testing.T) {
	svc, repo := newCaptureAuditSvc()
	svc.RecordGroupChangelogToggled(context.Background(), 300, 99, "@admin", true)

	if len(repo.inserted) != 1 {
		t.Fatalf("want 1 event, got %d", len(repo.inserted))
	}
	evt := repo.inserted[0]
	assertEventBase(t, evt, models.AuditEventGroupChangelogToggled, models.AuditVisibilityServerOwner, models.AuditSubjectGroup)
	assertUserActor(t, evt, 99, "@admin")
	if evt.GroupID == nil || *evt.GroupID != 300 {
		t.Errorf("GroupID: got %v, want 300", evt.GroupID)
	}
	if evt.Metadata["enabled"] != true {
		t.Errorf("Metadata[enabled]: got %v, want true", evt.Metadata["enabled"])
	}
}

func TestRecordGroupChangelogToggled_Disabled(t *testing.T) {
	svc, repo := newCaptureAuditSvc()
	svc.RecordGroupChangelogToggled(context.Background(), 400, 77, "@user", false)

	evt := repo.inserted[0]
	assertEventBase(t, evt, models.AuditEventGroupChangelogToggled, models.AuditVisibilityServerOwner, models.AuditSubjectGroup)
	if evt.Metadata["enabled"] != false {
		t.Errorf("Metadata[enabled]: got %v, want false", evt.Metadata["enabled"])
	}
}

// ── system actor events ───────────────────────────────────────────────────────

func TestRecordCourtBooked_SystemActor(t *testing.T) {
	svc, repo := newCaptureAuditSvc()
	gameDate := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	svc.RecordCourtBooked(context.Background(), 5, 20, "Club X", "Court 1", gameDate)

	if len(repo.inserted) != 1 {
		t.Fatalf("want 1 event, got %d", len(repo.inserted))
	}
	evt := repo.inserted[0]
	assertEventBase(t, evt, models.AuditEventCourtBooked, models.AuditVisibilityGroupAdmin, models.AuditSubjectCourtSlot)
	if evt.ActorKind != models.AuditActorSystem {
		t.Errorf("ActorKind: got %q, want system", evt.ActorKind)
	}
	if evt.ActorTgID != nil {
		t.Errorf("ActorTgID: expected nil for system actor, got %v", evt.ActorTgID)
	}
}

// ── description sanity checks ─────────────────────────────────────────────────

func TestRecordPlayerKicked_Description(t *testing.T) {
	svc, repo := newCaptureAuditSvc()
	svc.RecordPlayerKicked(context.Background(), 10, 20, 99, 101, "@admin")

	want := fmt.Sprintf("%s kicked player (tg:%d) from game", "@admin", 101)
	if repo.inserted[0].Description != want {
		t.Errorf("Description: got %q, want %q", repo.inserted[0].Description, want)
	}
}

func TestRecordGuestKicked_Description(t *testing.T) {
	svc, repo := newCaptureAuditSvc()
	svc.RecordGuestKicked(context.Background(), 10, 20, 99, 7, "@admin")

	want := fmt.Sprintf("%s kicked guest (id:%d) from game", "@admin", 7)
	if repo.inserted[0].Description != want {
		t.Errorf("Description: got %q, want %q", repo.inserted[0].Description, want)
	}
}

func TestRecordBotAddedToGroup_Description(t *testing.T) {
	svc, repo := newCaptureAuditSvc()
	svc.RecordBotAddedToGroup(context.Background(), 300, "Squash Club", 99, "@admin")

	want := `Bot added to group "Squash Club"`
	if repo.inserted[0].Description != want {
		t.Errorf("Description: got %q, want %q", repo.inserted[0].Description, want)
	}
}
