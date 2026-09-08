package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/models"
)

type fakeReliableBookingClient struct {
	mu             sync.Mutex
	createStatuses []*bookingapi.OperationStatus
	createErr      error
	verifyErr      error
	statusResults  []*bookingapi.OperationStatus
	statusErr      error
	createCalls    int
	statusCalls    int
}

func (c *fakeReliableBookingClient) VerifyBinding(context.Context, bookingapi.Scope) error {
	return c.verifyErr
}
func (c *fakeReliableBookingClient) SubmitCreateOperation(_ context.Context, _ bookingapi.OperationID, _ bookingapi.CreateRequest) (*bookingapi.OperationStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.createCalls++
	if c.createErr != nil {
		return nil, c.createErr
	}
	return c.createStatuses[0], nil
}
func (*fakeReliableBookingClient) SubmitCancelOperation(context.Context, bookingapi.OperationID, string, bookingapi.CancelRequest) (*bookingapi.OperationStatus, error) {
	return nil, errors.New("unexpected cancellation")
}
func (c *fakeReliableBookingClient) GetOperationStatus(_ context.Context, _ bookingapi.OperationID) (*bookingapi.OperationStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.statusCalls++
	if c.statusErr != nil {
		return nil, c.statusErr
	}
	index := c.statusCalls - 1
	if index >= len(c.statusResults) {
		index = len(c.statusResults) - 1
	}
	return c.statusResults[index], nil
}
func (*fakeReliableBookingClient) ReconcileOperation(context.Context, bookingapi.OperationID, bookingapi.ReconcileRequest) (*bookingapi.OperationStatus, error) {
	return nil, errors.New("unexpected reconciliation")
}
func (*fakeReliableBookingClient) ResolveOperation(context.Context, bookingapi.OperationID, bookingapi.OperationID, bookingapi.ResolutionRequest) (*bookingapi.OperationStatus, error) {
	return nil, errors.New("unexpected resolution")
}

type fakeBookingProjectionRepository struct {
	operation *models.ManagementBookingOperation
}

func (r *fakeBookingProjectionRepository) GetOperation(context.Context, bookingapi.OperationID) (*models.ManagementBookingOperation, error) {
	return r.operation, nil
}
func (r *fakeBookingProjectionRepository) ApplyOperationStatus(_ context.Context, status *bookingapi.OperationStatus) (*models.ManagementBookingOperation, bool, error) {
	r.operation.State = mapTestManagementState(status.State)
	r.operation.Phase = status.Phase
	r.operation.Outcome = status.Outcome
	r.operation.Code = status.Code
	r.operation.Revision = status.Revision
	if status.Evidence != nil {
		r.operation.BookingUUID = status.Evidence.BookingUUID
		r.operation.MatchID = status.Evidence.MatchID
	}
	return r.operation, true, nil
}

func TestReliableBookingExecutorFailsClosedBeforeMutationOnBindingMismatch(t *testing.T) {
	operation := testPlannedManagementOperation()
	client := &fakeReliableBookingClient{verifyErr: errors.New("facility mismatch")}
	executor, err := NewReliableBookingExecutor(client, &fakeBookingProjectionRepository{operation: operation}, testManagementBookingScope(), time.Second, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.ExecuteCreate(context.Background(), operation, bookingapi.Credentials{Email: "a", Password: "b"})
	if err == nil || result.OperationID != operation.OperationID || client.createCalls != 0 {
		t.Fatalf("result = %+v err = %v create calls = %d", result, err, client.createCalls)
	}
}

func TestReliableBookingExecutorPersistsAcceptedThenTerminalStatus(t *testing.T) {
	operation := testPlannedManagementOperation()
	client := &fakeReliableBookingClient{
		createStatuses: []*bookingapi.OperationStatus{testOperationStatus(bookingapi.StateAccepted, bookingapi.OutcomePending, 0)},
		statusResults:  []*bookingapi.OperationStatus{testOperationStatus(bookingapi.StateSucceeded, bookingapi.OutcomeSuccess, 1)},
	}
	repository := &fakeBookingProjectionRepository{operation: operation}
	executor, err := NewReliableBookingExecutor(client, repository, testManagementBookingScope(), time.Second, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.ExecuteCreate(context.Background(), operation, bookingapi.Credentials{Email: "a", Password: "b"})
	if err != nil {
		t.Fatalf("ExecuteCreate: %v", err)
	}
	if !ManagementBookingSucceeded(result) || result.MatchID != "match-1" || client.createCalls != 1 || client.statusCalls != 1 {
		t.Fatalf("result = %+v, create calls %d, status calls %d", result, client.createCalls, client.statusCalls)
	}
}

func TestReliableBookingExecutorRecoversTransportAmbiguityBySameKeyStatus(t *testing.T) {
	operation := testPlannedManagementOperation()
	client := &fakeReliableBookingClient{
		createErr:     errors.New("response lost"),
		statusResults: []*bookingapi.OperationStatus{testOperationStatus(bookingapi.StateOutcomeUnknown, bookingapi.OutcomeUnknown, 2)},
	}
	executor, err := NewReliableBookingExecutor(client, &fakeBookingProjectionRepository{operation: operation}, testManagementBookingScope(), time.Second, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.ExecuteCreate(context.Background(), operation, bookingapi.Credentials{Email: "a", Password: "b"})
	if err != nil {
		t.Fatalf("ExecuteCreate: %v", err)
	}
	if result.State != models.ManagementBookingOperationOutcomeUnknown || client.createCalls != 1 || client.statusCalls != 1 {
		t.Fatalf("result = %+v, create calls %d, status calls %d", result, client.createCalls, client.statusCalls)
	}
}

func TestReliableBookingExecutorPersistsProvenPreAdmissionConflictWithoutStatusRetry(t *testing.T) {
	operation := testPlannedManagementOperation()
	client := &fakeReliableBookingClient{createErr: &BookingContractError{
		HTTPStatus: 409, Code: bookingapi.ErrorBookingConflict, OperationID: operation.OperationID,
	}}
	executor, err := NewReliableBookingExecutor(client, &fakeBookingProjectionRepository{operation: operation}, testManagementBookingScope(), time.Second, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.ExecuteCreate(context.Background(), operation, bookingapi.Credentials{Email: "a", Password: "b"})
	if err != nil || result.State != models.ManagementBookingOperationNotDispatched || result.Outcome != bookingapi.OutcomeNoEffect {
		t.Fatalf("result = %+v err = %v", result, err)
	}
	if client.createCalls != 1 || client.statusCalls != 0 {
		t.Fatalf("create calls %d status calls %d", client.createCalls, client.statusCalls)
	}
}

func TestReliableBookingExecutorDoesNotReplaceKeyWhenStatusIsMissing(t *testing.T) {
	operation := testPlannedManagementOperation()
	client := &fakeReliableBookingClient{
		createErr: errors.New("request failed"),
		statusErr: &BookingContractError{HTTPStatus: 404, Code: bookingapi.ErrorOperationNotFound, OperationID: operation.OperationID},
	}
	executor, err := NewReliableBookingExecutor(client, &fakeBookingProjectionRepository{operation: operation}, testManagementBookingScope(), time.Second, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.ExecuteCreate(context.Background(), operation, bookingapi.Credentials{Email: "a", Password: "b"})
	if err == nil || result.OperationID != operation.OperationID || client.createCalls != 1 || client.statusCalls != 1 {
		t.Fatalf("result = %+v err = %v create calls %d status calls %d", result, err, client.createCalls, client.statusCalls)
	}
}

func TestManagementBookingCredentialRejectedRequiresDurableNoEffectEvidence(t *testing.T) {
	operation := testPlannedManagementOperation()
	operation.State = models.ManagementBookingOperationRejectedNoEffect
	operation.Outcome = bookingapi.OutcomeNoEffect
	operation.Code = bookingapi.ErrorCredentialRejected
	if !ManagementBookingCredentialRejected(operation) {
		t.Fatal("typed credential rejection was not recognized")
	}
	operation.Outcome = bookingapi.OutcomeUnknown
	if ManagementBookingCredentialRejected(operation) {
		t.Fatal("unknown outcome was treated as credential rejection")
	}
}

func testPlannedManagementOperation() *models.ManagementBookingOperation {
	return &models.ManagementBookingOperation{
		OperationID: managementTestOperationID, IntentID: 1, Kind: bookingapi.OperationCreate,
		CourtUUID: "court-1", StartProvider: "2027-01-10T18:00:00.000+01:00", EndProvider: "2027-01-10T18:45:00.000+01:00",
		State: models.ManagementBookingOperationPlanned, Phase: bookingapi.PhaseAdmission, Outcome: bookingapi.OutcomePending,
	}
}

func testOperationStatus(state bookingapi.OperationState, outcome bookingapi.OutcomeCategory, revision int64) *bookingapi.OperationStatus {
	return &bookingapi.OperationStatus{
		OperationID: string(managementTestOperationID), Kind: bookingapi.OperationCreate,
		State: state, Phase: bookingapi.PhaseFinalization, Outcome: outcome, Revision: revision,
		Evidence: &bookingapi.ProviderEvidence{BookingUUID: "booking-1", MatchID: "match-1"},
	}
}

func mapTestManagementState(state bookingapi.OperationState) models.ManagementBookingOperationState {
	switch state {
	case bookingapi.StateAccepted:
		return models.ManagementBookingOperationAccepted
	case bookingapi.StateSucceeded:
		return models.ManagementBookingOperationSucceeded
	case bookingapi.StateOutcomeUnknown:
		return models.ManagementBookingOperationOutcomeUnknown
	case bookingapi.StateRejectedNoEffect:
		return models.ManagementBookingOperationRejectedNoEffect
	case bookingapi.StateNotDispatched:
		return models.ManagementBookingOperationNotDispatched
	default:
		return models.ManagementBookingOperationRunning
	}
}
