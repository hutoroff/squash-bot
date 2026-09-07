package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/booking/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/jackc/pgx/v5"
)

type fixedStatusStore struct {
	operation *storage.Operation
	steps     []*storage.Step
}

func (s fixedStatusStore) Get(context.Context, bookingapi.OperationID) (*storage.Operation, error) {
	if s.operation == nil {
		return nil, pgx.ErrNoRows
	}
	return s.operation, nil
}
func (s fixedStatusStore) ListSteps(context.Context, bookingapi.OperationID) ([]*storage.Step, error) {
	return s.steps, nil
}

type fixedStatusEvidence struct{ booking *storage.ProviderBooking }

func (s fixedStatusEvidence) GetProviderBooking(context.Context, bookingapi.OperationID) (*storage.ProviderBooking, error) {
	if s.booking == nil {
		return nil, pgx.ErrNoRows
	}
	return s.booking, nil
}

func TestStatusServiceProjectsSanitizedCreationEvidenceAndWarnings(t *testing.T) {
	bookingID, paymentID := int64(42), int64(7)
	now := time.Now().UTC()
	service, err := NewStatusService(fixedStatusStore{operation: &storage.Operation{
		OperationID: operation1, Kind: bookingapi.OperationCreate, State: bookingapi.StateSucceeded,
		Phase: bookingapi.PhaseMatchAttachment, Outcome: bookingapi.OutcomeSuccessWithWarning,
		Revision: 8, ParentOperationID: string(operation2), CreatedAt: now, UpdatedAt: now,
	}}, fixedStatusEvidence{booking: &storage.ProviderBooking{
		BookingUUID: "booking-1", BookingID: &bookingID, PaymentID: &paymentID,
		MatchID: "match-1", BookingState: "confirmed", PaymentState: "confirmed",
	}})
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.GetStatus(context.Background(), operation1)
	if err != nil {
		t.Fatal(err)
	}
	if status.Evidence == nil || status.Evidence.BookingUUID != "booking-1" || status.Evidence.BookingID != 42 || status.Evidence.PaymentID != 7 || status.Evidence.MatchID != "match-1" {
		t.Fatalf("status evidence = %+v", status.Evidence)
	}
	if len(status.Warnings) != 2 || status.StatusURL != OperationStatusURL(operation1) {
		t.Fatalf("status warnings/url = %+v/%q", status.Warnings, status.StatusURL)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"password", "email", "cookie", "account"} {
		if strings.Contains(strings.ToLower(string(encoded)), secret) {
			t.Fatalf("status contains secret-bearing field %q: %s", secret, encoded)
		}
	}
}

func TestStatusServiceMapsSanitizedTerminalCode(t *testing.T) {
	service, err := NewStatusService(fixedStatusStore{
		operation: &storage.Operation{OperationID: operation1, Kind: bookingapi.OperationCreate, State: bookingapi.StateRejectedNoEffect, Outcome: bookingapi.OutcomeNoEffect},
		steps:     []*storage.Step{{Status: "evidence", Code: "credential_rejected"}},
	}, fixedStatusEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.GetStatus(context.Background(), operation1)
	if err != nil {
		t.Fatal(err)
	}
	if status.Code != bookingapi.ErrorCredentialRejected {
		t.Fatalf("status code = %q", status.Code)
	}
}

func TestStatusServiceTombstoneOmitsDetailedEvidence(t *testing.T) {
	now := time.Now().UTC()
	service, err := NewStatusService(fixedStatusStore{operation: &storage.Operation{
		OperationID: operation1, Kind: bookingapi.OperationCreate, State: bookingapi.StateSucceeded,
		Outcome: bookingapi.OutcomeSuccess, ResultExpiredAt: &now,
	}}, fixedStatusEvidence{booking: &storage.ProviderBooking{BookingUUID: "must-not-leak"}})
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.GetStatus(context.Background(), operation1)
	var expired *ResultExpiredError
	if !errors.As(err, &expired) {
		t.Fatalf("expected result-expired error, got %v", err)
	}
	if !status.ResultExpired || status.Evidence != nil {
		t.Fatalf("expired status/result evidence = %t/%+v", status.ResultExpired, status.Evidence)
	}
}

func TestStatusServiceConfirmedCancellationEvidence(t *testing.T) {
	service, err := NewStatusService(fixedStatusStore{operation: &storage.Operation{
		OperationID: operation1, Kind: bookingapi.OperationCancel, TargetMatchID: "match-1",
		State: bookingapi.StateSucceeded, Outcome: bookingapi.OutcomeSuccess,
	}}, fixedStatusEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.GetStatus(context.Background(), operation1)
	if err != nil {
		t.Fatal(err)
	}
	if status.Evidence == nil || status.Evidence.MatchID != "match-1" || status.Evidence.State != "CANCELLED" {
		t.Fatalf("cancellation status = %+v", status)
	}
}
