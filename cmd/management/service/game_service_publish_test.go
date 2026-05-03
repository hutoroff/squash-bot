package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/jackc/pgx/v5"
	"github.com/hutoroff/squash-bot/internal/models"
)

// newPublishSvc builds a GameService wired for PublishGame unit tests.
// participationRepo, guestRepo, and groupRepo use lightweight no-op stubs;
// the caller supplies the repos that need custom behaviour.
func newPublishSvc(gameRepo GameRepository, api TelegramAPI, auditSvc *AuditService) *GameService {
	return &GameService{
		gameRepo:          gameRepo,
		participationRepo: &stubParticipationRepo{},
		guestRepo:         &stubGuestParticipationRepo{},
		groupRepo:         &stubGroupRepoForDayAfter{},
		auditSvc:          auditSvc,
		api:               api,
		defaultLoc:        time.UTC,
		logger:            noopLogger(),
	}
}

// TestPublishGame_AlreadyPublished verifies that a game with message_id already set
// returns ErrGameAlreadyPublished without sending any Telegram message.
func TestPublishGame_AlreadyPublished(t *testing.T) {
	msgID := int64(99)
	repo := &mockGameRepo{getByIDResult: &models.Game{ID: 1, MessageID: &msgID}}
	api := &mockTelegramAPI{}
	svc := newPublishSvc(repo, api, nil)

	_, err := svc.PublishGame(context.Background(), 1, 0, "")
	if !errors.Is(err, ErrGameAlreadyPublished) {
		t.Errorf("want ErrGameAlreadyPublished, got %v", err)
	}
	if len(api.sendCalls) != 0 {
		t.Errorf("expected no Telegram sends, got %d", len(api.sendCalls))
	}
}

// TestPublishGame_GameNotFound verifies that a pgx.ErrNoRows from GetByID is surfaced
// as ErrGameNotFound.
func TestPublishGame_GameNotFound(t *testing.T) {
	repo := &mockGameRepo{getByIDErr: fmt.Errorf("scan game: %w", pgx.ErrNoRows)}
	svc := newPublishSvc(repo, &mockTelegramAPI{}, nil)

	_, err := svc.PublishGame(context.Background(), 1, 0, "")
	if !errors.Is(err, ErrGameNotFound) {
		t.Errorf("want ErrGameNotFound, got %v", err)
	}
}

// TestPublishGame_TransientDBError_NotMaskedAsNotFound verifies that a real database
// error (e.g. connection refused) is not silently mapped to ErrGameNotFound.
func TestPublishGame_TransientDBError_NotMaskedAsNotFound(t *testing.T) {
	repo := &mockGameRepo{getByIDErr: errors.New("connection refused")}
	svc := newPublishSvc(repo, &mockTelegramAPI{}, nil)

	_, err := svc.PublishGame(context.Background(), 1, 0, "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, ErrGameNotFound) {
		t.Error("transient DB error must not be reported as ErrGameNotFound")
	}
}

// TestPublishGame_SendFailure_UpdateMessageIDNotCalled verifies that when the Telegram
// announcement fails, UpdateMessageID is never called — the game row stays cleanly
// unpublished so the next attempt can retry from a consistent state.
func TestPublishGame_SendFailure_UpdateMessageIDNotCalled(t *testing.T) {
	repo := &mockGameRepo{}
	api := &mockTelegramAPI{sendErr: errors.New("telegram timeout")}
	svc := newPublishSvc(repo, api, nil)

	_, err := svc.PublishGame(context.Background(), 1, 0, "")
	if err == nil {
		t.Fatal("expected error from Send failure")
	}
	if repo.updateMsgCalled {
		t.Error("UpdateMessageID must not be called when Send fails")
	}
}

// TestPublishGame_UpdateMessageIDFailure_DeletesOrphan verifies that when the
// announcement is sent successfully but UpdateMessageID fails, PublishGame deletes
// the orphaned Telegram message to prevent duplicate announcements on retry.
func TestPublishGame_UpdateMessageIDFailure_DeletesOrphan(t *testing.T) {
	repo := &mockGameRepo{updateMsgErr: errors.New("db write failed")}
	api := &mockTelegramAPI{sendResult: tgbotapi.Message{MessageID: 42}}
	svc := newPublishSvc(repo, api, nil)

	_, err := svc.PublishGame(context.Background(), 1, 0, "")
	if err == nil {
		t.Fatal("expected error when UpdateMessageID fails")
	}
	foundDelete := false
	for _, call := range api.requestCalls {
		if _, ok := call.(tgbotapi.DeleteMessageConfig); ok {
			foundDelete = true
		}
	}
	if !foundDelete {
		t.Error("expected Request(DeleteMessageConfig) to clean up orphaned announcement")
	}
}

// TestPublishGame_Success verifies the happy path: announcement sent, message_id
// persisted, and a game_published audit event recorded.
func TestPublishGame_Success(t *testing.T) {
	repo := &mockGameRepo{}
	api := &mockTelegramAPI{sendResult: tgbotapi.Message{MessageID: 10}}
	auditSvc, auditCapture := newCaptureAuditSvc()
	svc := newPublishSvc(repo, api, auditSvc)

	game, err := svc.PublishGame(context.Background(), 1, 42, "alice")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if game == nil {
		t.Fatal("expected non-nil game")
	}
	if !repo.updateMsgCalled {
		t.Error("expected UpdateMessageID to be called on success")
	}
	if len(auditCapture.inserted) == 0 {
		t.Fatal("expected audit event to be recorded")
	}
	if auditCapture.inserted[0].EventType != models.AuditEventGamePublished {
		t.Errorf("expected game.published audit event, got %q", auditCapture.inserted[0].EventType)
	}
}
