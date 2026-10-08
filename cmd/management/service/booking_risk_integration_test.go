//go:build integration

package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/hutoroff/squash-bot/internal/testutil"
)

// This booking-ledger seam returns synthetic read-only observations; mutation
// calls persist a management child status but do not contact any provider.
type riskDBExecutor struct {
	repo               *storage.BookingIntentRepo
	active, reconciled atomic.Bool
	creates            atomic.Int32
	mu                 sync.Mutex
}

func (f *riskDBExecutor) Observe(ctx context.Context, id bookingapi.OperationID) (*models.ManagementBookingOperation, error) {
	op, err := f.repo.GetOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	if f.active.Load() {
		copy := *op
		copy.State = models.ManagementBookingOperationRunning
		return &copy, nil
	}
	return op, nil
}
func (f *riskDBExecutor) Reconcile(ctx context.Context, id bookingapi.OperationID, _ bookingapi.Credentials) (*models.ManagementBookingOperation, error) {
	op, err := f.repo.GetOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	if f.reconciled.Load() {
		copy := *op
		copy.State = models.ManagementBookingOperationSucceeded
		return &copy, nil
	}
	return op, nil
}
func (f *riskDBExecutor) ExecuteCreate(ctx context.Context, child *models.ManagementBookingOperation, credentials bookingapi.Credentials) (*models.ManagementBookingOperation, error) {
	if credentials.Email != "chosen@example.test" {
		return nil, errors.New("risk dispatched with an unchosen account")
	}
	f.creates.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	result, _, err := f.repo.ApplyOperationStatus(ctx, &bookingapi.OperationStatus{OperationID: string(child.OperationID), Kind: bookingapi.OperationCreate, State: bookingapi.StateSucceeded, Phase: bookingapi.PhaseFinalization, Outcome: bookingapi.OutcomeSuccess, Revision: 1, Evidence: &bookingapi.ProviderEvidence{BookingUUID: "child-booking", MatchID: "child-match", State: "ACCEPTED"}})
	return result, err
}

func TestManualRiskPostgresConcurrentAuthorizationPreservesParentAndNotifications(t *testing.T) {
	if err := testutil.CheckDocker(); err != nil {
		t.Fatalf("integration test requires Docker: %v", err)
	}
	ctx := context.Background()
	pool, cleanup, err := testutil.SetupTestDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	const groupID = int64(-880093)
	groups := storage.NewGroupRepo(pool)
	if err := groups.Upsert(ctx, groupID, "Risk Fake", true); err != nil {
		t.Fatal(err)
	}
	if err := groups.SetTimezone(ctx, groupID, "Europe/Berlin"); err != nil {
		t.Fatal(err)
	}
	group, err := groups.GetByID(ctx, groupID)
	if err != nil {
		t.Fatal(err)
	}
	zone := time.UTC
	if group.Timezone != "" {
		zone, err = time.LoadLocation(group.Timezone)
		if err != nil {
			t.Fatal(err)
		}
	}
	venues := storage.NewVenueRepo(pool)
	venue, err := venues.Create(ctx, &models.Venue{GroupID: groupID, Name: "Risk Venue", Courts: "1", GameDays: "4", BookingOpensDays: 5, AutoBookingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	venueID := venue.ID
	date := time.Date(2030, 1, 10, 0, 0, 0, 0, zone)
	start := time.Date(2030, 1, 10, 19, 0, 0, 0, zone)
	game, err := storage.NewGameRepo(pool).Create(ctx, &models.Game{ChatID: groupID, GameDate: start, Courts: "1", CourtsCount: 1, Sport: "squash", PlayersPerCourt: 2, VenueID: &venueID})
	if err != nil {
		t.Fatal(err)
	}
	key, err := NewEncryptor(testHexKey)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := key.Encrypt("synthetic-password")
	if err != nil {
		t.Fatal(err)
	}
	credentialRepo := storage.NewVenueCredentialRepo(pool)
	original, err := credentialRepo.Create(ctx, venueID, "original@example.test", encrypted, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	chosen, err := credentialRepo.Create(ctx, venueID, "chosen@example.test", encrypted, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	credentialService := NewVenueCredentialService(credentialRepo, venues, storage.NewCourtBookingRepo(pool), key)
	repo := storage.NewBookingIntentRepo(pool)
	intents, err := NewBookingIntentService(repo)
	if err != nil {
		t.Fatal(err)
	}
	intent, _, err := intents.AdmitIntent(ctx, BookingIntentRequest{RequestKey: "manual:risk:fixture", Action: bookingapi.OperationCreate, Source: models.BookingIntentManual, Scope: testManagementBookingScope(), GroupID: groupID, VenueID: venueID, GameID: &game.ID, GameDate: date, GameTime: "19:00", RequestedCount: 1, Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	parent, _, err := intents.AdmitOperation(ctx, BookingOperationRequest{IntentID: intent.ID, DedupKey: "create:court-risk:g1:a1", Kind: bookingapi.OperationCreate, CourtUUID: "court-risk", CourtLabel: "1", StartProvider: start.Format("2006-01-02T15:04:05.000Z07:00"), EndProvider: start.Add(45 * time.Minute).Format("2006-01-02T15:04:05.000Z07:00"), CredentialID: &original.ID})
	if err != nil {
		t.Fatal(err)
	}
	parent, _, err = repo.ApplyOperationStatus(ctx, &bookingapi.OperationStatus{OperationID: string(parent.OperationID), Kind: bookingapi.OperationCreate, State: bookingapi.StateOutcomeUnknown, Phase: bookingapi.PhasePayment, Outcome: bookingapi.OutcomeUnknown, Code: bookingapi.ErrorOperationOutcomeUnknown, Revision: 2, Evidence: &bookingapi.ProviderEvidence{BookingUUID: "original-uncertain", State: "PAYMENT_UNKNOWN"}})
	if err != nil {
		t.Fatal(err)
	}
	executor := &riskDBExecutor{repo: repo}
	policy := NewRiskBookingService(intents, executor, credentialService, groups, venues, testManagementBookingScope(), zone)
	policy.now = func() time.Time { return start.Add(-time.Hour) }
	attempt := func() (*models.ManagementBookingOperation, bool, error) {
		return policy.AuthorizeAndDispatch(ctx, parent.OperationID, 2, chosen.ID, 42, "owner acknowledged possible duplicate")
	}
	for _, tc := range []struct {
		name string
		flag *atomic.Bool
	}{{"active original", &executor.active}, {"reconciled confirmed", &executor.reconciled}} {
		t.Run(tc.name, func(t *testing.T) {
			tc.flag.Store(true)
			defer tc.flag.Store(false)
			if _, _, err := attempt(); err == nil {
				t.Fatal("admitted replacement without terminal unresolved original")
			}
		})
	}
	if executor.creates.Load() != 0 {
		t.Fatal("unsafe preflight dispatched")
	}
	const callers = 6
	results := make(chan *models.ManagementBookingOperation, callers)
	created := make(chan bool, callers)
	errorsCh := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			child, newChild, err := attempt()
			if err != nil {
				errorsCh <- err
				return
			}
			results <- child
			created <- newChild
		}()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Fatalf("concurrent risk authorization: %v", err)
	}
	close(results)
	close(created)
	var childID bookingapi.OperationID
	for child := range results {
		if childID == "" {
			childID = child.OperationID
		} else if child.OperationID != childID {
			t.Fatalf("multiple child keys %s %s", childID, child.OperationID)
		}
	}
	createdCount := 0
	for allocated := range created {
		if allocated {
			createdCount++
		}
	}
	if childID == "" || createdCount != 1 || executor.creates.Load() != 1 {
		t.Fatalf("child=%s allocations=%d dispatches=%d", childID, createdCount, executor.creates.Load())
	}
	storedParent, err := repo.GetOperation(ctx, parent.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	child, err := repo.GetOperation(ctx, childID)
	if err != nil {
		t.Fatal(err)
	}
	storedIntent, err := repo.GetIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedParent.State != models.ManagementBookingOperationOutcomeUnknown || storedParent.BookingUUID != "original-uncertain" || storedIntent.State != models.BookingIntentUnknown || child.State != models.ManagementBookingOperationSucceeded || child.BookingUUID != "child-booking" || child.ParentOperationID != parent.OperationID || child.RootOperationID != parent.OperationID || child.CredentialID == nil || *child.CredentialID != chosen.ID || child.CourtUUID != parent.CourtUUID || child.StartProvider != parent.StartProvider || child.EndProvider != parent.EndProvider || child.RiskReason != "owner:42:owner acknowledged possible duplicate" {
		t.Fatalf("separate parent/child or immutable booking scope lost: parent=%+v child=%+v intent=%+v", storedParent, child, storedIntent)
	}
	notices, err := repo.ClaimPendingNotifications(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[bookingapi.OperationID]models.ManagementBookingNotificationKind)
	for _, notice := range notices {
		seen[notice.OperationID] = notice.Kind
	}
	if len(notices) != 2 || seen[parent.OperationID] != models.ManagementBookingNotificationOutcomeUnknown || seen[child.OperationID] != models.ManagementBookingNotificationPossibleDuplicate {
		t.Fatalf("parent/child notifications=%+v", notices)
	}
}
