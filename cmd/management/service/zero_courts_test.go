package service

import (
	"context"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

// A game whose last court was removed has courts_count = 0. Scheduler jobs used to
// divide by it and panic; they must skip such games silently and mark them handled.

type notifiedGameRepoPC struct {
	stubGameRepoPC
	notifiedIDs []int64
}

func (r *notifiedGameRepoPC) MarkNotifiedDayBefore(_ context.Context, id int64) error {
	r.notifiedIDs = append(r.notifiedIDs, id)
	return nil
}

func zeroCourtGame() *models.Game {
	game := makeGame("", 0, time.Now().Add(24*time.Hour))
	game.ID = 40
	game.ChatID = 100
	return game
}

func TestCancellationReminder_ZeroCourts_SkipsAndMarksNotified(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	api := &captureSendAPI{}
	repo := &notifiedGameRepoPC{}
	job := makeJobForPC(api, repo, 1, 0, group, &spyNotifier{}, nil, nil)

	job.processCancellationReminder(context.Background(), zeroCourtGame())

	if len(api.msgs) != 0 {
		t.Errorf("expected no message, got %d", len(api.msgs))
	}
	if len(repo.notifiedIDs) != 1 || repo.notifiedIDs[0] != 40 {
		t.Errorf("expected game 40 marked notified, got %v", repo.notifiedIDs)
	}
}

func TestHalfwayCheck_ZeroCourts_SkipsAndMarksDone(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	api := &adminCaptureSendAPI{}
	repo := &stubGameRepoHCC{}
	canceler := &mockHalfwayCanceler{}
	job := makeHCCJob(api, repo, 1, 0, group, &spyNotifier{}, canceler)

	job.processHalfwayCheck(context.Background(), zeroCourtGame())

	if canceler.cancelCalls != 0 || len(api.msgs) != 0 {
		t.Errorf("expected no cancellation or message, got %d calls, %d msgs", canceler.cancelCalls, len(api.msgs))
	}
	if len(repo.markedIDs) != 1 || repo.markedIDs[0] != 40 {
		t.Errorf("expected game 40 marked done, got %v", repo.markedIDs)
	}
}

func TestFinalCheck_ZeroCourts_SkipsAndMarksDone(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	api := &adminCaptureSendAPI{}
	repo := &stubGameRepoFCC{}
	canceler := &mockCanceler{}
	job := makeFCCJob(api, repo, 1, 0, group, &spyNotifier{}, canceler)

	job.processFinalCheck(context.Background(), zeroCourtGame())

	if canceler.calls != 0 || len(api.msgs) != 0 {
		t.Errorf("expected no cancellation or message, got %d calls, %d msgs", canceler.calls, len(api.msgs))
	}
	if len(repo.markedIDs) != 1 || repo.markedIDs[0] != 40 {
		t.Errorf("expected game 40 marked done, got %v", repo.markedIDs)
	}
}

func TestCancellationReminder_AllCourtsReleased_RecordsSystemGameCanceled(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	// 0 players, 2 courts → both courts released.
	cbRepo := &stubCourtBookingRepo{entries: []*models.CourtBooking{
		algorithmCourtEntry("1", "match-1"),
		algorithmCourtEntry("2", "match-2"),
	}}
	game := makeGame("1,2", 2, time.Now().Add(24*time.Hour))
	game.ID = 10
	game.ChatID = 100
	game.Venue = &models.Venue{ID: 5}

	job := makeJobForPC(&captureSendAPI{}, &stubGameRepoPC{}, 0, 0, group, &spyNotifier{}, cbRepo, &mockBookingClient{})
	job.credService = makeAlgorithmCredService()
	auditSvc, auditRepo := newCaptureAuditSvc()
	job.auditSvc = auditSvc
	job.processCancellationReminder(context.Background(), game)

	var canceled []*models.AuditEvent
	for _, evt := range auditRepo.inserted {
		if evt.EventType == models.AuditEventGameCanceled {
			canceled = append(canceled, evt)
		}
	}
	if len(canceled) != 1 {
		t.Fatalf("game.canceled events = %d, want 1 (all events: %d)", len(canceled), len(auditRepo.inserted))
	}
	if canceled[0].ActorKind != models.AuditActorSystem || canceled[0].SubjectID != "10" {
		t.Errorf("event = %+v, want system actor for game 10", canceled[0])
	}
}

func TestCancellationReminder_SomeCourtsReleased_NoGameCanceled(t *testing.T) {
	group := &models.Group{ChatID: 100, Language: "en", Timezone: "UTC"}
	// 2 players, 2 courts → one court released, one remains.
	cbRepo := &stubCourtBookingRepo{entries: []*models.CourtBooking{
		algorithmCourtEntry("1", "match-1"),
		algorithmCourtEntry("2", "match-2"),
	}}
	game := makeGame("1,2", 2, time.Now().Add(24*time.Hour))
	game.ID = 11
	game.Venue = &models.Venue{ID: 5}

	job := makeJobForPC(&captureSendAPI{}, &stubGameRepoPC{}, 2, 0, group, &spyNotifier{}, cbRepo, &mockBookingClient{})
	job.credService = makeAlgorithmCredService()
	auditSvc, auditRepo := newCaptureAuditSvc()
	job.auditSvc = auditSvc
	job.processCancellationReminder(context.Background(), game)

	for _, evt := range auditRepo.inserted {
		if evt.EventType == models.AuditEventGameCanceled {
			t.Fatalf("unexpected game.canceled event when a court remains: %+v", evt)
		}
	}
	if len(auditRepo.inserted) == 0 {
		t.Fatal("expected a court.canceled event, got none; test setup did not release a court")
	}
}
