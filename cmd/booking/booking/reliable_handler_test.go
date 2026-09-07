package booking

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	bookingservice "github.com/hutoroff/squash-bot/cmd/booking/service"
	"github.com/hutoroff/squash-bot/cmd/booking/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/jackc/pgx/v5"
)

const (
	reliableOperationID = "018f8d2c-6f7a-7b01-9341-aaaaaaaaaaaa"
	reliableExecutorID  = "018f8d2c-6f7a-7b01-9341-bbbbbbbbbbbb"
)

var reliableIdentityKey = []byte("0123456789abcdef0123456789abcdef")
var reliableScope = bookingapi.Scope{
	ServiceKey: "munich-squash", Provider: "eversports", FacilityID: "76443",
	FacilityUUID: "facility-uuid", FacilitySlug: "facility-slug", Sport: "squash",
}

type fakeReliableCoordinator struct {
	createCalls int
	cancelCalls int
	create      bookingapi.ValidatedCreate
	cancel      bookingapi.ValidatedCancel
	credentials bookingapi.Credentials
	admission   *storage.Admission
	err         error
}

func (c *fakeReliableCoordinator) SubmitCreate(_ context.Context, request bookingapi.ValidatedCreate, credentials bookingapi.Credentials) (*storage.Admission, error) {
	c.createCalls++
	c.create, c.credentials = request, credentials
	return c.admission, c.err
}
func (c *fakeReliableCoordinator) SubmitCancel(_ context.Context, request bookingapi.ValidatedCancel, credentials bookingapi.Credentials) (*storage.Admission, error) {
	c.cancelCalls++
	c.cancel, c.credentials = request, credentials
	return c.admission, c.err
}

type fakeOperationStatusReader struct {
	status    *bookingapi.OperationStatus
	getErr    error
	fromErr   error
	getCalls  int
	fromCalls int
}

func (s *fakeOperationStatusReader) GetStatus(context.Context, bookingapi.OperationID) (*bookingapi.OperationStatus, error) {
	s.getCalls++
	return cloneStatus(s.status), s.getErr
}
func (s *fakeOperationStatusReader) StatusFromOperation(_ context.Context, operation *storage.Operation) (*bookingapi.OperationStatus, error) {
	s.fromCalls++
	if s.status != nil {
		return cloneStatus(s.status), s.fromErr
	}
	return statusForOperation(operation), s.fromErr
}

type fakeReliableReconciler struct {
	status         *bookingapi.OperationStatus
	err            error
	reconcileCalls int
	resolveCalls   int
	operationID    bookingapi.OperationID
	eventKey       bookingapi.OperationID
	credentials    bookingapi.Credentials
	resolution     bookingapi.ResolutionRequest
}

func (r *fakeReliableReconciler) Reconcile(_ context.Context, operationID bookingapi.OperationID, credentials bookingapi.Credentials) (*bookingapi.OperationStatus, error) {
	r.reconcileCalls++
	r.operationID, r.credentials = operationID, credentials
	return cloneStatus(r.status), r.err
}
func (r *fakeReliableReconciler) Resolve(_ context.Context, operationID, eventKey bookingapi.OperationID, request bookingapi.ResolutionRequest) (*bookingapi.OperationStatus, error) {
	r.resolveCalls++
	r.operationID, r.eventKey, r.resolution = operationID, eventKey, request
	return cloneStatus(r.status), r.err
}

type fakeReliableImporter struct {
	result   *bookingservice.ImportResponse
	err      error
	calls    int
	importID bookingapi.OperationID
	request  bookingapi.ImportRequest
}

func (i *fakeReliableImporter) Import(_ context.Context, importID bookingapi.OperationID, request bookingapi.ImportRequest) (*bookingservice.ImportResponse, error) {
	i.calls++
	i.importID, i.request = importID, request
	return i.result, i.err
}

func cloneStatus(status *bookingapi.OperationStatus) *bookingapi.OperationStatus {
	if status == nil {
		return nil
	}
	copy := *status
	return &copy
}

func statusForOperation(operation *storage.Operation) *bookingapi.OperationStatus {
	return &bookingapi.OperationStatus{
		OperationID: string(operation.OperationID), Kind: operation.Kind, State: operation.State,
		Phase: operation.Phase, Outcome: operation.Outcome, Revision: operation.Revision,
		StatusURL: bookingservice.OperationStatusURL(operation.OperationID),
	}
}

func TestReliableCreateRequiresSingleCanonicalOperationKeyBeforeAdmission(t *testing.T) {
	coordinator := &fakeReliableCoordinator{}
	h := newReliableTestHandler(t, coordinator, &fakeOperationStatusReader{})
	srv := serve(h)
	defer srv.Close()

	for _, tc := range []struct {
		name    string
		headers []string
	}{
		{name: "missing"},
		{name: "invalid", headers: []string{"not-a-uuid"}},
		{name: "multiple", headers: []string{reliableOperationID, reliableExecutorID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v2/eversports/matches", strings.NewReader(validReliableCreateBody()))
			for _, value := range tc.headers {
				req.Header.Add("Idempotency-Key", value)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d", resp.StatusCode)
			}
		})
	}
	if coordinator.createCalls != 0 {
		t.Fatalf("invalid keys reached admission %d times", coordinator.createCalls)
	}
}

func TestReliableCreateAcceptedReturnsOperationEnvelopeAndPreservesOffset(t *testing.T) {
	operation := acceptedOperation(bookingapi.OperationCreate)
	coordinator := &fakeReliableCoordinator{admission: &storage.Admission{Disposition: storage.AdmissionAccepted, Operation: operation}}
	statusReader := &fakeOperationStatusReader{status: statusForOperation(operation)}
	h := newReliableTestHandler(t, coordinator, statusReader)
	srv := serve(h)
	defer srv.Close()

	resp := doReliableMutation(t, http.MethodPost, srv.URL+"/api/v2/eversports/matches", validReliableCreateBody())
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted || resp.Header.Get("Location") != bookingservice.OperationStatusURL(operation.OperationID) {
		t.Fatalf("status/location = %d/%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	var status bookingapi.OperationStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.State != bookingapi.StateAccepted || coordinator.createCalls != 1 || coordinator.create.StartProvider != "2026-08-10T20:45:00.000+02:00" {
		t.Fatalf("status/create = %+v/%+v", status, coordinator.create)
	}
	if coordinator.credentials.Email != testEmail || coordinator.credentials.Password != testPassword {
		t.Fatalf("credentials not passed in memory: %+v", coordinator.credentials)
	}
}

func TestReliableCreateTerminalReplayUsesStoredStatus(t *testing.T) {
	operation := acceptedOperation(bookingapi.OperationCreate)
	operation.State, operation.Outcome, operation.Phase = bookingapi.StateSucceeded, bookingapi.OutcomeSuccess, bookingapi.PhaseMatchAttachment
	coordinator := &fakeReliableCoordinator{admission: &storage.Admission{Disposition: storage.AdmissionReplay, Operation: operation}}
	statusReader := &fakeOperationStatusReader{status: statusForOperation(operation)}
	statusReader.status.Evidence = &bookingapi.ProviderEvidence{BookingUUID: "booking-1", PaymentID: 7}
	h := newReliableTestHandler(t, coordinator, statusReader)
	srv := serve(h)
	defer srv.Close()

	resp := doReliableMutation(t, http.MethodPost, srv.URL+"/api/v2/eversports/matches", validReliableCreateBody())
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var status bookingapi.OperationStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Evidence == nil || status.Evidence.BookingUUID != "booking-1" || status.State != bookingapi.StateSucceeded {
		t.Fatalf("status = %+v", status)
	}
}

func TestReliableMutationMapsDurableConflictAndUncertainAdmission(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode bookingapi.ErrorCode
	}{
		{name: "conflict", err: &storage.ConflictError{Code: bookingapi.ErrorBookingConflict}, wantCode: bookingapi.ErrorBookingConflict},
		{name: "uncertain", err: &bookingservice.AdmissionUncertainError{OperationID: reliableOperationID, Err: errors.New("commit acknowledgement lost")}, wantCode: bookingapi.ErrorOperationOutcomeUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newReliableTestHandler(t, &fakeReliableCoordinator{err: tc.err}, &fakeOperationStatusReader{})
			srv := serve(h)
			defer srv.Close()
			resp := doReliableMutation(t, http.MethodPost, srv.URL+"/api/v2/eversports/matches", validReliableCreateBody())
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusConflict {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			var result bookingapi.ErrorResponse
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				t.Fatal(err)
			}
			if result.Code != tc.wantCode || result.OperationID != reliableOperationID {
				t.Fatalf("error response = %+v", result)
			}
		})
	}
}

func TestReliableStatusNeedsNoProviderCredentialsAndMapsUnknownAndExpiry(t *testing.T) {
	operation := acceptedOperation(bookingapi.OperationCreate)
	unknown := statusForOperation(operation)
	unknown.State, unknown.Outcome, unknown.Phase = bookingapi.StateOutcomeUnknown, bookingapi.OutcomeUnknown, bookingapi.PhasePayment
	reader := &fakeOperationStatusReader{status: unknown}
	h := newReliableTestHandler(t, &fakeReliableCoordinator{}, reader)
	srv := serve(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + bookingservice.OperationStatusURL(operation.OperationID))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || reader.getCalls != 1 {
		t.Fatalf("status/calls = %d/%d", resp.StatusCode, reader.getCalls)
	}

	reader.getErr = &bookingservice.ResultExpiredError{Status: unknown}
	resp, err = http.Get(srv.URL + bookingservice.OperationStatusURL(operation.OperationID))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("expired status = %d", resp.StatusCode)
	}
}

func TestReliableCancelAndLegacyMutationGate(t *testing.T) {
	operation := acceptedOperation(bookingapi.OperationCancel)
	coordinator := &fakeReliableCoordinator{admission: &storage.Admission{Disposition: storage.AdmissionAccepted, Operation: operation}}
	h := newReliableTestHandler(t, coordinator, &fakeOperationStatusReader{status: statusForOperation(operation)})
	srv := serve(h)
	defer srv.Close()

	resp := doReliableMutation(t, http.MethodDelete, srv.URL+"/api/v2/eversports/matches/match-1", `{"email":"`+testEmail+`","password":"`+testPassword+`"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted || coordinator.cancelCalls != 1 || coordinator.cancel.MatchID != "match-1" {
		t.Fatalf("v2 cancel status/calls/target = %d/%d/%q", resp.StatusCode, coordinator.cancelCalls, coordinator.cancel.MatchID)
	}

	legacyRequest, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/eversports/matches/match-1", strings.NewReader(`{"email":"`+testEmail+`","password":"`+testPassword+`"}`))
	legacyResponse, err := http.DefaultClient.Do(legacyRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer legacyResponse.Body.Close()
	if legacyResponse.StatusCode != http.StatusConflict {
		t.Fatalf("legacy mutation status = %d", legacyResponse.StatusCode)
	}
	var result bookingapi.ErrorResponse
	if err := json.NewDecoder(legacyResponse.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Code != bookingapi.ErrorContractUpgradeRequired || coordinator.cancelCalls != 1 {
		t.Fatalf("legacy response/calls = %+v/%d", result, coordinator.cancelCalls)
	}

	legacyCreate, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/eversports/matches", strings.NewReader(validReliableCreateBody()))
	legacyCreateResponse, err := http.DefaultClient.Do(legacyCreate)
	if err != nil {
		t.Fatal(err)
	}
	defer legacyCreateResponse.Body.Close()
	if legacyCreateResponse.StatusCode != http.StatusConflict || coordinator.createCalls != 0 {
		t.Fatalf("legacy create status/admission calls = %d/%d", legacyCreateResponse.StatusCode, coordinator.createCalls)
	}
}

func TestReliableCreateRejectsUnknownJSONFieldBeforeAdmission(t *testing.T) {
	coordinator := &fakeReliableCoordinator{}
	h := newReliableTestHandler(t, coordinator, &fakeOperationStatusReader{})
	srv := serve(h)
	defer srv.Close()
	body := strings.TrimSuffix(validReliableCreateBody(), "}") + `,"unexpected":true}`
	resp := doReliableMutation(t, http.MethodPost, srv.URL+"/api/v2/eversports/matches", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || coordinator.createCalls != 0 {
		t.Fatalf("status/admission calls = %d/%d", resp.StatusCode, coordinator.createCalls)
	}
}

func TestReliableStatusNotFoundDoesNotClaimProviderFailure(t *testing.T) {
	reader := &fakeOperationStatusReader{getErr: pgx.ErrNoRows}
	h := newReliableTestHandler(t, &fakeReliableCoordinator{}, reader)
	srv := serve(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/v2/eversports/operations/" + reliableOperationID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var result bookingapi.ErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Code != bookingapi.ErrorOperationNotFound || strings.Contains(strings.ToLower(result.Message), "provider") {
		t.Fatalf("not-found response = %+v", result)
	}
}

func TestReliableReconciliationAndResolutionControls(t *testing.T) {
	status := &bookingapi.OperationStatus{
		OperationID: reliableOperationID, Kind: bookingapi.OperationCreate, State: bookingapi.StateSucceeded,
		Outcome: bookingapi.OutcomeSuccess, Phase: bookingapi.PhaseFinalization,
		StatusURL: bookingservice.OperationStatusURL(reliableOperationID),
	}
	reconciler := &fakeReliableReconciler{status: status}
	h := newReliableTestHandlerWithReconciler(t, &fakeReliableCoordinator{}, &fakeOperationStatusReader{}, reconciler)
	srv := serve(h)
	defer srv.Close()

	reconcileResponse, err := http.Post(srv.URL+"/api/v2/eversports/operations/"+reliableOperationID+"/reconcile", "application/json",
		strings.NewReader(`{"email":"`+testEmail+`","password":"`+testPassword+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer reconcileResponse.Body.Close()
	if reconcileResponse.StatusCode != http.StatusOK || reconciler.reconcileCalls != 1 || reconciler.credentials.Password != testPassword {
		t.Fatalf("reconcile status/calls/credentials = %d/%d/%+v", reconcileResponse.StatusCode, reconciler.reconcileCalls, reconciler.credentials)
	}

	resolutionBody := `{"expectedRevision":3,"matchId":"match-1","email":"` + testEmail + `","password":"` + testPassword + `","actor":"management","reason":"verified exact match"}`
	missingKeyResponse, err := http.Post(srv.URL+"/api/v2/eversports/operations/"+reliableOperationID+"/resolution", "application/json", strings.NewReader(resolutionBody))
	if err != nil {
		t.Fatal(err)
	}
	missingKeyResponse.Body.Close()
	if missingKeyResponse.StatusCode != http.StatusBadRequest || reconciler.resolveCalls != 0 {
		t.Fatalf("keyless resolution status/calls = %d/%d", missingKeyResponse.StatusCode, reconciler.resolveCalls)
	}

	resolutionRequest, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v2/eversports/operations/"+reliableOperationID+"/resolution", strings.NewReader(resolutionBody))
	resolutionRequest.Header.Set("Idempotency-Key", reliableExecutorID)
	resolutionResponse, err := http.DefaultClient.Do(resolutionRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer resolutionResponse.Body.Close()
	if resolutionResponse.StatusCode != http.StatusOK || reconciler.resolveCalls != 1 || reconciler.eventKey != reliableExecutorID || reconciler.resolution.ExpectedRevision != 3 {
		t.Fatalf("resolution status/call = %d/%+v", resolutionResponse.StatusCode, reconciler)
	}
}

func TestReliableReconciliationFailureRemainsUnknown(t *testing.T) {
	reconciler := &fakeReliableReconciler{err: bookingservice.ErrVerificationMismatch}
	h := newReliableTestHandlerWithReconciler(t, &fakeReliableCoordinator{}, &fakeOperationStatusReader{}, reconciler)
	srv := serve(h)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/api/v2/eversports/operations/"+reliableOperationID+"/reconcile", "application/json", strings.NewReader(`{"email":"`+testEmail+`","password":"`+testPassword+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result bookingapi.ErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusConflict || result.Code != bookingapi.ErrorOperationOutcomeUnknown {
		t.Fatalf("reconciliation failure = %d/%+v", resp.StatusCode, result)
	}
}

func TestReliableLegacyImportRequiresKeyAndReturnsCreatedOrReplay(t *testing.T) {
	importer := &fakeReliableImporter{result: &bookingservice.ImportResponse{
		Status:  &bookingapi.ImportStatus{ImportID: reliableOperationID, Imported: true, Revision: 1, CourtUUID: "court-1", ProviderState: "active"},
		Created: true,
	}}
	h := newReliableTestHandlerWithControls(t, &fakeReliableCoordinator{}, &fakeOperationStatusReader{}, &fakeReliableReconciler{}, importer)
	srv := serve(h)
	defer srv.Close()
	body := `{"matchId":"match-1","courtUuid":"court-1","email":"` + testEmail + `","password":"` + testPassword + `","actor":"management","reason":"verified legacy booking"}`

	keyless, err := http.Post(srv.URL+"/api/v2/eversports/imports", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	keyless.Body.Close()
	if keyless.StatusCode != http.StatusBadRequest || importer.calls != 0 {
		t.Fatalf("keyless import status/calls = %d/%d", keyless.StatusCode, importer.calls)
	}

	request, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v2/eversports/imports", strings.NewReader(body))
	request.Header.Set("Idempotency-Key", reliableOperationID)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated || importer.calls != 1 || importer.importID != reliableOperationID || importer.request.Password != testPassword {
		t.Fatalf("import response/call = %d/%+v", response.StatusCode, importer)
	}

	importer.result.Created = false
	replayRequest, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v2/eversports/imports", strings.NewReader(body))
	replayRequest.Header.Set("Idempotency-Key", reliableOperationID)
	replay, err := http.DefaultClient.Do(replayRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Body.Close()
	if replay.StatusCode != http.StatusOK {
		t.Fatalf("import replay status = %d", replay.StatusCode)
	}
}

func newReliableTestHandler(t *testing.T, coordinator ReliableCoordinator, status OperationStatusReader) *Handler {
	t.Helper()
	return newReliableTestHandlerWithReconciler(t, coordinator, status, &fakeReliableReconciler{})
}

func newReliableTestHandlerWithReconciler(t *testing.T, coordinator ReliableCoordinator, status OperationStatusReader, reconciler ReliableReconciler) *Handler {
	t.Helper()
	return newReliableTestHandlerWithControls(t, coordinator, status, reconciler, &fakeReliableImporter{})
}

func newReliableTestHandlerWithControls(t *testing.T, coordinator ReliableCoordinator, status OperationStatusReader, reconciler ReliableReconciler, importer ReliableImporter) *Handler {
	t.Helper()
	h := NewHandler(testLogger, "test-version", reliableScope.FacilityID, reliableScope.FacilityUUID, reliableScope.FacilitySlug)
	if err := h.ConfigureReliableAPI(ReliableAPIConfig{
		Coordinator: coordinator, Status: status, Reconciler: reconciler, Importer: importer,
		Scope: reliableScope, IdentityKey: reliableIdentityKey,
		ResponseWait: time.Millisecond, PollInterval: 100 * time.Microsecond,
	}); err != nil {
		t.Fatal(err)
	}
	return h
}

func acceptedOperation(kind bookingapi.OperationKind) *storage.Operation {
	return &storage.Operation{
		OperationID: reliableOperationID, Kind: kind, State: bookingapi.StateAccepted,
		Outcome: bookingapi.OutcomePending, Phase: bookingapi.PhaseAdmission, Revision: 1,
	}
}

func validReliableCreateBody() string {
	return `{"courtUuid":"court-1","start":"2026-08-10T20:45:00+02:00","end":"2026-08-10T21:30:00+02:00","email":"` + testEmail + `","password":"` + testPassword + `"}`
}

func doReliableMutation(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", reliableOperationID)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
