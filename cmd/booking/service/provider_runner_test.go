package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/hutoroff/squash-bot/cmd/booking/eversports"
	"github.com/hutoroff/squash-bot/cmd/booking/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
)

var providerRunnerKey = []byte("0123456789abcdef0123456789abcdef")

type journalCall struct {
	kind    string
	phase   bookingapi.OperationPhase
	attempt int
	effect  string
	code    string
}

type fakeStepJournal struct {
	mu           sync.Mutex
	calls        []journalCall
	failIntent   map[string]error
	failEvidence map[string]error
	alias        string
	payloads     []any
}

func (j *fakeStepJournal) AppendStepIntent(ctx context.Context, operationID bookingapi.OperationID, phase bookingapi.OperationPhase, attempt int, _ bookingapi.OperationID) (*storage.Step, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.calls = append(j.calls, journalCall{kind: "intent", phase: phase, attempt: attempt})
	if err := j.failIntent[stepKey(phase, attempt)]; err != nil {
		return nil, err
	}
	return &storage.Step{OperationID: operationID, Phase: phase, Attempt: attempt, Status: "intent"}, nil
}

func (j *fakeStepJournal) AppendStepEvidence(ctx context.Context, operationID bookingapi.OperationID, phase bookingapi.OperationPhase, attempt int, effect, code string, evidence any, _ bookingapi.OperationID) (*storage.Step, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.calls = append(j.calls, journalCall{kind: "evidence", phase: phase, attempt: attempt, effect: effect, code: code})
	j.payloads = append(j.payloads, evidence)
	if err := j.failEvidence[stepKey(phase, attempt)]; err != nil {
		return nil, err
	}
	return &storage.Step{OperationID: operationID, Phase: phase, Attempt: attempt, Status: "evidence", Effect: effect, Code: code}, nil
}

func (j *fakeStepJournal) SetProviderUserAlias(_ context.Context, _, _, alias string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.alias = alias
	return nil
}

func stepKey(phase bookingapi.OperationPhase, attempt int) string {
	return string(phase) + ":" + string(rune('0'+attempt))
}

type fakeProviderEvidence struct {
	bookings []storage.ProviderBooking
	failAt   int
}

func (e *fakeProviderEvidence) UpsertProviderBooking(_ context.Context, booking storage.ProviderBooking, _ bookingapi.OperationID) (*storage.ProviderBooking, error) {
	e.bookings = append(e.bookings, booking)
	if e.failAt > 0 && len(e.bookings) == e.failAt {
		return nil, errors.New("database unavailable")
	}
	copy := booking
	return &copy, nil
}

type fakeProviderClient struct {
	loginErrs    []error
	reserve      []*eversports.ReservationResult
	reserveErrs  []error
	paymentErr   error
	attachID     string
	attachErr    error
	cancel       []*eversports.CancellationResult
	cancelErrs   []error
	afterReserve func()
	trackingErr  error

	logins, refreshes, reserves, payments, attachments, tracking, cancellations int
	startProvider, endProvider                                                  string
}

func (c *fakeProviderClient) EnsureLoggedIn(context.Context) error {
	c.logins++
	return indexedError(c.loginErrs, c.logins-1)
}
func (c *fakeProviderClient) Reauthenticate(context.Context) error {
	c.refreshes++
	return indexedError(c.loginErrs, c.logins+c.refreshes-1)
}
func (c *fakeProviderClient) ProviderUserID() string { return "provider-user-1" }
func (c *fakeProviderClient) ReserveCourtProviderTime(_ context.Context, _, _, _, start, end string) (*eversports.ReservationResult, error) {
	c.reserves++
	c.startProvider, c.endProvider = start, end
	if err := indexedError(c.reserveErrs, c.reserves-1); err != nil {
		return nil, err
	}
	var result *eversports.ReservationResult
	if len(c.reserve) >= c.reserves {
		result = c.reserve[c.reserves-1]
	} else {
		result = &eversports.ReservationResult{BookingUUID: "booking-1", BookingID: 42, PaymentID: 7, Status: "CONFIRMED"}
	}
	if c.afterReserve != nil {
		c.afterReserve()
	}
	return result, nil
}
func (c *fakeProviderClient) PayOffline(context.Context, int) error {
	c.payments++
	return c.paymentErr
}
func (c *fakeProviderClient) AttachMatch(context.Context, string) (string, error) {
	c.attachments++
	if c.attachID == "" {
		c.attachID = "match-1"
	}
	return c.attachID, c.attachErr
}
func (c *fakeProviderClient) CompleteBookingTracking(context.Context, int, string) error {
	c.tracking++
	return c.trackingErr
}
func (c *fakeProviderClient) CancelMatchOnce(context.Context, string) (*eversports.CancellationResult, error) {
	c.cancellations++
	if err := indexedError(c.cancelErrs, c.cancellations-1); err != nil {
		return nil, err
	}
	if len(c.cancel) >= c.cancellations {
		return c.cancel[c.cancellations-1], nil
	}
	return &eversports.CancellationResult{ID: "match-1", State: "CANCELLED"}, nil
}

func indexedError(values []error, index int) error {
	if index >= 0 && index < len(values) {
		return values[index]
	}
	return nil
}

func TestProviderRunnerCreateJournalsBeforeEachPhaseAndPreservesProviderOffset(t *testing.T) {
	runner, journal, evidence := newTestProviderRunner(t)
	client := &fakeProviderClient{}
	completion, err := runner.Run(context.Background(), createExecution(client))
	if err != nil {
		t.Fatal(err)
	}
	if completion.State != bookingapi.StateSucceeded || completion.Outcome != bookingapi.OutcomeSuccess {
		t.Fatalf("completion = %+v", completion)
	}
	if client.reserves != 1 || client.payments != 1 || client.attachments != 1 || client.tracking != 1 {
		t.Fatalf("provider calls reserve/pay/attach/track = %d/%d/%d/%d", client.reserves, client.payments, client.attachments, client.tracking)
	}
	if client.startProvider != "2026-08-10T20:45:00.000+02:00" || client.endProvider != "2026-08-10T21:30:00.000+02:00" {
		t.Fatalf("provider times = %q/%q", client.startProvider, client.endProvider)
	}
	want := []journalCall{
		{kind: "intent", phase: bookingapi.PhaseLogin, attempt: 1},
		{kind: "evidence", phase: bookingapi.PhaseLogin, attempt: 1, effect: confirmedEffect, code: "authenticated"},
		{kind: "intent", phase: bookingapi.PhaseReserve, attempt: 1},
		{kind: "evidence", phase: bookingapi.PhaseReserve, attempt: 1, effect: confirmedEffect, code: "reserved"},
		{kind: "intent", phase: bookingapi.PhasePayment, attempt: 1},
		{kind: "evidence", phase: bookingapi.PhasePayment, attempt: 1, effect: confirmedEffect, code: "payment_acknowledged"},
		{kind: "intent", phase: bookingapi.PhaseMatchAttachment, attempt: 1},
		{kind: "evidence", phase: bookingapi.PhaseMatchAttachment, attempt: 1, effect: confirmedEffect, code: "match_attached"},
	}
	if len(journal.calls) != len(want) {
		t.Fatalf("journal calls = %+v", journal.calls)
	}
	for i := range want {
		if journal.calls[i] != want[i] {
			t.Fatalf("journal call %d = %+v, want %+v", i, journal.calls[i], want[i])
		}
	}
	if len(evidence.bookings) != 3 || evidence.bookings[0].PaymentState != "unknown" || evidence.bookings[1].PaymentState != "confirmed" || evidence.bookings[2].MatchID != "match-1" {
		t.Fatalf("provider evidence = %+v", evidence.bookings)
	}
	if journal.alias == "" || strings.Contains(journal.alias, "provider-user-1") {
		t.Fatalf("provider alias was not keyed: %q", journal.alias)
	}
}

func TestProviderRunnerIntentFailurePreventsFirstMutation(t *testing.T) {
	runner, journal, evidence := newTestProviderRunner(t)
	journal.failIntent[stepKey(bookingapi.PhaseReserve, 1)] = errors.New("commit failed")
	client := &fakeProviderClient{}
	completion, err := runner.Run(context.Background(), createExecution(client))
	if err == nil || completion.State != bookingapi.StateNotDispatched {
		t.Fatalf("completion/error = %+v/%v", completion, err)
	}
	if client.reserves != 0 || client.payments != 0 || len(evidence.bookings) != 0 {
		t.Fatalf("mutation escaped failed intent: client=%+v evidence=%+v", client, evidence.bookings)
	}
}

func TestProviderRunnerReserveUnknownDoesNotRetryOrAdvance(t *testing.T) {
	runner, _, evidence := newTestProviderRunner(t)
	client := &fakeProviderClient{reserveErrs: []error{mutationError(eversports.MutationPhaseReserve, eversports.MutationUnknown, eversports.MutationCodeTransportError, true)}}
	completion, err := runner.Run(context.Background(), createExecution(client))
	if err == nil || completion.State != bookingapi.StateOutcomeUnknown || !completion.DiscardSession {
		t.Fatalf("completion/error = %+v/%v", completion, err)
	}
	if client.reserves != 1 || client.refreshes != 0 || client.payments != 0 || len(evidence.bookings) != 0 {
		t.Fatalf("calls reserve/refresh/pay = %d/%d/%d; evidence=%+v", client.reserves, client.refreshes, client.payments, evidence.bookings)
	}
	if strings.Contains(err.Error(), "secret provider body") {
		t.Fatalf("runner exposed raw provider error: %v", err)
	}
}

func TestProviderRunnerRetriesOnlyJournaledNoEffectReserveAuthentication(t *testing.T) {
	runner, journal, _ := newTestProviderRunner(t)
	client := &fakeProviderClient{reserveErrs: []error{
		mutationError(eversports.MutationPhaseReserve, eversports.MutationNoEffect, eversports.MutationCodeAuthenticationExpired, true), nil,
	}}
	completion, err := runner.Run(context.Background(), createExecution(client))
	if err != nil || completion.State != bookingapi.StateSucceeded {
		t.Fatalf("completion/error = %+v/%v", completion, err)
	}
	if client.reserves != 2 || client.refreshes != 1 || client.payments != 1 {
		t.Fatalf("calls reserve/refresh/pay = %d/%d/%d", client.reserves, client.refreshes, client.payments)
	}
	assertJournalCall(t, journal.calls, journalCall{kind: "evidence", phase: bookingapi.PhaseReserve, attempt: 1, effect: "no_effect", code: "authentication_expired"})
	assertJournalCall(t, journal.calls, journalCall{kind: "intent", phase: bookingapi.PhaseLogin, attempt: 2})
	assertJournalCall(t, journal.calls, journalCall{kind: "intent", phase: bookingapi.PhaseReserve, attempt: 2})
	assertJournalCall(t, journal.calls, journalCall{kind: "evidence", phase: bookingapi.PhaseReserve, attempt: 2, effect: confirmedEffect, code: "reserved"})
}

func TestProviderRunnerSecondReserveIntentFailureRemainsNoEffect(t *testing.T) {
	runner, journal, _ := newTestProviderRunner(t)
	journal.failIntent[stepKey(bookingapi.PhaseReserve, 2)] = errors.New("commit failed")
	client := &fakeProviderClient{reserveErrs: []error{
		mutationError(eversports.MutationPhaseReserve, eversports.MutationNoEffect, eversports.MutationCodeAuthenticationExpired, true), nil,
	}}
	completion, err := runner.Run(context.Background(), createExecution(client))
	if err == nil || completion.State != bookingapi.StateRejectedNoEffect {
		t.Fatalf("completion/error = %+v/%v", completion, err)
	}
	if client.reserves != 1 || client.payments != 0 {
		t.Fatalf("reserve/payment calls = %d/%d", client.reserves, client.payments)
	}
}

func TestProviderRunnerReserveEvidenceFailureRetainsKnownIdentifiersAndStopsPayment(t *testing.T) {
	runner, journal, evidence := newTestProviderRunner(t)
	journal.failEvidence[stepKey(bookingapi.PhaseReserve, 1)] = errors.New("evidence commit failed")
	client := &fakeProviderClient{}
	completion, err := runner.Run(context.Background(), createExecution(client))
	if err == nil || completion.State != bookingapi.StateOutcomeUnknown {
		t.Fatalf("completion/error = %+v/%v", completion, err)
	}
	if client.payments != 0 || client.attachments != 0 {
		t.Fatalf("provider advanced after persistence failure: pay/attach=%d/%d", client.payments, client.attachments)
	}
	if len(evidence.bookings) != 1 || evidence.bookings[0].BookingUUID != "booking-1" || evidence.bookings[0].PaymentID == nil || *evidence.bookings[0].PaymentID != 7 {
		t.Fatalf("known reservation identifiers were not retained: %+v", evidence.bookings)
	}
}

func TestProviderRunnerReserveProviderRowFailureLeavesIdentifiersInStepEvidence(t *testing.T) {
	runner, journal, evidence := newTestProviderRunner(t)
	evidence.failAt = 1
	client := &fakeProviderClient{}
	completion, err := runner.Run(context.Background(), createExecution(client))
	if err == nil || completion.State != bookingapi.StateOutcomeUnknown || client.payments != 0 {
		t.Fatalf("completion/error/payments = %+v/%v/%d", completion, err, client.payments)
	}
	found := false
	for _, payload := range journal.payloads {
		providerEvidence, ok := payload.(bookingapi.ProviderEvidence)
		if ok && providerEvidence.BookingUUID == "booking-1" && providerEvidence.PaymentID == 7 {
			found = true
		}
	}
	if !found {
		t.Fatalf("known identifiers absent from journal payloads: %+v", journal.payloads)
	}
}

func TestProviderRunnerPersistsReceivedReserveEvidenceAfterExecutionCancellation(t *testing.T) {
	runner, journal, evidence := newTestProviderRunner(t)
	ctx, cancel := context.WithCancel(context.Background())
	client := &fakeProviderClient{afterReserve: cancel}
	completion, err := runner.Run(ctx, createExecution(client))
	if err == nil || completion.State != bookingapi.StateOutcomeUnknown {
		t.Fatalf("completion/error = %+v/%v", completion, err)
	}
	if client.payments != 0 || len(evidence.bookings) != 1 || evidence.bookings[0].BookingUUID != "booking-1" {
		t.Fatalf("payment/evidence = %d/%+v", client.payments, evidence.bookings)
	}
	assertJournalCall(t, journal.calls, journalCall{kind: "evidence", phase: bookingapi.PhaseReserve, attempt: 1, effect: confirmedEffect, code: "reserved"})
}

func TestProviderRunnerPaymentAmbiguityNeverRestartsExternalWork(t *testing.T) {
	runner, journal, evidence := newTestProviderRunner(t)
	client := &fakeProviderClient{paymentErr: mutationError(eversports.MutationPhasePayment, eversports.MutationUnknown, eversports.MutationCodeInvalidResponse, false)}
	completion, err := runner.Run(context.Background(), createExecution(client))
	if err == nil || completion.State != bookingapi.StateOutcomeUnknown {
		t.Fatalf("completion/error = %+v/%v", completion, err)
	}
	if client.reserves != 1 || client.payments != 1 || client.attachments != 0 || client.refreshes != 0 {
		t.Fatalf("calls reserve/pay/attach/refresh = %d/%d/%d/%d", client.reserves, client.payments, client.attachments, client.refreshes)
	}
	if len(evidence.bookings) != 1 || evidence.bookings[0].PaymentState != "unknown" {
		t.Fatalf("reservation evidence = %+v", evidence.bookings)
	}
	assertJournalCall(t, journal.calls, journalCall{kind: "evidence", phase: bookingapi.PhasePayment, attempt: 1, effect: "unknown", code: "invalid_response"})
}

func TestProviderRunnerTrackingFailurePreservesBookingAndDiscardsSession(t *testing.T) {
	runner, _, evidence := newTestProviderRunner(t)
	client := &fakeProviderClient{trackingErr: errors.New("tracking unavailable")}
	completion, err := runner.Run(context.Background(), createExecution(client))
	if err == nil || completion.State != bookingapi.StateSucceeded || completion.Outcome != bookingapi.OutcomeSuccessWithWarning || !completion.DiscardSession {
		t.Fatalf("completion/error = %+v/%v", completion, err)
	}
	if client.tracking != 1 || len(evidence.bookings) != 3 || evidence.bookings[1].PaymentState != "confirmed" {
		t.Fatalf("tracking/evidence = %d/%+v", client.tracking, evidence.bookings)
	}
}

func TestProviderRunnerAttachmentFailurePreservesConfirmedBooking(t *testing.T) {
	runner, journal, evidence := newTestProviderRunner(t)
	client := &fakeProviderClient{attachErr: mutationError(eversports.MutationPhaseMatchAttachment, eversports.MutationUnknown, eversports.MutationCodeHTTPError, false)}
	completion, err := runner.Run(context.Background(), createExecution(client))
	if err == nil || completion.State != bookingapi.StateSucceeded || completion.Outcome != bookingapi.OutcomeSuccessWithWarning || !completion.DiscardSession {
		t.Fatalf("completion/error = %+v/%v", completion, err)
	}
	if client.attachments != 1 || client.tracking != 0 {
		t.Fatalf("attach/tracking calls = %d/%d", client.attachments, client.tracking)
	}
	if len(evidence.bookings) != 2 || evidence.bookings[1].PaymentState != "confirmed" {
		t.Fatalf("confirmed provider evidence = %+v", evidence.bookings)
	}
	assertJournalCall(t, journal.calls, journalCall{kind: "evidence", phase: bookingapi.PhaseMatchAttachment, attempt: 1, effect: "unknown", code: "http_error"})
}

func TestProviderRunnerCancellationClassificationsAndPersistenceBoundary(t *testing.T) {
	tests := []struct {
		name         string
		providerErr  error
		failEvidence bool
		wantState    bookingapi.OperationState
		wantCalls    int
	}{
		{name: "business rejection", providerErr: mutationError(eversports.MutationPhaseCancellation, eversports.MutationNoEffect, eversports.MutationCodeProviderRejected, false), wantState: bookingapi.StateRejectedNoEffect, wantCalls: 1},
		{name: "ambiguous response", providerErr: mutationError(eversports.MutationPhaseCancellation, eversports.MutationUnknown, eversports.MutationCodeInvalidResponse, false), wantState: bookingapi.StateOutcomeUnknown, wantCalls: 1},
		{name: "success evidence failure", failEvidence: true, wantState: bookingapi.StateOutcomeUnknown, wantCalls: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner, journal, _ := newTestProviderRunner(t)
			if tc.failEvidence {
				journal.failEvidence[stepKey(bookingapi.PhaseCancellation, 1)] = errors.New("commit failed")
			}
			client := &fakeProviderClient{cancelErrs: []error{tc.providerErr}}
			completion, err := runner.Run(context.Background(), cancelExecution(client))
			if err == nil || completion.State != tc.wantState {
				t.Fatalf("completion/error = %+v/%v", completion, err)
			}
			if client.cancellations != tc.wantCalls || client.refreshes != 0 {
				t.Fatalf("cancel/refresh calls = %d/%d", client.cancellations, client.refreshes)
			}
		})
	}
}

func TestProviderRunnerCancellationAuthRetryIsJournaledAndBounded(t *testing.T) {
	runner, journal, _ := newTestProviderRunner(t)
	client := &fakeProviderClient{cancelErrs: []error{
		mutationError(eversports.MutationPhaseCancellation, eversports.MutationNoEffect, eversports.MutationCodeAuthenticationExpired, true), nil,
	}}
	completion, err := runner.Run(context.Background(), cancelExecution(client))
	if err != nil || completion.State != bookingapi.StateSucceeded || client.cancellations != 2 || client.refreshes != 1 {
		t.Fatalf("completion/error/calls = %+v/%v/%d/%d", completion, err, client.cancellations, client.refreshes)
	}
	assertJournalCall(t, journal.calls, journalCall{kind: "intent", phase: bookingapi.PhaseCancellation, attempt: 2})
	assertJournalCall(t, journal.calls, journalCall{kind: "evidence", phase: bookingapi.PhaseCancellation, attempt: 2, effect: confirmedEffect, code: "cancelled"})
}

func newTestProviderRunner(t *testing.T) (*ProviderRunner, *fakeStepJournal, *fakeProviderEvidence) {
	t.Helper()
	journal := &fakeStepJournal{failIntent: make(map[string]error), failEvidence: make(map[string]error)}
	evidence := &fakeProviderEvidence{}
	runner, err := NewProviderRunner(journal, evidence, ProviderRunnerConfig{IdentityKey: providerRunnerKey, SportUUID: "sport-1"})
	if err != nil {
		t.Fatal(err)
	}
	return runner, journal, evidence
}

func createExecution(client ProviderClient) Execution {
	return Execution{
		Operation: &storage.Operation{
			OperationID: operation1, Kind: bookingapi.OperationCreate,
			Scope:           bookingapi.Scope{Provider: "eversports", FacilityUUID: "facility-1"},
			AccountIdentity: "account-1", CourtUUID: "court-1",
			StartProvider: "2026-08-10T20:45:00.000+02:00", EndProvider: "2026-08-10T21:30:00.000+02:00",
		},
		ExecutorID: operation2, Session: client,
	}
}

func cancelExecution(client ProviderClient) Execution {
	execution := createExecution(client)
	execution.Operation.Kind = bookingapi.OperationCancel
	execution.Operation.TargetMatchID = "match-1"
	return execution
}

func mutationError(phase eversports.MutationPhase, effect eversports.MutationEffect, code eversports.MutationCode, retry bool) error {
	return &eversports.MutationError{Phase: phase, Effect: effect, Code: code, RetrySafe: retry, Err: errors.New("secret provider body")}
}

func assertJournalCall(t *testing.T, calls []journalCall, want journalCall) {
	t.Helper()
	for _, call := range calls {
		if call == want {
			return
		}
	}
	t.Fatalf("journal calls %+v do not contain %+v", calls, want)
}
