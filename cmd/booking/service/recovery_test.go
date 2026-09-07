package service

import (
	"context"
	"errors"
	"testing"

	"github.com/hutoroff/squash-bot/cmd/booking/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/jackc/pgx/v5"
)

type fakeRecoveryStore struct {
	operations []*storage.Operation
	steps      map[bookingapi.OperationID][]*storage.Step
	finalized  []RecoveryResult
}

func (s *fakeRecoveryStore) ListUnfinished(context.Context) ([]*storage.Operation, error) {
	return s.operations, nil
}
func (s *fakeRecoveryStore) ListSteps(_ context.Context, id bookingapi.OperationID) ([]*storage.Step, error) {
	return s.steps[id], nil
}
func (s *fakeRecoveryStore) Finalize(_ context.Context, id, _ bookingapi.OperationID, state bookingapi.OperationState, outcome bookingapi.OutcomeCategory, phase bookingapi.OperationPhase) (*storage.Operation, error) {
	result := RecoveryResult{OperationID: id, State: state, Outcome: outcome, Phase: phase}
	s.finalized = append(s.finalized, result)
	return &storage.Operation{OperationID: id, State: state, Outcome: outcome, Phase: phase}, nil
}

type fakeRecoveryEvidence struct {
	bookings map[bookingapi.OperationID]*storage.ProviderBooking
}

func (s *fakeRecoveryEvidence) GetProviderBooking(_ context.Context, id bookingapi.OperationID) (*storage.ProviderBooking, error) {
	booking := s.bookings[id]
	if booking == nil {
		return nil, pgx.ErrNoRows
	}
	return booking, nil
}

func TestRecoveryDerivesTerminalOutcomesWithoutProviderExecution(t *testing.T) {
	bookingID, paymentID := int64(42), int64(7)
	tests := []struct {
		name        string
		kind        bookingapi.OperationKind
		phase       bookingapi.OperationPhase
		steps       []*storage.Step
		booking     *storage.ProviderBooking
		wantState   bookingapi.OperationState
		wantOutcome bookingapi.OutcomeCategory
	}{
		{name: "no mutation intent", kind: bookingapi.OperationCreate, phase: bookingapi.PhaseLogin, steps: []*storage.Step{evidenceStep(bookingapi.PhaseLogin, 1, "confirmed")}, wantState: bookingapi.StateNotDispatched, wantOutcome: bookingapi.OutcomeNoEffect},
		{name: "provider evidence without mutation intent fails closed", kind: bookingapi.OperationCreate, phase: bookingapi.PhaseLogin, booking: &storage.ProviderBooking{BookingUUID: "booking-1", BookingID: &bookingID, PaymentID: &paymentID, BookingState: "reserved", PaymentState: "unknown"}, wantState: bookingapi.StateOutcomeUnknown, wantOutcome: bookingapi.OutcomeUnknown},
		{name: "all mutation attempts no effect", kind: bookingapi.OperationCreate, phase: bookingapi.PhaseReserve, steps: []*storage.Step{intentStep(bookingapi.PhaseReserve, 1), evidenceStep(bookingapi.PhaseReserve, 1, "no_effect")}, wantState: bookingapi.StateRejectedNoEffect, wantOutcome: bookingapi.OutcomeNoEffect},
		{name: "intent without evidence", kind: bookingapi.OperationCreate, phase: bookingapi.PhaseReserve, steps: []*storage.Step{intentStep(bookingapi.PhaseReserve, 1)}, wantState: bookingapi.StateOutcomeUnknown, wantOutcome: bookingapi.OutcomeUnknown},
		{name: "reserve confirmed payment absent", kind: bookingapi.OperationCreate, phase: bookingapi.PhasePayment, steps: []*storage.Step{intentStep(bookingapi.PhaseReserve, 1), evidenceStep(bookingapi.PhaseReserve, 1, "confirmed")}, booking: &storage.ProviderBooking{BookingUUID: "booking-1", BookingID: &bookingID, PaymentID: &paymentID, BookingState: "reserved", PaymentState: "unknown"}, wantState: bookingapi.StateOutcomeUnknown, wantOutcome: bookingapi.OutcomeUnknown},
		{name: "paid booking without attachment evidence", kind: bookingapi.OperationCreate, phase: bookingapi.PhasePayment, steps: successfulCreateSteps(false), booking: &storage.ProviderBooking{BookingUUID: "booking-1", BookingID: &bookingID, PaymentID: &paymentID, BookingState: "confirmed", PaymentState: "confirmed"}, wantState: bookingapi.StateSucceeded, wantOutcome: bookingapi.OutcomeSuccessWithWarning},
		{name: "paid booking with attachment evidence", kind: bookingapi.OperationCreate, phase: bookingapi.PhaseMatchAttachment, steps: successfulCreateSteps(true), booking: &storage.ProviderBooking{BookingUUID: "booking-1", BookingID: &bookingID, PaymentID: &paymentID, MatchID: "match-1", BookingState: "confirmed", PaymentState: "confirmed"}, wantState: bookingapi.StateSucceeded, wantOutcome: bookingapi.OutcomeSuccess},
		{name: "confirmed cancellation", kind: bookingapi.OperationCancel, phase: bookingapi.PhaseCancellation, steps: []*storage.Step{intentStep(bookingapi.PhaseCancellation, 1), evidenceStep(bookingapi.PhaseCancellation, 1, "confirmed")}, wantState: bookingapi.StateSucceeded, wantOutcome: bookingapi.OutcomeSuccess},
		{name: "ambiguous cancellation", kind: bookingapi.OperationCancel, phase: bookingapi.PhaseCancellation, steps: []*storage.Step{intentStep(bookingapi.PhaseCancellation, 1), evidenceStep(bookingapi.PhaseCancellation, 1, "unknown")}, wantState: bookingapi.StateOutcomeUnknown, wantOutcome: bookingapi.OutcomeUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			operation := &storage.Operation{OperationID: operation1, Kind: tc.kind, Phase: tc.phase, ExecutorID: string(operation2)}
			operations := &fakeRecoveryStore{operations: []*storage.Operation{operation}, steps: map[bookingapi.OperationID][]*storage.Step{operation1: tc.steps}}
			evidence := &fakeRecoveryEvidence{bookings: map[bookingapi.OperationID]*storage.ProviderBooking{operation1: tc.booking}}
			recovery, err := NewRecovery(operations, evidence, newFakeAuthority())
			if err != nil {
				t.Fatal(err)
			}
			results, err := recovery.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || results[0].State != tc.wantState || results[0].Outcome != tc.wantOutcome {
				t.Fatalf("results = %+v, want %s/%s", results, tc.wantState, tc.wantOutcome)
			}
		})
	}
}

func TestRecoveryRequiresLiveSingletonAuthority(t *testing.T) {
	authority := newFakeAuthority()
	authority.lose()
	recovery, err := NewRecovery(
		&fakeRecoveryStore{},
		&fakeRecoveryEvidence{bookings: map[bookingapi.OperationID]*storage.ProviderBooking{}},
		authority,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recovery.Run(context.Background()); !errors.Is(err, ErrCoordinatorClosed) {
		t.Fatalf("error = %v, want coordinator closed", err)
	}
}

func TestRecoveryStopsAfterFinalizationFailure(t *testing.T) {
	operations := &failingRecoveryStore{fakeRecoveryStore: fakeRecoveryStore{
		operations: []*storage.Operation{{OperationID: operation1, Kind: bookingapi.OperationCreate, Phase: bookingapi.PhaseAdmission, ExecutorID: string(operation2)}},
		steps:      map[bookingapi.OperationID][]*storage.Step{},
	}}
	recovery, err := NewRecovery(operations, &fakeRecoveryEvidence{bookings: map[bookingapi.OperationID]*storage.ProviderBooking{}}, newFakeAuthority())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recovery.Run(context.Background()); err == nil {
		t.Fatal("recovery accepted a finalization failure")
	}
}

type failingRecoveryStore struct{ fakeRecoveryStore }

func (s *failingRecoveryStore) Finalize(context.Context, bookingapi.OperationID, bookingapi.OperationID, bookingapi.OperationState, bookingapi.OutcomeCategory, bookingapi.OperationPhase) (*storage.Operation, error) {
	return nil, errors.New("database unavailable")
}

func intentStep(phase bookingapi.OperationPhase, attempt int) *storage.Step {
	return &storage.Step{Phase: phase, Attempt: attempt, Status: "intent"}
}
func evidenceStep(phase bookingapi.OperationPhase, attempt int, effect string) *storage.Step {
	return &storage.Step{Phase: phase, Attempt: attempt, Status: "evidence", Effect: effect}
}
func successfulCreateSteps(withAttachment bool) []*storage.Step {
	steps := []*storage.Step{
		intentStep(bookingapi.PhaseReserve, 1), evidenceStep(bookingapi.PhaseReserve, 1, "confirmed"),
		intentStep(bookingapi.PhasePayment, 1), evidenceStep(bookingapi.PhasePayment, 1, "confirmed"),
	}
	if withAttachment {
		steps = append(steps, intentStep(bookingapi.PhaseMatchAttachment, 1), evidenceStep(bookingapi.PhaseMatchAttachment, 1, "confirmed"))
	}
	return steps
}
