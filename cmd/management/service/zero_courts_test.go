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
