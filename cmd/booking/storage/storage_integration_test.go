//go:build integration

package storage_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/booking/eversports"
	bookingservice "github.com/hutoroff/squash-bot/cmd/booking/service"
	bookingstorage "github.com/hutoroff/squash-bot/cmd/booking/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	testPool        *pgxpool.Pool
	testDatabaseURL string
)

func TestMain(m *testing.M) {
	if err := testutil.CheckDocker(); err != nil {
		fmt.Fprintf(os.Stderr, "booking storage integration tests require Docker: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()
	pool, databaseURL, cleanup, err := testutil.SetupEmptyTestDB(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup booking storage test DB: %v\n", err)
		os.Exit(1)
	}
	if err := testutil.RunMigrations(databaseURL); err != nil {
		cleanup()
		fmt.Fprintf(os.Stderr, "run management migrations: %v\n", err)
		os.Exit(1)
	}
	if err := bookingstorage.RunMigrations(databaseURL); err != nil {
		cleanup()
		fmt.Fprintf(os.Stderr, "run booking migrations: %v\n", err)
		os.Exit(1)
	}
	testPool, testDatabaseURL = pool, databaseURL
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func TestSingletonGuardExcludesSecondProcessAndReportsConnectionLoss(t *testing.T) {
	ctx := context.Background()
	guard, err := bookingstorage.AcquireSingletonGuard(ctx, testDatabaseURL, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	second, err := bookingstorage.AcquireSingletonGuard(ctx, testDatabaseURL, 20*time.Millisecond)
	if !errors.Is(err, bookingstorage.ErrSingletonHeld) {
		if second != nil {
			_ = second.Close(ctx)
		}
		t.Fatalf("second singleton acquisition error = %v", err)
	}
	if _, err := testPool.Exec(ctx, `SELECT pg_terminate_backend($1)`, guard.BackendPID()); err != nil {
		t.Fatalf("terminate singleton backend: %v", err)
	}
	select {
	case <-guard.Lost():
	case <-time.After(time.Second):
		t.Fatal("singleton guard did not report connection loss")
	}
	if guard.Healthy() {
		t.Fatal("singleton guard remained healthy after connection loss")
	}
	if err := guard.Close(ctx); err != nil {
		t.Fatalf("close lost singleton guard: %v", err)
	}

	replacement, err := bookingstorage.AcquireSingletonGuard(ctx, testDatabaseURL, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("acquire singleton after old connection stopped: %v", err)
	}
	if err := replacement.Close(ctx); err != nil {
		t.Fatalf("close replacement singleton: %v", err)
	}
}

func TestMigrationStreamsCoexistInBothOrders(t *testing.T) {
	ctx := context.Background()
	assertMigrationTables(t, ctx, testPool)
	if err := testutil.RunMigrations(testDatabaseURL); err != nil {
		t.Fatalf("repeat management migrations: %v", err)
	}
	if err := bookingstorage.RunMigrations(testDatabaseURL); err != nil {
		t.Fatalf("repeat booking migrations: %v", err)
	}

	pool, databaseURL, cleanup, err := testutil.SetupEmptyTestDB(ctx)
	if err != nil {
		t.Fatalf("setup reverse-order database: %v", err)
	}
	defer cleanup()
	if err := bookingstorage.RunMigrations(databaseURL); err != nil {
		t.Fatalf("booking-first migrations: %v", err)
	}
	var managementSchemaPresent bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.users') IS NOT NULL`).Scan(&managementSchemaPresent); err != nil {
		t.Fatal(err)
	}
	if managementSchemaPresent {
		t.Fatal("management schema unexpectedly present after booking-only initialization")
	}
	bookingOnlyRepo := bookingstorage.NewOperationRepo(pool)
	request := validatedCreate(t, operation1, "booking-only@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	if _, err := bookingOnlyRepo.AdmitCreate(ctx, request, time.Now().Add(time.Minute), executor1); err != nil {
		t.Fatalf("booking-only operation admission: %v", err)
	}
	if err := testutil.RunMigrations(databaseURL); err != nil {
		t.Fatalf("management-second migrations: %v", err)
	}
	assertMigrationTables(t, ctx, pool)
}

func TestOperationAdmissionIsDurablyIdempotent(t *testing.T) {
	resetBookingTables(t)
	ctx := context.Background()
	repo := bookingstorage.NewOperationRepo(testPool)
	request := validatedCreate(t, operation1, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	deadline := time.Now().Add(time.Minute)

	first, err := repo.AdmitCreate(ctx, request, deadline, executor1)
	if err != nil {
		t.Fatalf("first admission: %v", err)
	}
	if first.Disposition != bookingstorage.AdmissionAccepted {
		t.Fatalf("first disposition = %q", first.Disposition)
	}
	second, err := repo.AdmitCreate(ctx, request, deadline, executor1)
	if err != nil {
		t.Fatalf("replay admission: %v", err)
	}
	if second.Disposition != bookingstorage.AdmissionReplay || second.Operation.OperationID != request.OperationID {
		t.Fatalf("replay = %+v", second)
	}

	changed := validatedCreate(t, operation1, "player@example.com", "court-2", "2026-04-12T08:45:00+02:00", nil)
	_, err = repo.AdmitCreate(ctx, changed, deadline, executor1)
	assertConflict(t, err, bookingapi.ErrorIdempotencyKeyConflict)

	var operations, claims int
	var storedIdentity string
	if err := testPool.QueryRow(ctx, `SELECT identity FROM booking_accounts`).Scan(&storedIdentity); err != nil {
		t.Fatal(err)
	}
	if storedIdentity == "player@example.com" || storedIdentity != request.AccountIdentity {
		t.Fatalf("stored account identity = %q", storedIdentity)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM booking_operations`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM booking_resource_claims`).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if operations != 1 || claims != 1 {
		t.Fatalf("operations/claims = %d/%d, want 1/1", operations, claims)
	}
}

func TestConcurrentSameKeyAdmissionExecutesOnce(t *testing.T) {
	resetBookingTables(t)
	ctx := context.Background()
	repo := bookingstorage.NewOperationRepo(testPool)
	request := validatedCreate(t, operation1, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	start := make(chan struct{})

	type result struct {
		disposition bookingstorage.AdmissionDisposition
		err         error
	}
	results := make(chan result, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			admission, err := repo.AdmitCreate(ctx, request, time.Now().Add(time.Minute), executor1)
			if err != nil {
				results <- result{err: err}
				return
			}
			results <- result{disposition: admission.Disposition}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	accepted, replayed := 0, 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent same-key admission: %v", result.err)
		}
		switch result.disposition {
		case bookingstorage.AdmissionAccepted:
			accepted++
		case bookingstorage.AdmissionReplay:
			replayed++
		}
	}
	if accepted != 1 || replayed != 7 {
		t.Fatalf("accepted/replayed = %d/%d, want 1/7", accepted, replayed)
	}
}

func TestConcurrentAdmissionAllowsOneResourceOwner(t *testing.T) {
	resetBookingTables(t)
	ctx := context.Background()
	repo := bookingstorage.NewOperationRepo(testPool)
	start := make(chan struct{})
	requests := []bookingapi.ValidatedCreate{
		validatedCreate(t, operation1, "one@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil),
		validatedCreate(t, operation2, "two@example.com", "court-1", "2026-04-12T09:00:00+02:00", nil),
	}

	type result struct {
		admission *bookingstorage.Admission
		err       error
	}
	results := make(chan result, len(requests))
	var wg sync.WaitGroup
	for i, request := range requests {
		wg.Add(1)
		go func(i int, request bookingapi.ValidatedCreate) {
			defer wg.Done()
			<-start
			admission, err := repo.AdmitCreate(ctx, request, time.Now().Add(time.Minute), []bookingapi.OperationID{executor1, executor2}[i])
			results <- result{admission: admission, err: err}
		}(i, request)
	}
	close(start)
	wg.Wait()
	close(results)

	accepted, conflicts := 0, 0
	for result := range results {
		if result.err == nil && result.admission.Disposition == bookingstorage.AdmissionAccepted {
			accepted++
			continue
		}
		var conflict *bookingstorage.ConflictError
		if errors.As(result.err, &conflict) && conflict.Code == bookingapi.ErrorBookingConflict {
			conflicts++
			continue
		}
		t.Fatalf("unexpected concurrent admission result: admission=%+v err=%v", result.admission, result.err)
	}
	if accepted != 1 || conflicts != 1 {
		t.Fatalf("accepted/conflicts = %d/%d, want 1/1", accepted, conflicts)
	}
}

func TestMutationIntentCannotFinalizeAsNotDispatched(t *testing.T) {
	resetBookingTables(t)
	ctx := context.Background()
	repo := bookingstorage.NewOperationRepo(testPool)
	request := validatedCreate(t, operation1, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	if _, err := repo.AdmitCreate(ctx, request, time.Now().Add(time.Minute), executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AppendStepIntent(ctx, request.OperationID, bookingapi.PhaseReserve, 1, executor1); err != nil {
		t.Fatal(err)
	}
	_, err := repo.AppendStepIntent(ctx, request.OperationID, bookingapi.PhaseReserve, 2, executor1)
	assertConflict(t, err, bookingapi.ErrorOperationOutcomeUnknown)
	_, err = repo.Finalize(ctx, request.OperationID, executor1, bookingapi.StateNotDispatched, bookingapi.OutcomeNoEffect, bookingapi.PhaseReserve)
	assertConflict(t, err, bookingapi.ErrorOperationOutcomeUnknown)
	if _, err := repo.Finalize(ctx, request.OperationID, executor1, bookingapi.StateOutcomeUnknown, bookingapi.OutcomeUnknown, bookingapi.PhaseReserve); err != nil {
		t.Fatal(err)
	}
	var claimState string
	if err := testPool.QueryRow(ctx, `SELECT state FROM booking_resource_claims WHERE operation_id = $1`, request.OperationID).Scan(&claimState); err != nil {
		t.Fatal(err)
	}
	if claimState != "active" {
		t.Fatalf("unknown operation claim state = %q, want active", claimState)
	}
}

func TestNotDispatchedReleasesClaimsButRetainsOperationTombstone(t *testing.T) {
	resetBookingTables(t)
	ctx := context.Background()
	repo := bookingstorage.NewOperationRepo(testPool)
	request := validatedCreate(t, operation1, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	if _, err := repo.AdmitCreate(ctx, request, time.Now().Add(time.Minute), executor1); err != nil {
		t.Fatal(err)
	}
	terminal, err := repo.Finalize(ctx, request.OperationID, executor1, bookingapi.StateNotDispatched, bookingapi.OutcomeNoEffect, bookingapi.PhaseAdmission)
	if err != nil {
		t.Fatal(err)
	}
	expired, err := repo.MarkResultExpired(ctx, request.OperationID, terminal.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if expired.ResultExpiredAt == nil {
		t.Fatal("terminal operation has no result-expiry marker")
	}
	replay, err := repo.AdmitCreate(ctx, request, time.Now().Add(time.Minute), executor2)
	if err != nil || replay.Disposition != bookingstorage.AdmissionReplay || replay.Operation.ResultExpiredAt == nil {
		t.Fatalf("expired operation replay = %+v, %v", replay, err)
	}
	newRequest := validatedCreate(t, operation2, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	if _, err := repo.AdmitCreate(ctx, newRequest, time.Now().Add(time.Minute), executor2); err != nil {
		t.Fatalf("new operation after no dispatch: %v", err)
	}
}

func TestCancelAdmissionIsIdempotentAndTargetScoped(t *testing.T) {
	resetBookingTables(t)
	ctx := context.Background()
	repo := bookingstorage.NewOperationRepo(testPool)
	evidence := bookingstorage.NewEvidenceRepo(testPool)
	create := validatedCreate(t, operation3, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	if _, err := repo.AdmitCreate(ctx, create, time.Now().Add(time.Minute), executor3); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AppendStepIntent(ctx, create.OperationID, bookingapi.PhaseReserve, 1, executor3); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AppendStepEvidence(ctx, create.OperationID, bookingapi.PhaseReserve, 1, "confirmed", "reserved", struct{}{}, executor3); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AppendStepIntent(ctx, create.OperationID, bookingapi.PhasePayment, 1, executor3); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AppendStepEvidence(ctx, create.OperationID, bookingapi.PhasePayment, 1, "confirmed", "paid", struct{}{}, executor3); err != nil {
		t.Fatal(err)
	}
	bookingID, paymentID := int64(42), int64(7)
	if _, err := evidence.UpsertProviderBooking(ctx, bookingstorage.ProviderBooking{
		CreateOperationID: create.OperationID, Provider: create.Scope.Provider, FacilityUUID: create.Scope.FacilityUUID,
		BookingUUID: "booking-1", BookingID: &bookingID, PaymentID: &paymentID, MatchID: "match-1",
		PaymentState: "confirmed", BookingState: "confirmed",
	}, executor3); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Finalize(ctx, create.OperationID, executor3, bookingapi.StateSucceeded, bookingapi.OutcomeSuccess, bookingapi.PhaseFinalization); err != nil {
		t.Fatal(err)
	}
	request, err := bookingapi.ValidateCancel(operation1, testScope, identityKey, "match-1", bookingapi.CancelRequest{
		Email: "player@example.com", Password: "test-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := repo.AdmitCancel(ctx, request, time.Now().Add(time.Minute), executor1)
	if err != nil || first.Disposition != bookingstorage.AdmissionAccepted {
		t.Fatalf("first cancel admission = %+v, %v", first, err)
	}
	replay, err := repo.AdmitCancel(ctx, request, time.Now().Add(time.Minute), executor1)
	if err != nil || replay.Disposition != bookingstorage.AdmissionReplay {
		t.Fatalf("cancel replay = %+v, %v", replay, err)
	}
	other, err := bookingapi.ValidateCancel(operation2, testScope, identityKey, "match-1", bookingapi.CancelRequest{
		Email: "other@example.com", Password: "test-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.AdmitCancel(ctx, other, time.Now().Add(time.Minute), executor2)
	assertConflict(t, err, bookingapi.ErrorAccountBlocked)
}

func TestCancelAdmissionRejectsTargetAbsentFromLedger(t *testing.T) {
	resetBookingTables(t)
	request, err := bookingapi.ValidateCancel(operation1, testScope, identityKey, "unknown-match", bookingapi.CancelRequest{
		Email: "player@example.com", Password: "test-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = bookingstorage.NewOperationRepo(testPool).AdmitCancel(context.Background(), request, time.Now().Add(time.Minute), executor1)
	assertConflict(t, err, bookingapi.ErrorBookingConflict)
}

func TestConfirmedCancellationReleasesKnownOccupancyAtomically(t *testing.T) {
	resetBookingTables(t)
	ctx := context.Background()
	operations := bookingstorage.NewOperationRepo(testPool)
	evidence := bookingstorage.NewEvidenceRepo(testPool)
	create := validatedCreate(t, operation1, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	if _, err := operations.AdmitCreate(ctx, create, time.Now().Add(time.Minute), executor1); err != nil {
		t.Fatal(err)
	}
	_, err := operations.Finalize(ctx, create.OperationID, executor1, bookingapi.StateSucceeded, bookingapi.OutcomeSuccess, bookingapi.PhaseFinalization)
	assertConflict(t, err, bookingapi.ErrorOperationOutcomeUnknown)
	if _, err := operations.AppendStepIntent(ctx, create.OperationID, bookingapi.PhaseReserve, 1, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepEvidence(ctx, create.OperationID, bookingapi.PhaseReserve, 1, "confirmed", "reserved", map[string]any{"booking_uuid": "booking-1"}, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepIntent(ctx, create.OperationID, bookingapi.PhasePayment, 1, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepEvidence(ctx, create.OperationID, bookingapi.PhasePayment, 1, "confirmed", "paid", map[string]any{"payment_id": 7}, executor1); err != nil {
		t.Fatal(err)
	}
	bookingID, paymentID := int64(42), int64(7)
	if _, err := evidence.UpsertProviderBooking(ctx, bookingstorage.ProviderBooking{
		CreateOperationID: create.OperationID,
		Provider:          create.Scope.Provider,
		FacilityUUID:      create.Scope.FacilityUUID,
		BookingUUID:       "booking-1",
		BookingID:         &bookingID,
		PaymentID:         &paymentID,
		MatchID:           "match-1",
		PaymentState:      "confirmed",
		BookingState:      "confirmed",
	}, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.Finalize(ctx, create.OperationID, executor1, bookingapi.StateSucceeded, bookingapi.OutcomeSuccess, bookingapi.PhaseFinalization); err != nil {
		t.Fatal(err)
	}
	cancel, err := bookingapi.ValidateCancel(operation2, testScope, identityKey, "match-1", bookingapi.CancelRequest{
		Email: "player@example.com", Password: "test-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AdmitCancel(ctx, cancel, time.Now().Add(time.Minute), executor2); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepIntent(ctx, cancel.OperationID, bookingapi.PhaseCancellation, 1, executor2); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepEvidence(ctx, cancel.OperationID, bookingapi.PhaseCancellation, 1, "confirmed", "cancelled", map[string]any{"match_id": "match-1"}, executor2); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.Finalize(ctx, cancel.OperationID, executor2, bookingapi.StateSucceeded, bookingapi.OutcomeSuccess, bookingapi.PhaseFinalization); err != nil {
		t.Fatal(err)
	}

	var claimState, bookingState, cancellationOperationID string
	if err := testPool.QueryRow(ctx, `
		SELECT c.state, b.booking_state, b.cancellation_operation_id::text
		FROM booking_resource_claims c
		JOIN booking_provider_bookings b ON b.create_operation_id = c.operation_id
		WHERE c.operation_id = $1 AND c.claim_type = 'occupancy'`, create.OperationID,
	).Scan(&claimState, &bookingState, &cancellationOperationID); err != nil {
		t.Fatal(err)
	}
	if claimState != "released" || bookingState != "cancelled" || cancellationOperationID != operation2 {
		t.Fatalf("claim/booking/cancellation = %q/%q/%q", claimState, bookingState, cancellationOperationID)
	}
	resolvedCancel, err := bookingapi.ValidateCancel(operation3, testScope, identityKey, "match-1", bookingapi.CancelRequest{
		Email: "player@example.com", Password: "rotated-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := operations.AdmitCancel(ctx, resolvedCancel, time.Now().Add(time.Minute), executor3)
	if err != nil {
		t.Fatalf("resolved cancellation admission: %v", err)
	}
	if resolved.Disposition != bookingstorage.AdmissionResolved || resolved.Operation.State != bookingapi.StateSucceeded || resolved.Operation.ExecutorID != "" {
		t.Fatalf("resolved cancellation = %+v", resolved)
	}

	rebooking := validatedCreate(t, operation4, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	if _, err := operations.AdmitCreate(ctx, rebooking, time.Now().Add(time.Minute), executor3); err != nil {
		t.Fatalf("rebooking after confirmed cancellation: %v", err)
	}
}

func TestUnknownOperationAllowsOneExplicitRiskReplacement(t *testing.T) {
	resetBookingTables(t)
	ctx := context.Background()
	repo := bookingstorage.NewOperationRepo(testPool)
	original := validatedCreate(t, operation1, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	if _, err := repo.AdmitCreate(ctx, original, time.Now().Add(time.Minute), executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Finalize(ctx, original.OperationID, executor1, bookingapi.StateOutcomeUnknown, bookingapi.OutcomeUnknown, bookingapi.PhaseReserve); err != nil {
		t.Fatalf("finalize unknown: %v", err)
	}

	ordinary := validatedCreate(t, operation2, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	_, err := repo.AdmitCreate(ctx, ordinary, time.Now().Add(time.Minute), executor2)
	assertConflict(t, err, bookingapi.ErrorAccountBlocked)

	risk := &bookingapi.RiskAuthorization{
		ReplacesOperationID: operation1,
		AcceptDuplicateRisk: true,
		Reason:              "booking window is closing",
	}
	replacement := validatedCreate(t, operation2, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", risk)
	admission, err := repo.AdmitCreate(ctx, replacement, time.Now().Add(time.Minute), executor2)
	if err != nil {
		t.Fatalf("risk replacement: %v", err)
	}
	if admission.Operation.ParentOperationID != operation1 || admission.Operation.RootOperationID != operation1 {
		t.Fatalf("replacement lineage = parent %q root %q", admission.Operation.ParentOperationID, admission.Operation.RootOperationID)
	}

	secondRisk := validatedCreate(t, operation3, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", risk)
	_, err = repo.AdmitCreate(ctx, secondRisk, time.Now().Add(time.Minute), executor3)
	if err == nil {
		t.Fatal("second risk replacement unexpectedly admitted")
	}
}

func TestConcurrentRiskReplacementAllowsOneChildPerRoot(t *testing.T) {
	resetBookingTables(t)
	ctx := context.Background()
	repo := bookingstorage.NewOperationRepo(testPool)
	original := validatedCreate(t, operation1, "original@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	if _, err := repo.AdmitCreate(ctx, original, time.Now().Add(time.Minute), executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Finalize(ctx, original.OperationID, executor1, bookingapi.StateOutcomeUnknown, bookingapi.OutcomeUnknown, bookingapi.PhaseReserve); err != nil {
		t.Fatal(err)
	}
	risk := &bookingapi.RiskAuthorization{ReplacesOperationID: operation1, AcceptDuplicateRisk: true, Reason: "window closing"}
	requests := []bookingapi.ValidatedCreate{
		validatedCreate(t, operation2, "replacement-one@example.com", "court-1", "2026-04-12T08:45:00+02:00", risk),
		validatedCreate(t, operation3, "replacement-two@example.com", "court-1", "2026-04-12T08:45:00+02:00", risk),
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i, request := range requests {
		wg.Add(1)
		go func(i int, request bookingapi.ValidatedCreate) {
			defer wg.Done()
			<-start
			_, err := repo.AdmitCreate(ctx, request, time.Now().Add(time.Minute), []bookingapi.OperationID{executor2, executor3}[i])
			results <- err
		}(i, request)
	}
	close(start)
	wg.Wait()
	close(results)

	accepted, conflicts := 0, 0
	for err := range results {
		if err == nil {
			accepted++
			continue
		}
		var conflict *bookingstorage.ConflictError
		if errors.As(err, &conflict) && conflict.Code == bookingapi.ErrorBookingConflict {
			conflicts++
			continue
		}
		t.Fatalf("unexpected risk admission error: %v", err)
	}
	if accepted != 1 || conflicts != 1 {
		t.Fatalf("accepted/conflicts = %d/%d, want 1/1", accepted, conflicts)
	}
}

func TestProviderAliasConflictFailsClosed(t *testing.T) {
	resetBookingTables(t)
	ctx := context.Background()
	repo := bookingstorage.NewOperationRepo(testPool)
	first := validatedCreate(t, operation1, "first@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	second := validatedCreate(t, operation2, "second@example.com", "court-2", "2026-04-12T10:00:00+02:00", nil)
	if _, err := repo.AdmitCreate(ctx, first, time.Now().Add(time.Minute), executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AdmitCreate(ctx, second, time.Now().Add(time.Minute), executor2); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetProviderUserAlias(ctx, first.Scope.Provider, first.AccountIdentity, "provider-user-1"); err != nil {
		t.Fatal(err)
	}
	err := repo.SetProviderUserAlias(ctx, second.Scope.Provider, second.AccountIdentity, "provider-user-1")
	assertConflict(t, err, bookingapi.ErrorAccountBlocked)
}

type successfulLedgerProvider struct{}

func (*successfulLedgerProvider) EnsureLoggedIn(context.Context) error { return nil }
func (*successfulLedgerProvider) Reauthenticate(context.Context) error { return nil }
func (*successfulLedgerProvider) ProviderUserID() string               { return "provider-user-1" }
func (*successfulLedgerProvider) ReserveCourtProviderTime(context.Context, string, string, string, string, string) (*eversports.ReservationResult, error) {
	return &eversports.ReservationResult{BookingUUID: "booking-journaled", BookingID: 42, PaymentID: 7, Status: "CONFIRMED"}, nil
}
func (*successfulLedgerProvider) PayOffline(context.Context, int) error { return nil }
func (*successfulLedgerProvider) AttachMatch(context.Context, string) (string, error) {
	return "match-journaled", nil
}
func (*successfulLedgerProvider) CompleteBookingTracking(context.Context, int, string) error {
	return nil
}
func (*successfulLedgerProvider) CancelMatchOnce(context.Context, string) (*eversports.CancellationResult, error) {
	panic("unexpected cancellation")
}

func TestResultCompactionScrubsOnlyUnblockedTerminalEvidence(t *testing.T) {
	resetBookingTables(t)
	ctx := context.Background()
	operations := bookingstorage.NewOperationRepo(testPool)
	request := validatedCreate(t, operation1, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	if _, err := operations.AdmitCreate(ctx, request, time.Now().Add(time.Minute), executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepIntent(ctx, request.OperationID, bookingapi.PhaseLogin, 1, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepEvidence(ctx, request.OperationID, bookingapi.PhaseLogin, 1, "no_effect", "credential_rejected", map[string]string{"detail": "bounded"}, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.Finalize(ctx, request.OperationID, executor1, bookingapi.StateNotDispatched, bookingapi.OutcomeNoEffect, bookingapi.PhaseLogin); err != nil {
		t.Fatal(err)
	}

	active := validatedCreate(t, operation2, "other@example.com", "court-2", "2026-04-12T10:15:00+02:00", nil)
	if _, err := operations.AdmitCreate(ctx, active, time.Now().Add(time.Minute), executor2); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.Finalize(ctx, active.OperationID, executor2, bookingapi.StateOutcomeUnknown, bookingapi.OutcomeUnknown, bookingapi.PhaseReserve); err != nil {
		t.Fatal(err)
	}

	cutoff := time.Now().Add(24 * time.Hour)
	if request.End.After(cutoff) {
		cutoff = request.End.Add(24 * time.Hour)
	}
	compacted, err := operations.CompactEligibleResults(ctx, cutoff, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(compacted) != 1 || compacted[0] != request.OperationID {
		t.Fatalf("compacted operations = %+v", compacted)
	}
	expired, err := operations.Get(ctx, request.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if expired.ResultExpiredAt == nil {
		t.Fatal("compacted operation lacks tombstone marker")
	}
	steps, err := operations.ListSteps(ctx, request.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 || string(steps[1].Evidence) != "{}" || steps[1].Code != "credential_rejected" || steps[1].Effect != "no_effect" {
		t.Fatalf("compacted steps = %+v", steps)
	}
	if replay, err := operations.CompactEligibleResults(ctx, cutoff, 10); err != nil || len(replay) != 0 {
		t.Fatalf("compaction replay = %+v, %v", replay, err)
	}
}

func TestVerifiedLegacyImportIsIdempotentAndSupportsSafeCancellation(t *testing.T) {
	resetBookingTables(t)
	ctx := context.Background()
	imports := bookingstorage.NewImportRepo(testPool)
	operations := bookingstorage.NewOperationRepo(testPool)
	request, err := bookingapi.ValidateImport(operation1, testScope, identityKey, bookingapi.ImportRequest{
		MatchID: "legacy-match", CourtUUID: "court-1", Email: "player@example.com", Password: "password",
		Actor: "management", Reason: "verified legacy booking",
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 4, 12, 8, 45, 0, 0, time.FixedZone("CEST", 2*60*60))
	end := start.Add(45 * time.Minute)
	accepted, err := imports.AdmitVerified(ctx, request, start, end, "2026-04-12T08:45:00.000+02:00", "2026-04-12T09:30:00.000+02:00", "active")
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Disposition != bookingstorage.ImportAccepted || accepted.Import.StartProvider != "2026-04-12T08:45:00.000+02:00" {
		t.Fatalf("accepted import = %+v", accepted)
	}
	replayRequest, err := bookingapi.ValidateImport(operation1, testScope, identityKey, bookingapi.ImportRequest{
		MatchID: "legacy-match", CourtUUID: "court-1", Email: "PLAYER@example.com", Password: "rotated",
		Actor: "management", Reason: "verified legacy booking",
	})
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := imports.AdmitVerified(ctx, replayRequest, start, end, "2026-04-12T08:45:00.000+02:00", "2026-04-12T09:30:00.000+02:00", "active")
	if err != nil || replayed.Disposition != bookingstorage.ImportReplay {
		t.Fatalf("import replay = %+v, %v", replayed, err)
	}

	sameKey := validatedCreate(t, operation1, "other@example.com", "court-2", "2026-04-12T10:15:00+02:00", nil)
	_, err = operations.AdmitCreate(ctx, sameKey, time.Now().Add(time.Minute), executor1)
	assertConflict(t, err, bookingapi.ErrorIdempotencyKeyConflict)

	overlap := validatedCreate(t, operation2, "other@example.com", "court-1", "2026-04-12T09:00:00+02:00", nil)
	_, err = operations.AdmitCreate(ctx, overlap, time.Now().Add(time.Minute), executor1)
	assertConflict(t, err, bookingapi.ErrorBookingConflict)

	cancel, err := bookingapi.ValidateCancel(operation2, testScope, identityKey, "legacy-match", bookingapi.CancelRequest{
		Email: "player@example.com", Password: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AdmitCancel(ctx, cancel, time.Now().Add(time.Minute), executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepIntent(ctx, cancel.OperationID, bookingapi.PhaseCancellation, 1, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepEvidence(ctx, cancel.OperationID, bookingapi.PhaseCancellation, 1, "confirmed", "cancelled", struct{}{}, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.Finalize(ctx, cancel.OperationID, executor1, bookingapi.StateSucceeded, bookingapi.OutcomeSuccess, bookingapi.PhaseFinalization); err != nil {
		t.Fatal(err)
	}
	imported, err := imports.Get(ctx, request.ImportID)
	if err != nil {
		t.Fatal(err)
	}
	if imported.ProviderState != "cancelled" || imported.Revision != 2 {
		t.Fatalf("cancelled import = %+v", imported)
	}
	rebooking := validatedCreate(t, operation3, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	if _, err := operations.AdmitCreate(ctx, rebooking, time.Now().Add(time.Minute), executor2); err != nil {
		t.Fatalf("cancelled import retained occupancy: %v", err)
	}
	operationKeyImport, err := bookingapi.ValidateImport(operation3, testScope, identityKey, bookingapi.ImportRequest{
		MatchID: "other-match", CourtUUID: "court-3", Email: "player@example.com", Password: "password",
		Actor: "management", Reason: "key collision check",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = imports.AdmitVerified(ctx, operationKeyImport, start.Add(3*time.Hour), end.Add(3*time.Hour), "2026-04-12T11:45:00.000+02:00", "2026-04-12T12:30:00.000+02:00", "active")
	assertConflict(t, err, bookingapi.ErrorIdempotencyKeyConflict)
}

func TestVerifiedResolutionAtomicallyRefinesUnknownCreationAndIsIdempotent(t *testing.T) {
	resetBookingTables(t)
	ctx := context.Background()
	operations := bookingstorage.NewOperationRepo(testPool)
	evidence := bookingstorage.NewEvidenceRepo(testPool)
	request := validatedCreate(t, operation1, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	if _, err := operations.AdmitCreate(ctx, request, time.Now().Add(time.Minute), executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepIntent(ctx, request.OperationID, bookingapi.PhaseReserve, 1, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepEvidence(ctx, request.OperationID, bookingapi.PhaseReserve, 1, "unknown", "transport_error", struct{}{}, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := evidence.UpsertProviderBooking(ctx, bookingstorage.ProviderBooking{
		CreateOperationID: request.OperationID, Provider: request.Scope.Provider, FacilityUUID: request.Scope.FacilityUUID,
		MatchID: "match-verified", PaymentState: "unknown", BookingState: "unknown",
	}, executor1); err != nil {
		t.Fatal(err)
	}
	unknown, err := operations.Finalize(ctx, request.OperationID, executor1, bookingapi.StateOutcomeUnknown, bookingapi.OutcomeUnknown, bookingapi.PhaseReserve)
	if err != nil {
		t.Fatal(err)
	}
	resolution := bookingstorage.VerifiedResolution{
		OperationID: request.OperationID, EventKey: operation2, ExpectedRevision: unknown.Revision,
		EventType: "resolution", MatchID: "match-verified", ProviderState: "active",
		Actor: "management", Reason: "credentialed exact match verification",
	}
	resolved, err := evidence.ApplyVerifiedResolution(ctx, resolution)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.State != bookingapi.StateSucceeded || resolved.Revision != unknown.Revision+1 {
		t.Fatalf("resolved operation = %+v", resolved)
	}
	providerBooking, err := evidence.GetProviderBooking(ctx, request.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if providerBooking.MatchID != "match-verified" || providerBooking.BookingState != "confirmed" || providerBooking.PaymentState != "confirmed" {
		t.Fatalf("resolved provider booking = %+v", providerBooking)
	}
	resolution.ProviderState = "cancelled" // Provider observations may change after the keyed decision.
	replayed, err := evidence.ApplyVerifiedResolution(ctx, resolution)
	if err != nil || replayed.Revision != resolved.Revision {
		t.Fatalf("resolution replay = %+v, %v", replayed, err)
	}
	providerBooking, err = evidence.GetProviderBooking(ctx, request.OperationID)
	if err != nil || providerBooking.BookingState != "confirmed" {
		t.Fatalf("resolution replay changed first decision: %+v, %v", providerBooking, err)
	}
	resolution.Actor = "different-actor"
	_, err = evidence.ApplyVerifiedResolution(ctx, resolution)
	assertConflict(t, err, bookingapi.ErrorIdempotencyKeyConflict)

	other := validatedCreate(t, operation3, "player@example.com", "court-2", "2026-04-12T10:15:00+02:00", nil)
	if _, err := operations.AdmitCreate(ctx, other, time.Now().Add(time.Minute), executor2); err != nil {
		t.Fatalf("resolved account remained quarantined: %v", err)
	}
}

func TestVerifiedCancellationResolutionReleasesOnlyKnownBookingClaims(t *testing.T) {
	resetBookingTables(t)
	ctx := context.Background()
	operations := bookingstorage.NewOperationRepo(testPool)
	evidence := bookingstorage.NewEvidenceRepo(testPool)
	create := validatedCreate(t, operation1, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	if _, err := operations.AdmitCreate(ctx, create, time.Now().Add(time.Minute), executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepIntent(ctx, create.OperationID, bookingapi.PhaseReserve, 1, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepEvidence(ctx, create.OperationID, bookingapi.PhaseReserve, 1, "confirmed", "reserved", struct{}{}, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepIntent(ctx, create.OperationID, bookingapi.PhasePayment, 1, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepEvidence(ctx, create.OperationID, bookingapi.PhasePayment, 1, "confirmed", "paid", struct{}{}, executor1); err != nil {
		t.Fatal(err)
	}
	bookingID, paymentID := int64(42), int64(7)
	if _, err := evidence.UpsertProviderBooking(ctx, bookingstorage.ProviderBooking{
		CreateOperationID: create.OperationID, Provider: create.Scope.Provider, FacilityUUID: create.Scope.FacilityUUID,
		BookingUUID: "booking-1", BookingID: &bookingID, PaymentID: &paymentID, MatchID: "match-1",
		PaymentState: "confirmed", BookingState: "confirmed",
	}, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.Finalize(ctx, create.OperationID, executor1, bookingapi.StateSucceeded, bookingapi.OutcomeSuccess, bookingapi.PhaseFinalization); err != nil {
		t.Fatal(err)
	}
	cancelRequest, err := bookingapi.ValidateCancel(operation2, testScope, identityKey, "match-1", bookingapi.CancelRequest{Email: "player@example.com", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AdmitCancel(ctx, cancelRequest, time.Now().Add(time.Minute), executor2); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepIntent(ctx, cancelRequest.OperationID, bookingapi.PhaseCancellation, 1, executor2); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepEvidence(ctx, cancelRequest.OperationID, bookingapi.PhaseCancellation, 1, "unknown", "transport_error", struct{}{}, executor2); err != nil {
		t.Fatal(err)
	}
	unknown, err := operations.Finalize(ctx, cancelRequest.OperationID, executor2, bookingapi.StateOutcomeUnknown, bookingapi.OutcomeUnknown, bookingapi.PhaseCancellation)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := evidence.ApplyVerifiedResolution(ctx, bookingstorage.VerifiedResolution{
		OperationID: cancelRequest.OperationID, EventKey: operation3, ExpectedRevision: unknown.Revision,
		EventType: "reconciliation", MatchID: "match-1", ProviderState: "cancelled",
		Actor: "booking-reconciler", Reason: "credentialed exact-match reconciliation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.State != bookingapi.StateSucceeded {
		t.Fatalf("resolved cancellation = %+v", resolved)
	}
	providerBooking, err := evidence.GetProviderBooking(ctx, create.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if providerBooking.BookingState != "cancelled" || providerBooking.CancellationOperationID != string(cancelRequest.OperationID) {
		t.Fatalf("cancelled provider booking = %+v", providerBooking)
	}
	rebooking := validatedCreate(t, operation4, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	if _, err := operations.AdmitCreate(ctx, rebooking, time.Now().Add(time.Minute), executor3); err != nil {
		t.Fatalf("verified cancellation did not release occupancy: %v", err)
	}
}

func TestRecoveryFinalizesPaidUnfinishedBookingWithoutProviderExecution(t *testing.T) {
	resetBookingTables(t)
	ctx := context.Background()
	operations := bookingstorage.NewOperationRepo(testPool)
	evidence := bookingstorage.NewEvidenceRepo(testPool)
	request := validatedCreate(t, operation1, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	if _, err := operations.AdmitCreate(ctx, request, time.Now().Add(time.Minute), executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepIntent(ctx, request.OperationID, bookingapi.PhaseReserve, 1, executor1); err != nil {
		t.Fatal(err)
	}
	providerEvidence := bookingapi.ProviderEvidence{BookingUUID: "booking-recovered", BookingID: 42, PaymentID: 7, State: "RESERVED"}
	if _, err := operations.AppendStepEvidence(ctx, request.OperationID, bookingapi.PhaseReserve, 1, "confirmed", "reserved", providerEvidence, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepIntent(ctx, request.OperationID, bookingapi.PhasePayment, 1, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepEvidence(ctx, request.OperationID, bookingapi.PhasePayment, 1, "confirmed", "payment_acknowledged", providerEvidence, executor1); err != nil {
		t.Fatal(err)
	}
	bookingID, paymentID := int64(42), int64(7)
	if _, err := evidence.UpsertProviderBooking(ctx, bookingstorage.ProviderBooking{
		CreateOperationID: request.OperationID, Provider: request.Scope.Provider, FacilityUUID: request.Scope.FacilityUUID,
		BookingUUID: "booking-recovered", BookingID: &bookingID, PaymentID: &paymentID,
		BookingState: "confirmed", PaymentState: "confirmed",
	}, executor1); err != nil {
		t.Fatal(err)
	}
	guard, err := bookingstorage.AcquireSingletonGuard(ctx, testDatabaseURL, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close(context.Background())
	recovery, err := bookingservice.NewRecovery(operations, evidence, guard)
	if err != nil {
		t.Fatal(err)
	}
	results, err := recovery.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].State != bookingapi.StateSucceeded || results[0].Outcome != bookingapi.OutcomeSuccessWithWarning {
		t.Fatalf("recovery results = %+v", results)
	}
	operation, err := operations.Get(ctx, request.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.ExecutorID != "" || operation.State != bookingapi.StateSucceeded {
		t.Fatalf("recovered operation = %+v", operation)
	}
}

func TestProviderRunnerEvidenceSatisfiesDurableSuccessFinalization(t *testing.T) {
	resetBookingTables(t)
	ctx := context.Background()
	operations := bookingstorage.NewOperationRepo(testPool)
	evidence := bookingstorage.NewEvidenceRepo(testPool)
	request := validatedCreate(t, operation1, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	admission, err := operations.AdmitCreate(ctx, request, time.Now().Add(time.Minute), executor1)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := bookingservice.NewProviderRunner(operations, evidence, bookingservice.ProviderRunnerConfig{
		IdentityKey: identityKey, SportUUID: "sport-uuid",
	})
	if err != nil {
		t.Fatal(err)
	}
	completion, err := runner.Run(ctx, bookingservice.Execution{
		Operation: admission.Operation, ExecutorID: executor1, Session: &successfulLedgerProvider{},
	})
	if err != nil {
		t.Fatalf("run provider flow: %v", err)
	}
	if _, err := operations.Finalize(ctx, request.OperationID, executor1, completion.State, completion.Outcome, completion.Phase); err != nil {
		t.Fatalf("finalize provider flow: %v", err)
	}
	operation, err := operations.Get(ctx, request.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != bookingapi.StateSucceeded || operation.Outcome != bookingapi.OutcomeSuccess {
		t.Fatalf("operation = %+v", operation)
	}
	providerBooking, err := evidence.GetProviderBooking(ctx, request.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if providerBooking.BookingUUID != "booking-journaled" || providerBooking.MatchID != "match-journaled" || providerBooking.PaymentState != "confirmed" {
		t.Fatalf("provider booking = %+v", providerBooking)
	}
}

func TestJournalDoesNotAuthorizeRetryForGenericNoEffectRejection(t *testing.T) {
	resetBookingTables(t)
	ctx := context.Background()
	operations := bookingstorage.NewOperationRepo(testPool)
	request := validatedCreate(t, operation1, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	if _, err := operations.AdmitCreate(ctx, request, time.Now().Add(time.Minute), executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepIntent(ctx, request.OperationID, bookingapi.PhaseReserve, 1, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepEvidence(ctx, request.OperationID, bookingapi.PhaseReserve, 1, "no_effect", "provider_rejected", struct{}{}, executor1); err != nil {
		t.Fatal(err)
	}
	_, err := operations.AppendStepIntent(ctx, request.OperationID, bookingapi.PhaseLogin, 2, executor1)
	assertConflict(t, err, bookingapi.ErrorOperationOutcomeUnknown)
	_, err = operations.AppendStepIntent(ctx, request.OperationID, bookingapi.PhaseReserve, 2, executor1)
	assertConflict(t, err, bookingapi.ErrorOperationOutcomeUnknown)
}

func TestJournalAllowsOnlyOneAuthenticationRetryAfterMutationNoEffect(t *testing.T) {
	resetBookingTables(t)
	ctx := context.Background()
	operations := bookingstorage.NewOperationRepo(testPool)
	request := validatedCreate(t, operation1, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	if _, err := operations.AdmitCreate(ctx, request, time.Now().Add(time.Minute), executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepIntent(ctx, request.OperationID, bookingapi.PhaseLogin, 1, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepEvidence(ctx, request.OperationID, bookingapi.PhaseLogin, 1, "confirmed", "authenticated", struct{}{}, executor1); err != nil {
		t.Fatal(err)
	}
	_, err := operations.AppendStepIntent(ctx, request.OperationID, bookingapi.PhaseLogin, 2, executor1)
	assertConflict(t, err, bookingapi.ErrorOperationOutcomeUnknown)
	if _, err := operations.AppendStepIntent(ctx, request.OperationID, bookingapi.PhaseReserve, 1, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepEvidence(ctx, request.OperationID, bookingapi.PhaseReserve, 1, "no_effect", "authentication_expired", struct{}{}, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepIntent(ctx, request.OperationID, bookingapi.PhaseLogin, 2, executor1); err != nil {
		t.Fatalf("append authorized authentication retry: %v", err)
	}
	if _, err := operations.AppendStepEvidence(ctx, request.OperationID, bookingapi.PhaseLogin, 2, "confirmed", "authenticated", struct{}{}, executor1); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.AppendStepIntent(ctx, request.OperationID, bookingapi.PhaseReserve, 2, executor1); err != nil {
		t.Fatalf("append authorized reserve retry: %v", err)
	}
	_, err = operations.AppendStepIntent(ctx, request.OperationID, bookingapi.PhaseLogin, 3, executor1)
	assertConflict(t, err, bookingapi.ErrorOperationOutcomeUnknown)
}

func TestStepAndEvidenceRecordsAreImmutableAndIdempotent(t *testing.T) {
	resetBookingTables(t)
	ctx := context.Background()
	operations := bookingstorage.NewOperationRepo(testPool)
	evidence := bookingstorage.NewEvidenceRepo(testPool)
	request := validatedCreate(t, operation1, "player@example.com", "court-1", "2026-04-12T08:45:00+02:00", nil)
	if _, err := operations.AdmitCreate(ctx, request, time.Now().Add(time.Minute), executor1); err != nil {
		t.Fatal(err)
	}
	_, err := operations.AppendStepEvidence(ctx, request.OperationID, bookingapi.PhaseReserve, 1, "confirmed", "reserved", map[string]any{"booking_uuid": "booking-1"}, executor1)
	assertConflict(t, err, bookingapi.ErrorOperationOutcomeUnknown)
	if _, err := operations.AppendStepIntent(ctx, request.OperationID, bookingapi.PhaseReserve, 1, executor1); err != nil {
		t.Fatalf("append intent: %v", err)
	}
	if _, err := operations.AppendStepIntent(ctx, request.OperationID, bookingapi.PhaseReserve, 1, executor1); err != nil {
		t.Fatalf("replay intent: %v", err)
	}
	if _, err := operations.AppendStepEvidence(ctx, request.OperationID, bookingapi.PhaseReserve, 1, "confirmed", "reserved", map[string]any{"booking_uuid": "booking-1"}, executor1); err != nil {
		t.Fatalf("append evidence: %v", err)
	}
	_, err = operations.AppendStepIntent(ctx, request.OperationID, bookingapi.PhaseReserve, 2, executor1)
	assertConflict(t, err, bookingapi.ErrorOperationOutcomeUnknown)
	_, err = operations.AppendStepEvidence(ctx, request.OperationID, bookingapi.PhaseReserve, 1, "confirmed", "reserved", map[string]any{"booking_uuid": "different"}, executor1)
	assertConflict(t, err, bookingapi.ErrorIdempotencyKeyConflict)

	revision := int64(3)
	event := bookingstorage.OperationEvent{
		OperationID:       request.OperationID,
		EventKey:          bookingapi.OperationID(operation2),
		EventType:         "provider_evidence_recorded",
		ExpectedRevision:  &revision,
		ResultingRevision: revision + 1,
		Reason:            "verified fixture",
		Evidence:          []byte(`{"source":"test"}`),
	}
	if _, err := evidence.AppendEvent(ctx, event); err != nil {
		t.Fatalf("append event: %v", err)
	}
	if _, err := evidence.AppendEvent(ctx, event); err != nil {
		t.Fatalf("replay event: %v", err)
	}
	event.Reason = "conflicting reason"
	_, err = evidence.AppendEvent(ctx, event)
	assertConflict(t, err, bookingapi.ErrorIdempotencyKeyConflict)

	if err := evidence.VerifyIdentityKey(ctx, identityKey); err != nil {
		t.Fatal(err)
	}
	if err := evidence.VerifyIdentityKey(ctx, identityKey); err != nil {
		t.Fatal(err)
	}
	if err := evidence.VerifyIdentityKey(ctx, []byte("abcdef0123456789abcdef0123456789")); err == nil {
		t.Fatal("identity key change unexpectedly accepted")
	}
	if err := evidence.PutMetadataOnce(ctx, "ledger_version", "1"); err != nil {
		t.Fatal(err)
	}
	if err := evidence.PutMetadataOnce(ctx, "ledger_version", "2"); err == nil {
		t.Fatal("metadata identity change unexpectedly accepted")
	}

	bookingID, paymentID := int64(42), int64(7)
	providerBooking := bookingstorage.ProviderBooking{
		CreateOperationID: request.OperationID,
		Provider:          request.Scope.Provider,
		FacilityUUID:      request.Scope.FacilityUUID,
		BookingUUID:       "booking-1",
		BookingID:         &bookingID,
		PaymentID:         &paymentID,
		PaymentState:      "confirmed",
		BookingState:      "confirmed",
	}
	beforeEvidence, err := operations.Get(ctx, request.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := evidence.UpsertProviderBooking(ctx, providerBooking, executor1); err != nil {
		t.Fatalf("upsert provider booking: %v", err)
	}
	afterEvidence, err := operations.Get(ctx, request.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if afterEvidence.Revision != beforeEvidence.Revision+1 {
		t.Fatalf("provider evidence revision = %d, want %d", afterEvidence.Revision, beforeEvidence.Revision+1)
	}
	if _, err := evidence.UpsertProviderBooking(ctx, providerBooking, executor1); err != nil {
		t.Fatalf("replay provider booking: %v", err)
	}
	afterReplay, err := operations.Get(ctx, request.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if afterReplay.Revision != afterEvidence.Revision {
		t.Fatalf("provider evidence replay advanced revision from %d to %d", afterEvidence.Revision, afterReplay.Revision)
	}
	_, err = evidence.UpsertProviderBooking(ctx, providerBooking, executor2)
	assertConflict(t, err, bookingapi.ErrorOperationOutcomeUnknown)
	providerBooking.BookingUUID = "contradiction"
	_, err = evidence.UpsertProviderBooking(ctx, providerBooking, executor1)
	assertConflict(t, err, bookingapi.ErrorBookingConflict)
}

func TestIdentityKeyHandling(t *testing.T) {
	key, err := bookingstorage.DecodeIdentityKey("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	marker, err := bookingstorage.IdentityKeyMarker(key)
	if err != nil {
		t.Fatal(err)
	}
	if marker == "" || marker == string(key) {
		t.Fatalf("invalid marker %q", marker)
	}
	if _, err := bookingstorage.DecodeIdentityKey("not-a-key"); err == nil {
		t.Fatal("invalid identity key unexpectedly accepted")
	}
}

func assertMigrationTables(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, table := range []string{"schema_migrations", "booking_schema_migrations", "users", "booking_operations", "booking_imports"} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("expected migration table/object %q", table)
		}
	}
}

func resetBookingTables(t *testing.T) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), `
		TRUNCATE booking_operation_events, booking_provider_bookings,
		         booking_resource_claims, booking_operation_steps,
		         booking_operations, booking_imports, booking_accounts, booking_metadata
		RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("reset booking tables: %v", err)
	}
}

func validatedCreate(t *testing.T, operationID, email, court, start string, risk *bookingapi.RiskAuthorization) bookingapi.ValidatedCreate {
	t.Helper()
	request, err := bookingapi.ValidateCreate(operationID, testScope, identityKey, bookingapi.CreateRequest{
		CourtUUID: court,
		Start:     start,
		End:       mustEnd(t, start),
		Email:     email,
		Password:  "test-password",
		Risk:      risk,
	})
	if err != nil {
		t.Fatalf("validate create: %v", err)
	}
	return request
}

func mustEnd(t *testing.T, start string) string {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, start)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Add(45 * time.Minute).Format(time.RFC3339)
}

func assertConflict(t *testing.T, err error, code bookingapi.ErrorCode) {
	t.Helper()
	var conflict *bookingstorage.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v, want ConflictError", err)
	}
	if conflict.Code != code {
		t.Fatalf("conflict code = %q, want %q", conflict.Code, code)
	}
}

var testScope = bookingapi.Scope{
	ServiceKey:   "booking.eversports.primary",
	Provider:     "eversports",
	FacilityID:   "76443",
	FacilityUUID: "6266968c-b0fd-4115-ad3b-ae225cc880f1",
	FacilitySlug: "squash-house-berlin-03",
	Sport:        "squash",
}

var identityKey = []byte("0123456789abcdef0123456789abcdef")

const (
	operation1 = "0195f8db-7a6f-7c7b-8f71-0f6e0712042d"
	operation2 = "550e8400-e29b-41d4-a716-446655440000"
	operation3 = "6ba7b810-9dad-41d1-80b4-00c04fd430c8"
	operation4 = "7ca8c921-aebe-42e2-91c5-11d15fe541d9"
	executor1  = "018f8d2c-6f7a-7b01-9341-aaaaaaaaaaaa"
	executor2  = "018f8d2c-6f7a-7b01-9341-bbbbbbbbbbbb"
	executor3  = "018f8d2c-6f7a-7b01-9341-cccccccccccc"
)
