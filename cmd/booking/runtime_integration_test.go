//go:build integration

package main

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/booking/eversports"
	bookingservice "github.com/hutoroff/squash-bot/cmd/booking/service"
	"github.com/hutoroff/squash-bot/cmd/booking/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/config"
	"github.com/hutoroff/squash-bot/internal/testutil"
)

func TestReliableRuntimeStartupFakeProviderSingletonAndShutdown(t *testing.T) {
	if err := testutil.CheckDocker(); err != nil {
		t.Fatalf("booking runtime integration test requires Docker: %v", err)
	}
	ctx := context.Background()
	_, databaseURL, cleanup, err := testutil.SetupEmptyTestDB(ctx)
	if err != nil {
		t.Fatalf("setup test database: %v", err)
	}
	cleanedUp := false
	defer func() {
		if !cleanedUp {
			cleanup()
		}
	}()
	cfg := &config.BookingConfig{
		EversportsFacilityID: "76443", EversportsFacilityUUID: "facility-uuid", EversportsFacilitySlug: "facility-slug",
		EversportsSquashUUID: "b388b6e6-69de-11e8-bdc6-02bd505aa7b2", DatabaseURL: databaseURL,
		IdentityKey: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ServiceKey: "booking.test",
		HTTPResponseWait: time.Second, ExecutionTimeout: 2 * time.Second, ProviderCallTimeout: time.Second,
		FinalizationTimeout: time.Second, AdmissionWait: time.Second, SingletonCheckInterval: 100 * time.Millisecond,
		RetentionAge: 24 * time.Hour, RetentionBatchSize: 10, SessionCapacity: 2, SessionIdleTimeout: time.Minute,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var reserveCalls atomic.Int32
	factory := bookingservice.SessionFactoryFunc(func(context.Context, bookingapi.Credentials) (bookingservice.Session, error) {
		return runtimeFakeProvider{reserveCalls: &reserveCalls}, nil
	})
	runtime, _, err := buildReliableRuntimeWithFactory(ctx, cfg, logger, nil, factory)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	if readiness := runtime.readiness(ctx); !readiness.Ready {
		t.Fatalf("runtime not ready: %+v", readiness)
	}
	ledgerHealth, err := runtime.operations.LedgerHealth(ctx)
	if err != nil {
		t.Fatalf("ledger health: %v", err)
	}
	if ledgerHealth.UnknownOperations != 0 || ledgerHealth.RiskReplacements != 0 || ledgerHealth.OldestUnknownAt != nil {
		t.Fatalf("unexpected initial ledger health: %+v", ledgerHealth)
	}
	if _, _, err := buildReliableRuntime(ctx, cfg, logger, nil); !errors.Is(err, storage.ErrSingletonHeld) {
		t.Fatalf("second runtime error = %v, want ErrSingletonHeld", err)
	}

	identityKey, err := hex.DecodeString(cfg.IdentityKey)
	if err != nil {
		t.Fatal(err)
	}
	create, err := bookingapi.ValidateCreate("018f8d2c-6f7a-7b01-9341-bbbbbbbbbbbb", runtime.scope, identityKey, bookingapi.CreateRequest{
		CourtUUID: "court-1", Start: "2027-01-10T10:00:00+01:00", End: "2027-01-10T10:45:00+01:00",
		Email: "player@example.com", Password: "secret",
	})
	if err != nil {
		t.Fatalf("validate create: %v", err)
	}
	admission, err := runtime.coordinator.SubmitCreate(ctx, create, bookingapi.Credentials{Email: "player@example.com", Password: "secret"})
	if err != nil {
		t.Fatalf("submit create: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for admission.Operation.State != bookingapi.StateSucceeded && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		admission.Operation, err = runtime.operations.Get(ctx, create.OperationID)
		if err != nil {
			t.Fatalf("read operation: %v", err)
		}
	}
	if admission.Operation.State != bookingapi.StateSucceeded || admission.Operation.Outcome != bookingapi.OutcomeSuccess {
		t.Fatalf("fake-provider operation = state %s outcome %s", admission.Operation.State, admission.Operation.Outcome)
	}
	if got := reserveCalls.Load(); got != 1 {
		t.Fatalf("reserve calls = %d, want 1", got)
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(ctx, 5*time.Second)
	if err := runtime.shutdown(shutdownCtx); err != nil {
		cancelShutdown()
		t.Fatalf("shutdown runtime: %v", err)
	}
	cancelShutdown()
	if runtime.pool != nil || runtime.guard != nil {
		t.Fatal("runtime resources remained attached after shutdown")
	}

	restarted, _, err := buildReliableRuntimeWithFactory(ctx, cfg, logger, nil, factory)
	if err != nil {
		t.Fatalf("restart runtime: %v", err)
	}
	replay, err := restarted.coordinator.SubmitCreate(ctx, create, bookingapi.Credentials{Email: "player@example.com", Password: "rotated-secret"})
	if err != nil {
		t.Fatalf("replay after restart: %v", err)
	}
	if replay.Operation.State != bookingapi.StateSucceeded || replay.Disposition != storage.AdmissionReplay {
		t.Fatalf("restart replay = disposition %s state %s", replay.Disposition, replay.Operation.State)
	}
	if got := reserveCalls.Load(); got != 1 {
		t.Fatalf("reserve calls after replay = %d, want 1", got)
	}

	cleanup()
	cleanedUp = true
	select {
	case <-restarted.guard.Lost():
	case <-time.After(3 * time.Second):
		t.Fatal("singleton authority loss was not observed")
	}
	readinessCtx, cancelReadiness := context.WithTimeout(ctx, 2*time.Second)
	readiness := restarted.readiness(readinessCtx)
	cancelReadiness()
	if readiness.Ready || readiness.Database || readiness.Singleton || readiness.Coordinator {
		t.Fatalf("runtime remained ready after database loss: %+v", readiness)
	}
	secondCreate, err := bookingapi.ValidateCreate("018f8d2c-6f7a-7b01-9341-cccccccccccc", restarted.scope, identityKey, bookingapi.CreateRequest{
		CourtUUID: "court-2", Start: "2027-01-10T11:00:00+01:00", End: "2027-01-10T11:45:00+01:00",
		Email: "player@example.com", Password: "secret",
	})
	if err != nil {
		t.Fatalf("validate create after authority loss: %v", err)
	}
	if _, err := restarted.coordinator.SubmitCreate(ctx, secondCreate, bookingapi.Credentials{Email: "player@example.com", Password: "secret"}); !errors.Is(err, bookingservice.ErrCoordinatorClosed) {
		t.Fatalf("submit after authority loss error = %v, want ErrCoordinatorClosed", err)
	}
	if got := reserveCalls.Load(); got != 1 {
		t.Fatalf("reserve calls after database loss = %d, want 1", got)
	}
	shutdownCtx, cancelShutdown = context.WithTimeout(ctx, 5*time.Second)
	if err := restarted.shutdown(shutdownCtx); err != nil {
		cancelShutdown()
		t.Fatalf("shutdown restarted runtime: %v", err)
	}
	cancelShutdown()
}

type runtimeFakeProvider struct {
	reserveCalls *atomic.Int32
}

func (runtimeFakeProvider) EnsureLoggedIn(context.Context) error { return nil }
func (runtimeFakeProvider) Reauthenticate(context.Context) error { return nil }
func (runtimeFakeProvider) ProviderUserID() string               { return "provider-user" }
func (p runtimeFakeProvider) ReserveCourtProviderTime(context.Context, string, string, string, string, string) (*eversports.ReservationResult, error) {
	p.reserveCalls.Add(1)
	return &eversports.ReservationResult{BookingUUID: "booking-uuid", BookingID: 42, PaymentID: 7, Status: "CONFIRMED"}, nil
}
func (runtimeFakeProvider) PayOffline(context.Context, int) error { return nil }
func (runtimeFakeProvider) AttachMatch(context.Context, string) (string, error) {
	return "match-uuid", nil
}
func (runtimeFakeProvider) CompleteBookingTracking(context.Context, int, string) error { return nil }
func (runtimeFakeProvider) CancelMatchOnce(context.Context, string) (*eversports.CancellationResult, error) {
	return nil, errors.New("unexpected cancellation")
}
