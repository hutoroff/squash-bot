package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/models"
)

type riskIntentFake struct {
	parent        *models.ManagementBookingOperation
	intent        *models.BookingIntent
	child         *models.ManagementBookingOperation
	created       bool
	allocations   int
	dedup, reason string
	credentialID  int64
}

func (f *riskIntentFake) GetOperation(context.Context, bookingapi.OperationID) (*models.ManagementBookingOperation, error) {
	return f.parent, nil
}
func (f *riskIntentFake) GetIntent(context.Context, int64) (*models.BookingIntent, error) {
	return f.intent, nil
}
func (f *riskIntentFake) AuthorizeDuplicateRisk(_ context.Context, _ bookingapi.OperationID, _ int64, dedup string, credentialID int64, reason string) (*models.ManagementBookingOperation, bool, error) {
	f.allocations++
	f.dedup = dedup
	f.credentialID = credentialID
	f.reason = reason
	return f.child, f.created, nil
}

type fakeRiskExecutor struct {
	observed, reconciled          *models.ManagementBookingOperation
	observes, reconciles, creates int
}

func (f *fakeRiskExecutor) Observe(context.Context, bookingapi.OperationID) (*models.ManagementBookingOperation, error) {
	f.observes++
	return f.observed, nil
}
func (f *fakeRiskExecutor) Reconcile(context.Context, bookingapi.OperationID, bookingapi.Credentials) (*models.ManagementBookingOperation, error) {
	f.reconciles++
	return f.reconciled, nil
}
func (f *fakeRiskExecutor) ExecuteCreate(_ context.Context, child *models.ManagementBookingOperation, _ bookingapi.Credentials) (*models.ManagementBookingOperation, error) {
	f.creates++
	return child, nil
}

type fakeRiskCredentials map[int64]*DecryptedCredential

func (f fakeRiskCredentials) GetDecryptedByID(_ context.Context, id int64) (*DecryptedCredential, error) {
	cred := f[id]
	if cred == nil {
		return nil, errors.New("missing")
	}
	return cred, nil
}

type riskGroupLookup struct{ group *models.Group }

func (f riskGroupLookup) GetByID(context.Context, int64) (*models.Group, error) { return f.group, nil }

type riskVenueLookup struct{ venue *models.Venue }

func (f riskVenueLookup) GetByID(context.Context, int64) (*models.Venue, error) { return f.venue, nil }

func riskFixture() (*RiskBookingService, *riskIntentFake, *fakeRiskExecutor) {
	credID, gameID := int64(1), int64(7)
	parent := &models.ManagementBookingOperation{OperationID: "aaaaaaaa-aaaa-7aaa-8aaa-aaaaaaaaaaaa", IntentID: 1, Kind: bookingapi.OperationCreate,
		State: models.ManagementBookingOperationOutcomeUnknown, Revision: 2, CredentialID: &credID, RootOperationID: "aaaaaaaa-aaaa-7aaa-8aaa-aaaaaaaaaaaa",
		CourtUUID: "court-1", StartProvider: "2030-01-10T18:00:00.000+00:00", EndProvider: "2030-01-10T18:45:00.000+00:00"}
	intent := &models.BookingIntent{ID: 1, State: models.BookingIntentUnknown, Source: models.BookingIntentManual, Action: bookingapi.OperationCreate,
		GroupID: -100, VenueID: 4, GameID: &gameID, Scope: testManagementBookingScope(), GameDate: time.Date(2030, 1, 10, 0, 0, 0, 0, time.UTC), GameTime: "18:00"}
	store := &riskIntentFake{parent: parent, intent: intent, child: &models.ManagementBookingOperation{OperationID: "bbbbbbbb-bbbb-7bbb-8bbb-bbbbbbbbbbbb", ParentOperationID: parent.OperationID}, created: true}
	exec := &fakeRiskExecutor{observed: parent, reconciled: parent}
	svc := NewRiskBookingService(store, exec, fakeRiskCredentials{1: {ID: 1, VenueID: 4, Login: "original", Password: "secret"}, 2: {ID: 2, VenueID: 4, Login: "chosen", Password: "secret"}},
		riskGroupLookup{&models.Group{ChatID: -100, Timezone: "UTC"}}, riskVenueLookup{&models.Venue{ID: 4, GroupID: -100, AutoBookingEnabled: true}}, testManagementBookingScope(), time.UTC)
	svc.now = func() time.Time { return time.Date(2030, 1, 10, 17, 0, 0, 0, time.UTC) }
	return svc, store, exec
}

func TestRiskPolicyRejectsBeforeAllocationOrDispatch(t *testing.T) {
	for name, change := range map[string]func(*RiskBookingService, *riskIntentFake, *fakeRiskExecutor){
		"active executor": func(_ *RiskBookingService, _ *riskIntentFake, e *fakeRiskExecutor) {
			op := *e.observed
			op.State = models.ManagementBookingOperationRunning
			e.observed = &op
		},
		"reconciled success": func(_ *RiskBookingService, _ *riskIntentFake, e *fakeRiskExecutor) {
			op := *e.reconciled
			op.State = models.ManagementBookingOperationSucceeded
			e.reconciled = &op
		},
		"stale revision": func(_ *RiskBookingService, store *riskIntentFake, _ *fakeRiskExecutor) { store.parent.Revision = 3 },
		"wrong scope": func(_ *RiskBookingService, store *riskIntentFake, _ *fakeRiskExecutor) {
			store.intent.Scope.ServiceKey = "different"
		},
		"original time mismatch": func(_ *RiskBookingService, store *riskIntentFake, _ *fakeRiskExecutor) {
			store.intent.GameTime = "19:00"
		},
		"venue auto booking off": func(s *RiskBookingService, _ *riskIntentFake, _ *fakeRiskExecutor) {
			s.venues.(riskVenueLookup).venue.AutoBookingEnabled = false
		},
		"passed start": func(s *RiskBookingService, _ *riskIntentFake, _ *fakeRiskExecutor) {
			s.now = func() time.Time { return time.Date(2030, 1, 10, 18, 1, 0, 0, time.UTC) }
		},
		"wrong credential venue": func(s *RiskBookingService, _ *riskIntentFake, _ *fakeRiskExecutor) {
			s.credentials = fakeRiskCredentials{1: {ID: 1, VenueID: 4, Login: "original"}, 2: {ID: 2, VenueID: 5, Login: "other"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, store, exec := riskFixture()
			change(s, store, exec)
			if _, _, err := s.AuthorizeAndDispatch(context.Background(), store.parent.OperationID, 2, 2, 42, "I accept possible duplicate"); err == nil {
				t.Fatal("accepted unsafe risk")
			}
			if store.allocations != 0 || exec.creates != 0 {
				t.Fatalf("allocations=%d creates=%d", store.allocations, exec.creates)
			}
		})
	}
}

func TestRiskPolicyAllocatesOneExplicitChildAndDoesNotDispatchReplay(t *testing.T) {
	s, store, exec := riskFixture()
	child, created, err := s.AuthorizeAndDispatch(context.Background(), store.parent.OperationID, 2, 2, 42, "possible duplicate acknowledged")
	if err != nil || !created || child != store.child || exec.observes != 1 || exec.reconciles != 1 || exec.creates != 1 || store.allocations != 1 {
		t.Fatalf("result=%+v created=%t err=%v executor=%+v allocations=%d", child, created, err, exec, store.allocations)
	}
	if store.dedup != "risk:"+string(store.parent.OperationID) || store.credentialID != 2 || store.reason != "owner:42:possible duplicate acknowledged" {
		t.Fatalf("durable child decision: dedup=%q credential=%d reason=%q", store.dedup, store.credentialID, store.reason)
	}
	store.created = false
	if _, created, err := s.AuthorizeAndDispatch(context.Background(), store.parent.OperationID, 2, 2, 42, "possible duplicate acknowledged"); err != nil || created || exec.creates != 1 {
		t.Fatalf("replay created=%t err=%v executions=%d", created, err, exec.creates)
	}
}

func TestRiskWindowPreservesScheduledOpening(t *testing.T) {
	s, store, _ := riskFixture()
	store.intent.Source = models.BookingIntentScheduled
	store.intent.GameID = nil
	venue := s.venues.(riskVenueLookup).venue
	venue.BookingOpensDays = 5
	venue.AutoBookingCourtsCount = 1
	venue.Courts = "court-1"
	venue.GameDays = "4"
	venue.PreferredGameTimes = "18:00"
	s.groups.(riskGroupLookup).group.AutoBookingAllowed = true
	for _, tc := range []struct {
		now  time.Time
		want bool
	}{
		{time.Date(2030, 1, 5, 0, 1, 0, 0, time.UTC), true},
		{time.Date(2030, 1, 5, 0, 5, 0, 0, time.UTC), false},
		{time.Date(2030, 1, 6, 0, 1, 0, 0, time.UTC), false},
	} {
		if got := riskWindowOpen(store.intent, venue, s.groups.(riskGroupLookup).group, store.parent, time.UTC, tc.now); got != tc.want {
			t.Fatalf("window %s got %t", tc.now, got)
		}
	}
	s.groups.(riskGroupLookup).group.AutoBookingAllowed = false
	if riskWindowOpen(store.intent, venue, s.groups.(riskGroupLookup).group, store.parent, time.UTC, time.Date(2030, 1, 5, 0, 1, 0, 0, time.UTC)) {
		t.Fatal("disabled group auto-booking still permits scheduled risk")
	}
}
