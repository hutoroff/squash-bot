package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/models"
)

const managementAdmissionReadbackTimeout = 3 * time.Second

var ErrInvalidBookingRequestKey = fmt.Errorf("stable booking request key is invalid")

type BookingIntentRequest struct {
	RequestKey     string
	Action         bookingapi.OperationKind
	Source         models.BookingIntentSource
	Scope          bookingapi.Scope
	GroupID        int64
	VenueID        int64
	GameID         *int64
	GameDate       time.Time
	GameTime       string
	RequestedCount int
	Generation     int
}

type BookingOperationRequest struct {
	IntentID               int64
	DedupKey               string
	Kind                   bookingapi.OperationKind
	CourtUUID              string
	CourtLabel             string
	StartProvider          string
	EndProvider            string
	TargetMatchID          string
	CredentialID           *int64
	ParentOperationID      bookingapi.OperationID
	ParentExpectedRevision int64
	RootOperationID        bookingapi.OperationID
	RiskReason             string
}

type BookingIntentService struct {
	repository BookingIntentRepository
}

func NewBookingIntentService(repository BookingIntentRepository) (*BookingIntentService, error) {
	if repository == nil {
		return nil, fmt.Errorf("management booking intent repository is required")
	}
	return &BookingIntentService{repository: repository}, nil
}

func (s *BookingIntentService) AdmitIntent(ctx context.Context, request BookingIntentRequest) (*models.BookingIntent, bool, error) {
	if err := validateBookingIntentRequest(request); err != nil {
		return nil, false, err
	}
	if request.Generation == 0 {
		request.Generation = 1
	}
	request.GameDate = time.Date(request.GameDate.Year(), request.GameDate.Month(), request.GameDate.Day(), 0, 0, 0, 0, request.GameDate.Location())
	fingerprint, err := fingerprint(request)
	if err != nil {
		return nil, false, err
	}
	candidate := &models.BookingIntent{
		RequestKey: request.RequestKey, RequestFingerprint: fingerprint,
		Action: request.Action, Source: request.Source, Scope: request.Scope,
		GroupID: request.GroupID, VenueID: request.VenueID, GameID: request.GameID,
		GameDate: request.GameDate, GameTime: request.GameTime,
		RequestedCount: request.RequestedCount, Generation: request.Generation,
		State: models.BookingIntentPlanned,
	}
	intent, created, err := s.repository.AdmitIntent(ctx, candidate)
	if err == nil {
		return intent, created, nil
	}
	// A lost commit acknowledgement is not permission to allocate a new business
	// intent. Read the same immutable key before returning the original ambiguity.
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), managementAdmissionReadbackTimeout)
	defer cancel()
	existing, readErr := s.repository.GetIntentByRequestKey(readCtx, candidate.RequestKey)
	if readErr == nil && existing.RequestFingerprint == candidate.RequestFingerprint {
		return existing, false, nil
	}
	return nil, false, err
}

func (s *BookingIntentService) AdmitOperation(ctx context.Context, request BookingOperationRequest) (*models.ManagementBookingOperation, bool, error) {
	if err := validateBookingOperationRequest(request); err != nil {
		return nil, false, err
	}
	if request.Kind == bookingapi.OperationCreate {
		start, startProvider, err := parseManagementProviderTime(request.StartProvider)
		if err != nil {
			return nil, false, fmt.Errorf("invalid management booking operation start: %w", err)
		}
		end, endProvider, err := parseManagementProviderTime(request.EndProvider)
		if err != nil {
			return nil, false, fmt.Errorf("invalid management booking operation end: %w", err)
		}
		if !end.After(start) {
			return nil, false, fmt.Errorf("management booking operation end must be after start")
		}
		request.StartProvider, request.EndProvider = startProvider, endProvider
	}
	operationUUID, err := uuid.NewV7()
	if err != nil {
		return nil, false, fmt.Errorf("generate management booking operation ID: %w", err)
	}
	operationID, err := bookingapi.ParseOperationID(operationUUID.String())
	if err != nil {
		return nil, false, fmt.Errorf("parse generated management booking operation ID: %w", err)
	}
	fingerprint, err := fingerprint(request)
	if err != nil {
		return nil, false, err
	}
	rootOperationID := request.RootOperationID
	if rootOperationID == "" {
		rootOperationID = operationID
	}
	candidate := &models.ManagementBookingOperation{
		OperationID: operationID, IntentID: request.IntentID, DedupKey: request.DedupKey,
		RequestFingerprint: fingerprint, Kind: request.Kind, CourtUUID: request.CourtUUID,
		CourtLabel: request.CourtLabel, StartProvider: request.StartProvider, EndProvider: request.EndProvider,
		TargetMatchID: request.TargetMatchID, CredentialID: request.CredentialID,
		ParentOperationID: request.ParentOperationID, ParentExpectedRevision: request.ParentExpectedRevision,
		RootOperationID: rootOperationID, RiskReason: request.RiskReason,
		State: models.ManagementBookingOperationPlanned, Phase: bookingapi.PhaseAdmission, Outcome: bookingapi.OutcomePending,
	}
	operation, created, err := s.repository.AdmitOperation(ctx, candidate)
	if err == nil {
		return operation, created, nil
	}
	// As with the high-level intent, inspect the same dedup identity after an
	// uncertain write result. Never generate and dispatch a replacement key here.
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), managementAdmissionReadbackTimeout)
	defer cancel()
	existing, readErr := s.repository.GetOperationByIntentDedup(readCtx, candidate.IntentID, candidate.DedupKey)
	if readErr == nil && existing.RequestFingerprint == candidate.RequestFingerprint {
		return existing, false, nil
	}
	return nil, false, err
}

// AuthorizeDuplicateRisk allocates one explicit child for a terminal unknown
// creation. It is not called by ordinary scheduler/manual flows; policy and
// operator authorization must happen before this boundary.
func (s *BookingIntentService) AuthorizeDuplicateRisk(ctx context.Context, parentID bookingapi.OperationID, expectedRevision int64, dedupKey string, credentialID int64, reason string) (*models.ManagementBookingOperation, bool, error) {
	parent, err := s.repository.GetOperation(ctx, parentID)
	if err != nil {
		return nil, false, fmt.Errorf("read duplicate-risk parent: %w", err)
	}
	if expectedRevision <= 0 || parent.Revision != expectedRevision || parent.Kind != bookingapi.OperationCreate || parent.State != models.ManagementBookingOperationOutcomeUnknown || parent.ParentOperationID != "" {
		return nil, false, fmt.Errorf("duplicate-risk parent is not an eligible root unknown creation")
	}
	return s.AdmitOperation(ctx, BookingOperationRequest{
		IntentID: parent.IntentID, DedupKey: dedupKey, Kind: bookingapi.OperationCreate,
		CourtUUID: parent.CourtUUID, CourtLabel: parent.CourtLabel,
		StartProvider: parent.StartProvider, EndProvider: parent.EndProvider, CredentialID: &credentialID,
		ParentOperationID: parent.OperationID, ParentExpectedRevision: expectedRevision,
		RootOperationID: parent.RootOperationID, RiskReason: reason,
	})
}

func (s *BookingIntentService) GetIntent(ctx context.Context, intentID int64) (*models.BookingIntent, error) {
	if intentID <= 0 {
		return nil, fmt.Errorf("management booking intent ID is required")
	}
	return s.repository.GetIntent(ctx, intentID)
}

func (s *BookingIntentService) GetOperation(ctx context.Context, operationID bookingapi.OperationID) (*models.ManagementBookingOperation, error) {
	if operationID == "" {
		return nil, fmt.Errorf("management booking operation ID is required")
	}
	return s.repository.GetOperation(ctx, operationID)
}

func (s *BookingIntentService) ListOperations(ctx context.Context, intentID int64) ([]*models.ManagementBookingOperation, error) {
	if intentID <= 0 {
		return nil, fmt.Errorf("management booking intent ID is required")
	}
	return s.repository.ListOperationsByIntent(ctx, intentID)
}

func (s *BookingIntentService) FinalizeIntent(ctx context.Context, intentID int64, state models.BookingIntentState) error {
	if intentID <= 0 {
		return fmt.Errorf("management booking intent ID is required")
	}
	if state != models.BookingIntentCompleted && state != models.BookingIntentPartial && state != models.BookingIntentUnknown {
		return fmt.Errorf("management booking intent terminal state is invalid")
	}
	return s.repository.FinalizeIntent(ctx, intentID, state)
}

func validateBookingIntentRequest(request BookingIntentRequest) error {
	if !validManagementBookingKey(request.RequestKey) {
		return fmt.Errorf("management booking intent request key is invalid")
	}
	if request.Action != bookingapi.OperationCreate && request.Action != bookingapi.OperationCancel {
		return fmt.Errorf("management booking intent action is invalid")
	}
	switch request.Source {
	case models.BookingIntentScheduled, models.BookingIntentManual, models.BookingIntentRecovery:
	default:
		return fmt.Errorf("management booking intent source is invalid")
	}
	if request.Scope.ServiceKey == "" || request.Scope.Provider == "" || request.Scope.FacilityID == "" || request.Scope.FacilityUUID == "" || request.Scope.FacilitySlug == "" || request.Scope.Sport == "" {
		return fmt.Errorf("management booking intent scope is incomplete")
	}
	if request.GroupID == 0 || request.VenueID <= 0 || request.GameDate.IsZero() || strings.TrimSpace(request.GameTime) == "" {
		return fmt.Errorf("management booking intent group, venue, date, and time are required")
	}
	if _, err := time.Parse("15:04", request.GameTime); err != nil {
		return fmt.Errorf("management booking intent time must use HH:MM")
	}
	if request.RequestedCount <= 0 || request.Generation < 0 {
		return fmt.Errorf("management booking intent count and generation must be positive")
	}
	return nil
}

func validateBookingOperationRequest(request BookingOperationRequest) error {
	if request.IntentID <= 0 || !validManagementBookingKey(request.DedupKey) {
		return fmt.Errorf("management booking operation intent and dedup key are required")
	}
	if request.CredentialID == nil || *request.CredentialID <= 0 {
		return fmt.Errorf("management booking operation credential is required")
	}
	switch request.Kind {
	case bookingapi.OperationCreate:
		if request.CourtUUID == "" || request.StartProvider == "" || request.EndProvider == "" || request.TargetMatchID != "" {
			return fmt.Errorf("management create operation court and provider times are required")
		}
	case bookingapi.OperationCancel:
		if request.TargetMatchID == "" || request.CourtUUID != "" || request.StartProvider != "" || request.EndProvider != "" {
			return fmt.Errorf("management cancel operation exact match target is required")
		}
		if request.ParentOperationID != "" {
			return fmt.Errorf("management cancellation does not support duplicate-risk replacement")
		}
	default:
		return fmt.Errorf("management booking operation kind is invalid")
	}
	if request.ParentOperationID != "" {
		if request.ParentExpectedRevision <= 0 || request.RootOperationID == "" || !validManagementRiskReason(request.RiskReason) {
			return fmt.Errorf("management duplicate-risk operation requires root and reason")
		}
	} else if request.ParentExpectedRevision != 0 || request.RootOperationID != "" || request.RiskReason != "" {
		return fmt.Errorf("management ordinary operation cannot carry duplicate-risk provenance")
	}
	return nil
}

func ValidManagementBookingRequestKey(value string) bool {
	return validManagementBookingKey(value)
}

func validManagementBookingKey(value string) bool {
	if value == "" || len(value) > 200 {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune(":._/-", r)) {
			return false
		}
	}
	return true
}

func validManagementRiskReason(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 500 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func parseManagementProviderTime(value string) (time.Time, string, error) {
	if value == "" || value != strings.TrimSpace(value) || strings.HasSuffix(value, "Z") || strings.HasSuffix(value, "z") {
		return time.Time{}, "", fmt.Errorf("numeric-offset RFC3339 timestamp is required")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Nanosecond()%int(time.Millisecond) != 0 {
		return time.Time{}, "", fmt.Errorf("millisecond-or-coarser RFC3339 timestamp is required")
	}
	return parsed, parsed.Format("2006-01-02T15:04:05.000Z07:00"), nil
}

func fingerprint(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("fingerprint management booking intent: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
