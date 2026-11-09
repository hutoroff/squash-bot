package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/models"
)

type captureBookingIntentRepository struct {
	intent    *models.BookingIntent
	operation *models.ManagementBookingOperation
}

func (r *captureBookingIntentRepository) AdmitIntent(_ context.Context, intent *models.BookingIntent) (*models.BookingIntent, bool, error) {
	r.intent = intent
	intent.ID = 1
	return intent, true, nil
}
func (r *captureBookingIntentRepository) AdmitOperation(_ context.Context, operation *models.ManagementBookingOperation) (*models.ManagementBookingOperation, bool, error) {
	r.operation = operation
	return operation, true, nil
}
func (*captureBookingIntentRepository) GetIntentByRequestKey(context.Context, string) (*models.BookingIntent, error) {
	return nil, nil
}
func (*captureBookingIntentRepository) GetIntent(context.Context, int64) (*models.BookingIntent, error) {
	return nil, nil
}
func (*captureBookingIntentRepository) ListOperationsByIntent(context.Context, int64) ([]*models.ManagementBookingOperation, error) {
	return nil, nil
}
func (*captureBookingIntentRepository) ListRecoverableOperations(context.Context, int) ([]*models.ManagementBookingOperation, error) {
	return nil, nil
}
func (*captureBookingIntentRepository) MarkRecoveryAttempt(context.Context, bookingapi.OperationID) error {
	return nil
}
func (*captureBookingIntentRepository) FinalizeIntent(context.Context, int64, models.BookingIntentState) error {
	return nil
}
func (*captureBookingIntentRepository) GetOperation(context.Context, bookingapi.OperationID) (*models.ManagementBookingOperation, error) {
	return nil, nil
}
func (*captureBookingIntentRepository) GetOperationByIntentDedup(context.Context, int64, string) (*models.ManagementBookingOperation, error) {
	return nil, nil
}
func (*captureBookingIntentRepository) ApplyOperationStatus(context.Context, *bookingapi.OperationStatus) (*models.ManagementBookingOperation, bool, error) {
	return nil, false, nil
}

func TestBookingIntentServiceAdmitsNormalizedIntentAndPlannedOperation(t *testing.T) {
	repository := &captureBookingIntentRepository{}
	service, err := NewBookingIntentService(repository)
	if err != nil {
		t.Fatal(err)
	}
	location := time.FixedZone("facility", 3600)
	intent, created, err := service.AdmitIntent(context.Background(), BookingIntentRequest{
		RequestKey: "scheduled:venue-4:2027-01-10:18:00:g1", Action: bookingapi.OperationCreate,
		Source: models.BookingIntentScheduled, Scope: testManagementBookingScope(), GroupID: -100,
		VenueID: 4, GameDate: time.Date(2027, 1, 10, 18, 30, 0, 0, location), GameTime: "18:00", RequestedCount: 2,
	})
	if err != nil || !created {
		t.Fatalf("AdmitIntent() = created %t err %v", created, err)
	}
	if intent.GameDate.Hour() != 0 || intent.Generation != 1 || len(intent.RequestFingerprint) != 64 {
		t.Fatalf("normalized intent = %+v", intent)
	}
	credentialID := int64(9)
	operation, created, err := service.AdmitOperation(context.Background(), BookingOperationRequest{
		IntentID: intent.ID, DedupKey: "create:court-1:g1", Kind: bookingapi.OperationCreate,
		CourtUUID: "court-1", CourtLabel: "1", StartProvider: "2027-01-10T18:00:00+01:00",
		EndProvider: "2027-01-10T18:45:00+01:00", CredentialID: &credentialID,
	})
	if err != nil || !created {
		t.Fatalf("AdmitOperation() = created %t err %v", created, err)
	}
	if _, err := bookingapi.ParseOperationID(string(operation.OperationID)); err != nil {
		t.Fatalf("generated operation ID: %v", err)
	}
	if operation.State != models.ManagementBookingOperationPlanned || operation.RootOperationID != operation.OperationID || len(operation.RequestFingerprint) != 64 {
		t.Fatalf("planned operation = %+v", operation)
	}
	if repository.operation.CredentialID == nil || *repository.operation.CredentialID != credentialID {
		t.Fatalf("credential binding = %+v", repository.operation.CredentialID)
	}
}

func TestBookingIntentServiceRejectsUnsafeOrIncompleteRequests(t *testing.T) {
	service, err := NewBookingIntentService(&captureBookingIntentRepository{})
	if err != nil {
		t.Fatal(err)
	}
	validIntent := BookingIntentRequest{
		RequestKey: "manual:request-1", Action: bookingapi.OperationCreate, Source: models.BookingIntentManual,
		Scope: testManagementBookingScope(), GroupID: -100, VenueID: 4,
		GameDate: time.Date(2027, 1, 10, 0, 0, 0, 0, time.UTC), GameTime: "18:00", RequestedCount: 1,
	}
	for name, mutate := range map[string]func(*BookingIntentRequest){
		"missing stable key": func(r *BookingIntentRequest) { r.RequestKey = "" },
		"unsafe stable key":  func(r *BookingIntentRequest) { r.RequestKey = "manual\nforged" },
		"invalid time":       func(r *BookingIntentRequest) { r.GameTime = "6pm" },
		"missing scope":      func(r *BookingIntentRequest) { r.Scope.ServiceKey = "" },
		"zero count":         func(r *BookingIntentRequest) { r.RequestedCount = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			request := validIntent
			mutate(&request)
			if _, _, err := service.AdmitIntent(context.Background(), request); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	credentialID := int64(1)
	for name, request := range map[string]BookingOperationRequest{
		"missing credential":   {IntentID: 1, DedupKey: "create:1", Kind: bookingapi.OperationCreate, CourtUUID: "court", StartProvider: "start", EndProvider: "end"},
		"cancel with court":    {IntentID: 1, DedupKey: "cancel:1", Kind: bookingapi.OperationCancel, TargetMatchID: "match", CourtUUID: "court", CredentialID: &credentialID},
		"ordinary risk reason": {IntentID: 1, DedupKey: "create:2", Kind: bookingapi.OperationCreate, CourtUUID: "court", StartProvider: "2027-01-10T18:00:00+01:00", EndProvider: "2027-01-10T18:45:00+01:00", CredentialID: &credentialID, RiskReason: "unsafe"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := service.AdmitOperation(context.Background(), request); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

type uncertainBookingIntentRepository struct {
	captureBookingIntentRepository
}

func (r *uncertainBookingIntentRepository) AdmitIntent(_ context.Context, intent *models.BookingIntent) (*models.BookingIntent, bool, error) {
	intent.ID = 2
	r.intent = intent
	return nil, false, errors.New("commit acknowledgement lost")
}
func (r *uncertainBookingIntentRepository) GetIntentByRequestKey(context.Context, string) (*models.BookingIntent, error) {
	return r.intent, nil
}
func (r *uncertainBookingIntentRepository) AdmitOperation(_ context.Context, operation *models.ManagementBookingOperation) (*models.ManagementBookingOperation, bool, error) {
	r.operation = operation
	return nil, false, errors.New("commit acknowledgement lost")
}
func (r *uncertainBookingIntentRepository) GetOperationByIntentDedup(context.Context, int64, string) (*models.ManagementBookingOperation, error) {
	return r.operation, nil
}

func TestBookingIntentServiceReadsSameKeysAfterUncertainAdmission(t *testing.T) {
	repository := &uncertainBookingIntentRepository{}
	service, err := NewBookingIntentService(repository)
	if err != nil {
		t.Fatal(err)
	}
	intent, created, err := service.AdmitIntent(context.Background(), BookingIntentRequest{
		RequestKey: "manual:uncertain", Action: bookingapi.OperationCreate, Source: models.BookingIntentManual,
		Scope: testManagementBookingScope(), GroupID: -1, VenueID: 1,
		GameDate: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), GameTime: "18:00", RequestedCount: 1,
	})
	if err != nil || created || intent.ID != 2 {
		t.Fatalf("uncertain intent readback = intent %+v created %t err %v", intent, created, err)
	}
	credentialID := int64(4)
	operation, created, err := service.AdmitOperation(context.Background(), BookingOperationRequest{
		IntentID: intent.ID, DedupKey: "create:uncertain", Kind: bookingapi.OperationCreate,
		CourtUUID: "court-1", StartProvider: "2027-01-01T18:00:00+01:00",
		EndProvider: "2027-01-01T18:45:00+01:00", CredentialID: &credentialID,
	})
	if err != nil || created || operation == nil {
		t.Fatalf("uncertain operation readback = operation %+v created %t err %v", operation, created, err)
	}
}

func testManagementBookingScope() bookingapi.Scope {
	return bookingapi.Scope{
		ServiceKey: "booking.eversports.primary", Provider: "eversports",
		FacilityID: "76443", FacilityUUID: "facility-uuid", FacilitySlug: "facility-slug", Sport: "squash",
	}
}
