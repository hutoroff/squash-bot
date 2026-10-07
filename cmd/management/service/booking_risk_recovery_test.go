package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/models"
)

type riskRecoveryRepo struct {
	captureBookingIntentRepository
	child    *models.ManagementBookingOperation
	attempts int
}

func (r *riskRecoveryRepo) GetIntent(context.Context, int64) (*models.BookingIntent, error) {
	return &models.BookingIntent{Scope: testManagementBookingScope()}, nil
}
func (r *riskRecoveryRepo) ListRecoverableOperations(context.Context, int) ([]*models.ManagementBookingOperation, error) {
	return []*models.ManagementBookingOperation{r.child}, nil
}
func (r *riskRecoveryRepo) MarkRecoveryAttempt(context.Context, bookingapi.OperationID) error {
	r.attempts++
	return nil
}

type noRiskCourtWrites struct{ CourtBookingRepository }

func TestRiskRecoveryObservesButNeverResubmitsPlannedChild(t *testing.T) {
	child := testPlannedManagementOperation()
	child.ParentOperationID = "aaaaaaaa-aaaa-7aaa-8aaa-aaaaaaaaaaaa"
	repo := &riskRecoveryRepo{child: child}
	intents, err := NewBookingIntentService(repo)
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeReliableBookingClient{statusErr: errors.New("booking status unavailable")}
	executor, err := NewReliableBookingExecutor(client, &fakeBookingProjectionRepository{operation: child}, testManagementBookingScope(), time.Second, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := NewReliableBookingRecovery(repo, intents, executor, &VenueCredentialService{}, &noRiskCourtWrites{}, testManagementBookingScope(), slog.New(slog.NewTextHandler(io.Discard, nil)), 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := recovery.RunOnce(context.Background()); err == nil {
		t.Fatal("missing risk child status reported as success")
	}
	if client.createCalls != 0 || client.statusCalls != 1 || repo.attempts != 1 {
		t.Fatalf("creates=%d status=%d attempts=%d", client.createCalls, client.statusCalls, repo.attempts)
	}
}
