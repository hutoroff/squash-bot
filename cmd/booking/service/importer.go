package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/hutoroff/squash-bot/cmd/booking/eversports"
	"github.com/hutoroff/squash-bot/cmd/booking/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/jackc/pgx/v5"
)

type ImportStore interface {
	Get(context.Context, bookingapi.OperationID) (*storage.Import, error)
	AdmitVerified(context.Context, bookingapi.ValidatedImport, time.Time, time.Time, string, string, string) (*storage.ImportResult, error)
}

type ImportReader interface {
	GetMatchByID(context.Context, string) (*eversports.Booking, error)
	GetCourts(context.Context, string, string, string, string, string, string, string) ([]eversports.Court, error)
	GetSlots(context.Context, string, []string, string) ([]eversports.Slot, error)
}

type ImportServiceConfig struct {
	Scope              bookingapi.Scope
	IdentityKey        []byte
	SportID            string
	SportName          string
	SportUUID          string
	PersistenceTimeout time.Duration
}

type ImportResponse struct {
	Status  *bookingapi.ImportStatus
	Created bool
}

type ImportService struct {
	store              ImportStore
	factory            SessionFactory
	scope              bookingapi.Scope
	identityKey        []byte
	sportID            string
	sportName          string
	sportUUID          string
	persistenceTimeout time.Duration
	gatesMu            sync.Mutex
	gates              map[bookingapi.OperationID]*reconciliationGate
}

func NewImportService(store ImportStore, factory SessionFactory, cfg ImportServiceConfig) (*ImportService, error) {
	if store == nil || factory == nil {
		return nil, fmt.Errorf("booking import store and session factory are required")
	}
	if cfg.PersistenceTimeout == 0 {
		cfg.PersistenceTimeout = 5 * time.Second
	}
	if cfg.PersistenceTimeout < 0 {
		return nil, fmt.Errorf("booking import persistence timeout must be positive")
	}
	if cfg.SportID == "" || cfg.SportName == "" || cfg.SportUUID == "" {
		return nil, fmt.Errorf("booking import provider sport identifiers are required")
	}
	if _, err := bookingapi.ValidateImport("018f8d2c-6f7a-7b01-9341-aaaaaaaaaaaa", cfg.Scope, cfg.IdentityKey, bookingapi.ImportRequest{
		MatchID: "config-match", CourtUUID: "config-court", Email: "config@example.invalid", Password: "config-check",
		Actor: "booking-importer", Reason: "configuration validation",
	}); err != nil {
		return nil, fmt.Errorf("invalid booking import scope or identity configuration: %w", err)
	}
	return &ImportService{
		store: store, factory: factory, scope: cfg.Scope, identityKey: append([]byte(nil), cfg.IdentityKey...),
		sportID: cfg.SportID, sportName: cfg.SportName, sportUUID: cfg.SportUUID,
		persistenceTimeout: cfg.PersistenceTimeout, gates: make(map[bookingapi.OperationID]*reconciliationGate),
	}, nil
}

// Import verifies an exact legacy provider match and records it as imported
// evidence. It performs provider reads only and never invents a create operation.
func (s *ImportService) Import(ctx context.Context, importID bookingapi.OperationID, request bookingapi.ImportRequest) (*ImportResponse, error) {
	validated, err := bookingapi.ValidateImport(string(importID), s.scope, s.identityKey, request)
	if err != nil {
		return nil, err
	}
	release := s.lockImport(importID)
	defer release()
	if existing, getErr := s.store.Get(ctx, importID); getErr == nil {
		if existing.RequestFingerprint != validated.Fingerprint() {
			return nil, &storage.ConflictError{Code: bookingapi.ErrorIdempotencyKeyConflict, ConflictingOperationID: string(importID)}
		}
		return &ImportResponse{Status: importStatus(existing), Created: false}, nil
	} else if !errors.Is(getErr, pgx.ErrNoRows) {
		return nil, fmt.Errorf("read booking import replay: %w", getErr)
	}

	session, err := s.factory.NewSession(ctx, bookingapi.Credentials{Email: request.Email, Password: request.Password})
	if err != nil {
		return nil, fmt.Errorf("construct booking import session: %w", err)
	}
	if closer, ok := session.(io.Closer); ok {
		defer closer.Close()
	}
	reader, ok := session.(ImportReader)
	if !ok {
		return nil, fmt.Errorf("booking import session does not support required reads")
	}
	match, err := reader.GetMatchByID(ctx, validated.MatchID)
	if err != nil {
		return nil, fmt.Errorf("read provider match for import: %w", err)
	}
	providerState, err := verifyImportedMatch(validated, match)
	if err != nil {
		return nil, err
	}
	courts, err := reader.GetCourts(ctx, validated.Scope.FacilityID, validated.Scope.FacilitySlug,
		s.sportID, validated.Scope.Sport, s.sportName, s.sportUUID, match.Start.Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("read provider courts for import: %w", err)
	}
	court, ok := verifiedImportedCourt(validated.CourtUUID, match.Court.Name, courts)
	if !ok {
		return nil, ErrVerificationMismatch
	}
	providerDate := match.Start.Format("2006-01-02")
	slots, err := reader.GetSlots(ctx, validated.Scope.FacilityID, []string{court.ID}, providerDate)
	if err != nil {
		return nil, fmt.Errorf("read provider slots for import: %w", err)
	}
	if !verifiedImportedOwnership(match, court, slots) {
		return nil, ErrVerificationMismatch
	}
	startProvider := match.Start.Format("2006-01-02T15:04:05.000Z07:00")
	endProvider := match.End.Format("2006-01-02T15:04:05.000Z07:00")
	persistenceCtx, cancel := context.WithTimeout(context.Background(), s.persistenceTimeout)
	defer cancel()
	result, err := s.store.AdmitVerified(persistenceCtx, validated, match.Start, match.End, startProvider, endProvider, providerState)
	if err != nil {
		return nil, err
	}
	return &ImportResponse{Status: importStatus(result.Import), Created: result.Disposition == storage.ImportAccepted}, nil
}

func verifyImportedMatch(request bookingapi.ValidatedImport, match *eversports.Booking) (string, error) {
	if match == nil || match.ID != request.MatchID || match.Start.IsZero() || match.End.IsZero() || !match.End.After(match.Start) ||
		(match.Venue.ID != request.Scope.FacilityID && match.Venue.ID != request.Scope.FacilityUUID) ||
		match.Venue.Slug != request.Scope.FacilitySlug || !strings.EqualFold(match.Sport.Slug, request.Scope.Sport) || match.Court.Name == "" {
		return "", ErrVerificationMismatch
	}
	switch strings.ToUpper(match.State) {
	case "ACCEPTED":
		return "active", nil
	case "CANCELLED":
		return "cancelled", nil
	default:
		return "", ErrReconciliationUnresolved
	}
}

func verifiedImportedCourt(courtUUID, matchCourtName string, courts []eversports.Court) (eversports.Court, bool) {
	var verified eversports.Court
	matches := 0
	for _, court := range courts {
		if court.UUID == courtUUID && court.Name == matchCourtName && court.ID != "" {
			verified = court
			matches++
		}
	}
	return verified, matches == 1
}

func verifiedImportedOwnership(match *eversports.Booking, court eversports.Court, slots []eversports.Slot) bool {
	date, start := match.Start.Format("2006-01-02"), match.Start.Format("1504")
	matches := 0
	for _, slot := range slots {
		if slot.Date == date && slot.Start == start && fmt.Sprint(slot.Court) == court.ID &&
			slot.IsUserBookingOwner && slot.Match != nil && slot.Match.UUID == match.ID {
			matches++
		}
	}
	return matches == 1
}

func importStatus(imported *storage.Import) *bookingapi.ImportStatus {
	if imported == nil {
		return nil
	}
	evidenceState := "ACCEPTED"
	if imported.ProviderState == "cancelled" {
		evidenceState = "CANCELLED"
	}
	return &bookingapi.ImportStatus{
		ImportID: string(imported.ImportID), Imported: true, Revision: imported.Revision,
		CourtUUID: imported.CourtUUID, Start: imported.StartProvider, End: imported.EndProvider,
		Evidence:      bookingapi.ProviderEvidence{MatchID: imported.MatchID, State: evidenceState},
		ProviderState: imported.ProviderState, CreatedAt: imported.CreatedAt, UpdatedAt: imported.UpdatedAt,
	}
}

func (s *ImportService) lockImport(importID bookingapi.OperationID) func() {
	s.gatesMu.Lock()
	gate := s.gates[importID]
	if gate == nil {
		gate = &reconciliationGate{}
		s.gates[importID] = gate
	}
	gate.refs++
	s.gatesMu.Unlock()
	gate.mu.Lock()
	return func() {
		gate.mu.Unlock()
		s.gatesMu.Lock()
		gate.refs--
		if gate.refs == 0 {
			delete(s.gates, importID)
		}
		s.gatesMu.Unlock()
	}
}
