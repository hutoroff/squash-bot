package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/booking/eversports"
	"github.com/hutoroff/squash-bot/cmd/booking/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/jackc/pgx/v5"
)

type fakeImportStore struct {
	mu       sync.Mutex
	imports  map[bookingapi.OperationID]*storage.Import
	admitted int
}

func (s *fakeImportStore) Get(_ context.Context, id bookingapi.OperationID) (*storage.Import, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.imports[id]
	if value == nil {
		return nil, pgx.ErrNoRows
	}
	copy := *value
	return &copy, nil
}
func (s *fakeImportStore) AdmitVerified(ctx context.Context, request bookingapi.ValidatedImport, start, end time.Time, startProvider, endProvider, providerState string) (*storage.ImportResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.imports[request.ImportID]; existing != nil {
		return &storage.ImportResult{Disposition: storage.ImportReplay, Import: existing}, nil
	}
	s.admitted++
	value := &storage.Import{
		ImportID: request.ImportID, RequestFingerprint: request.Fingerprint(), Scope: request.Scope,
		CourtUUID: request.CourtUUID, Start: start, End: end, StartProvider: startProvider, EndProvider: endProvider,
		MatchID: request.MatchID, ProviderState: providerState, Actor: request.Actor, Reason: request.Reason,
		Revision: 1, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	s.imports[request.ImportID] = value
	return &storage.ImportResult{Disposition: storage.ImportAccepted, Import: value}, nil
}

type fakeImportReader struct {
	match      *eversports.Booking
	courts     []eversports.Court
	slots      []eversports.Slot
	matchCalls atomic.Int32
	courtCalls atomic.Int32
	slotCalls  atomic.Int32
	afterReads func()
}

func (r *fakeImportReader) GetMatchByID(context.Context, string) (*eversports.Booking, error) {
	r.matchCalls.Add(1)
	return r.match, nil
}
func (r *fakeImportReader) GetCourts(context.Context, string, string, string, string, string, string, string) ([]eversports.Court, error) {
	r.courtCalls.Add(1)
	return r.courts, nil
}
func (r *fakeImportReader) GetSlots(context.Context, string, []string, string) ([]eversports.Slot, error) {
	r.slotCalls.Add(1)
	if r.afterReads != nil {
		r.afterReads()
	}
	return r.slots, nil
}

func TestImportVerifiesProviderMatchAndPreservesLocalOffset(t *testing.T) {
	store, reader, importer := newImportFixture(t)
	response, err := importer.Import(context.Background(), operation1, validImportRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !response.Created || response.Status.Start != "2026-08-10T20:45:00.000+02:00" || response.Status.End != "2026-08-10T21:30:00.000+02:00" {
		t.Fatalf("import response = %+v", response)
	}
	if reader.matchCalls.Load() != 1 || reader.courtCalls.Load() != 1 || reader.slotCalls.Load() != 1 || store.admitted != 1 {
		t.Fatalf("read/admission counts = %d/%d/%d/%d", reader.matchCalls.Load(), reader.courtCalls.Load(), reader.slotCalls.Load(), store.admitted)
	}

	replayRequest := validImportRequest()
	replayRequest.Password = "rotated-password"
	replayed, err := importer.Import(context.Background(), operation1, replayRequest)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Created || reader.matchCalls.Load() != 1 || store.admitted != 1 {
		t.Fatalf("replay performed provider work: %+v reads=%d admissions=%d", replayed, reader.matchCalls.Load(), store.admitted)
	}
	changed := replayRequest
	changed.Reason = "different import provenance"
	if _, err := importer.Import(context.Background(), operation1, changed); err == nil {
		t.Fatal("changed import key reuse was accepted")
	}
	if reader.matchCalls.Load() != 1 {
		t.Fatal("conflicting import key reuse reached provider")
	}
}

func TestImportRejectsUnverifiedCourtAndProviderState(t *testing.T) {
	tests := []struct {
		name  string
		alter func(*fakeImportReader)
		want  error
	}{
		{name: "court mismatch", alter: func(reader *fakeImportReader) { reader.courts[0].UUID = "other" }, want: ErrVerificationMismatch},
		{name: "unknown state", alter: func(reader *fakeImportReader) { reader.match.State = "PENDING_REVIEW" }, want: ErrReconciliationUnresolved},
		{name: "facility mismatch", alter: func(reader *fakeImportReader) { reader.match.Venue.Slug = "other" }, want: ErrVerificationMismatch},
		{name: "another account owns slot", alter: func(reader *fakeImportReader) { reader.slots[0].IsUserBookingOwner = false }, want: ErrVerificationMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, reader, importer := newImportFixture(t)
			test.alter(reader)
			_, err := importer.Import(context.Background(), operation1, validImportRequest())
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if store.admitted != 0 {
				t.Fatal("unverified import was persisted")
			}
		})
	}
}

func TestImportPersistsCompletedReadsAfterCallerCancellation(t *testing.T) {
	store, reader, importer := newImportFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	reader.afterReads = cancel
	if _, err := importer.Import(ctx, operation1, validImportRequest()); err != nil {
		t.Fatal(err)
	}
	if store.admitted != 1 {
		t.Fatal("verified import was not persisted after caller cancellation")
	}
}

func newImportFixture(t *testing.T) (*fakeImportStore, *fakeImportReader, *ImportService) {
	t.Helper()
	location := time.FixedZone("CEST", 2*60*60)
	start := time.Date(2026, 8, 10, 20, 45, 0, 0, location)
	match := &eversports.Booking{ID: "match-1", Start: start, End: start.Add(45 * time.Minute), State: "ACCEPTED"}
	match.Venue.ID, match.Venue.Slug = reconciliationScope.FacilityID, reconciliationScope.FacilitySlug
	match.Sport.Slug, match.Court.Name = reconciliationScope.Sport, "Court 1"
	reader := &fakeImportReader{
		match: match, courts: []eversports.Court{{ID: "1", UUID: "court-1", Name: "Court 1"}},
		slots: []eversports.Slot{{Date: "2026-08-10", Start: "2045", Court: 1, IsUserBookingOwner: true, Match: &eversports.SlotMatch{UUID: "match-1"}}},
	}
	store := &fakeImportStore{imports: make(map[bookingapi.OperationID]*storage.Import)}
	importer, err := NewImportService(store, SessionFactoryFunc(func(context.Context, bookingapi.Credentials) (Session, error) {
		return reader, nil
	}), ImportServiceConfig{
		Scope: reconciliationScope, IdentityKey: reconciliationIdentityKey,
		SportID: "496", SportName: "Squash", SportUUID: "b388b6e6-69de-11e8-bdc6-02bd505aa7b2",
		PersistenceTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, reader, importer
}

func validImportRequest() bookingapi.ImportRequest {
	return bookingapi.ImportRequest{
		MatchID: "match-1", CourtUUID: "court-1", Email: "player@example.com", Password: "password",
		Actor: "management", Reason: "verified legacy booking",
	}
}
