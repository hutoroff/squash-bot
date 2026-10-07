//go:build integration

package storage_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/models"
)

func TestBookingIntentRepoConcurrentAdmissionReplayAndDeletionGuards(t *testing.T) {
	mustTruncate(t)
	ctx := context.Background()
	const groupID = int64(-730001)
	venueID := seedVenue(t, groupID)
	credential, err := storage.NewVenueCredentialRepo(testPool).Create(ctx, venueID, "intent@example.com", "encrypted", 0, 2)
	if err != nil {
		t.Fatalf("create credential: %v", err)
	}
	repository := storage.NewBookingIntentRepo(testPool)
	intentService, err := service.NewBookingIntentService(repository)
	if err != nil {
		t.Fatal(err)
	}
	intentRequest := service.BookingIntentRequest{
		RequestKey: "scheduled:venue-1:2000-01-10:18:00:g1", Action: bookingapi.OperationCreate,
		Source: models.BookingIntentScheduled, Scope: managementBookingScope(), GroupID: groupID,
		VenueID: venueID, GameDate: time.Date(2000, 1, 10, 18, 0, 0, 0, time.FixedZone("facility", 3600)),
		GameTime: "18:00", RequestedCount: 2,
	}

	const callers = 8
	var createdIntents atomic.Int32
	intentIDs := make(chan int64, callers)
	errorsCh := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			intent, created, err := intentService.AdmitIntent(ctx, intentRequest)
			if err != nil {
				errorsCh <- err
				return
			}
			if created {
				createdIntents.Add(1)
			}
			intentIDs <- intent.ID
		}()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Fatalf("concurrent intent admission: %v", err)
	}
	close(intentIDs)
	var intentID int64
	for id := range intentIDs {
		if intentID == 0 {
			intentID = id
		} else if id != intentID {
			t.Fatalf("intent IDs differ: %d and %d", intentID, id)
		}
	}
	if createdIntents.Load() != 1 {
		t.Fatalf("created intents = %d, want 1", createdIntents.Load())
	}
	changedIntent := intentRequest
	changedIntent.RequestedCount = 3
	if _, _, err := intentService.AdmitIntent(ctx, changedIntent); !isBookingIntentConflict(err) {
		t.Fatalf("changed intent error = %v, want conflict", err)
	}

	operationRequest := service.BookingOperationRequest{
		IntentID: intentID, DedupKey: "create:court-1:g1", Kind: bookingapi.OperationCreate,
		CourtUUID: "court-1", CourtLabel: "1", StartProvider: "2000-01-10T18:00:00+01:00",
		EndProvider: "2000-01-10T18:45:00+01:00", CredentialID: &credential.ID,
	}
	var createdOperations atomic.Int32
	operationIDs := make(chan bookingapi.OperationID, callers)
	errorsCh = make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			operation, created, err := intentService.AdmitOperation(ctx, operationRequest)
			if err != nil {
				errorsCh <- err
				return
			}
			if created {
				createdOperations.Add(1)
			}
			operationIDs <- operation.OperationID
		}()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Fatalf("concurrent operation admission: %v", err)
	}
	close(operationIDs)
	var operationID bookingapi.OperationID
	for id := range operationIDs {
		if operationID == "" {
			operationID = id
		} else if id != operationID {
			t.Fatalf("operation IDs differ: %s and %s", operationID, id)
		}
	}
	if createdOperations.Load() != 1 {
		t.Fatalf("created operations = %d, want 1", createdOperations.Load())
	}
	operation, err := repository.GetOperation(ctx, operationID)
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}
	if operation.State != models.ManagementBookingOperationPlanned || operation.RootOperationID != operation.OperationID || operation.StartProvider != "2000-01-10T18:00:00.000+01:00" {
		t.Fatalf("stored operation = %+v", operation)
	}
	acceptedStatus := &bookingapi.OperationStatus{
		OperationID: string(operationID), Kind: bookingapi.OperationCreate, State: bookingapi.StateAccepted,
		Phase: bookingapi.PhaseAdmission, Outcome: bookingapi.OutcomePending, Revision: 0,
	}
	operation, applied, err := repository.ApplyOperationStatus(ctx, acceptedStatus)
	if err != nil || !applied || operation.State != models.ManagementBookingOperationAccepted {
		t.Fatalf("apply accepted status = operation %+v applied %t err %v", operation, applied, err)
	}
	if _, applied, err := repository.ApplyOperationStatus(ctx, acceptedStatus); err != nil || applied {
		t.Fatalf("accepted replay = applied %t err %v", applied, err)
	}
	unknownStatus := &bookingapi.OperationStatus{
		OperationID: string(operationID), Kind: bookingapi.OperationCreate, State: bookingapi.StateOutcomeUnknown,
		Phase: bookingapi.PhasePayment, Outcome: bookingapi.OutcomeUnknown,
		Code: bookingapi.ErrorOperationOutcomeUnknown, Revision: 1,
		Evidence: &bookingapi.ProviderEvidence{BookingUUID: "booking-1", BookingID: 10, PaymentID: 20, State: "PAYMENT_UNKNOWN"},
		Warnings: []string{"payment outcome needs review"},
	}
	operation, applied, err = repository.ApplyOperationStatus(ctx, unknownStatus)
	if err != nil || !applied || operation.State != models.ManagementBookingOperationOutcomeUnknown || operation.BookingUUID != "booking-1" || operation.PaymentID == nil || *operation.PaymentID != 20 {
		t.Fatalf("apply unknown status = operation %+v applied %t err %v", operation, applied, err)
	}
	if stale, applied, err := repository.ApplyOperationStatus(ctx, acceptedStatus); err != nil || applied || stale.Revision != 1 {
		t.Fatalf("stale status = operation %+v applied %t err %v", stale, applied, err)
	}
	changedRevision := *unknownStatus
	changedRevision.Warnings = []string{"changed at same revision"}
	if _, _, err := repository.ApplyOperationStatus(ctx, &changedRevision); err == nil {
		t.Fatal("same revision accepted changed projection")
	}
	var createdRisks atomic.Int32
	riskChildren := make(chan *models.ManagementBookingOperation, callers)
	riskErrors := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			child, created, riskErr := intentService.AuthorizeDuplicateRisk(ctx, operationID, operation.Revision, "risk:court-1:g1", credential.ID, "owner accepted possible duplicate")
			if riskErr != nil {
				riskErrors <- riskErr
				return
			}
			if created {
				createdRisks.Add(1)
			}
			riskChildren <- child
		}()
	}
	wg.Wait()
	close(riskErrors)
	for riskErr := range riskErrors {
		t.Fatalf("concurrent risk child admission: %v", riskErr)
	}
	close(riskChildren)
	var riskChild *models.ManagementBookingOperation
	for child := range riskChildren {
		if riskChild == nil {
			riskChild = child
		} else if child.OperationID != riskChild.OperationID {
			t.Fatalf("multiple risk children: %s and %s", child.OperationID, riskChild.OperationID)
		}
	}
	if createdRisks.Load() != 1 || riskChild == nil || riskChild.ParentOperationID != operationID || riskChild.RootOperationID != operationID {
		t.Fatalf("concurrent risk child = %+v created count %d", riskChild, createdRisks.Load())
	}
	staleRisk := *riskChild
	staleRisk.OperationID = "cccccccc-cccc-7ccc-8ccc-cccccccccccc"
	staleRisk.ParentExpectedRevision++
	staleRisk.DedupKey = "risk:stale-revision"
	staleRisk.RequestFingerprint = "stale"
	if _, _, err := repository.AdmitOperation(ctx, &staleRisk); err == nil {
		t.Fatal("database admitted risk child with stale parent revision")
	}
	if _, _, err := intentService.AuthorizeDuplicateRisk(ctx, operationID, operation.Revision, "risk:court-1:g1:second", credential.ID, "second child must fail"); err == nil {
		t.Fatal("duplicate-risk root admitted a second child")
	}
	if _, _, err := repository.ApplyOperationStatus(ctx, &bookingapi.OperationStatus{
		OperationID: string(riskChild.OperationID), Kind: bookingapi.OperationCreate,
		State: bookingapi.StateRejectedNoEffect, Phase: bookingapi.PhaseReserve,
		Outcome: bookingapi.OutcomeNoEffect, Code: bookingapi.ErrorBookingRejected, Revision: 1,
	}); err != nil {
		t.Fatalf("reject risk child with no effect: %v", err)
	}
	notifications, err := repository.ClaimPendingNotifications(ctx, 10)
	if err != nil || len(notifications) != 3 {
		t.Fatalf("claimed notifications = %+v err = %v", notifications, err)
	}
	if replay, err := repository.ClaimPendingNotifications(ctx, 10); err != nil || len(replay) != 0 {
		t.Fatalf("reclaimed notifications = %+v err = %v", replay, err)
	}
	for _, notification := range notifications {
		if err := repository.MarkNotificationDelivered(ctx, notification.ID); err != nil {
			t.Fatalf("mark notification delivered: %v", err)
		}
	}
	metrics, err := repository.GetMetrics(ctx)
	if err != nil || metrics.OutcomeUnknown != 1 || metrics.RejectedNoEffect != 1 || metrics.RiskAuthorized != 1 || metrics.PendingNotices != 0 {
		t.Fatalf("booking metrics = %+v err = %v", metrics, err)
	}
	storedIntent, err := repository.GetIntentByRequestKey(ctx, intentRequest.RequestKey)
	if err != nil {
		t.Fatalf("GetIntentByRequestKey: %v", err)
	}
	if storedIntent.State != models.BookingIntentUnknown || storedIntent.GameDate.Hour() != 0 {
		t.Fatalf("stored intent = %+v", storedIntent)
	}
	changedOperation := operationRequest
	changedOperation.CourtUUID = "court-2"
	if _, _, err := intentService.AdmitOperation(ctx, changedOperation); !isBookingIntentConflict(err) {
		t.Fatalf("changed operation error = %v, want conflict", err)
	}

	courtBookings := storage.NewCourtBookingRepo(testPool)
	byCredential, err := courtBookings.HasActiveByCredentialID(ctx, credential.ID)
	if err != nil {
		t.Fatalf("HasActiveByCredentialID: %v", err)
	}
	byVenue, err := courtBookings.HasActiveByVenueID(ctx, venueID)
	if err != nil {
		t.Fatalf("HasActiveByVenueID: %v", err)
	}
	if !byCredential || !byVenue {
		t.Fatalf("pending intent guards = credential %t venue %t, want both true", byCredential, byVenue)
	}
	succeededStatus := &bookingapi.OperationStatus{
		OperationID: string(operationID), Kind: bookingapi.OperationCreate, State: bookingapi.StateSucceeded,
		Phase: bookingapi.PhaseFinalization, Outcome: bookingapi.OutcomeSuccessWithWarning, Revision: 2,
		Evidence: &bookingapi.ProviderEvidence{BookingUUID: "booking-1", BookingID: 10, PaymentID: 20, MatchID: "match-1", State: "ACCEPTED"},
		Warnings: []string{"tracking was not completed"}, ResultExpired: true,
	}
	operation, applied, err = repository.ApplyOperationStatus(ctx, succeededStatus)
	if err != nil || !applied || operation.State != models.ManagementBookingOperationSucceeded || !operation.ResultExpired || operation.MatchID != "match-1" {
		t.Fatalf("complete operation = operation %+v applied %t err %v", operation, applied, err)
	}
	recoverable, err := repository.ListRecoverableOperations(ctx, 10)
	if err != nil {
		t.Fatalf("list recoverable operations: %v", err)
	}
	foundUnmaterializedSuccess := false
	for _, candidate := range recoverable {
		foundUnmaterializedSuccess = foundUnmaterializedSuccess || candidate.OperationID == operationID
	}
	if !foundUnmaterializedSuccess {
		t.Fatal("successful create without a court-booking projection was not recoverable")
	}
	if err := repository.FinalizeIntent(ctx, intentID, models.BookingIntentPartial); err != nil {
		t.Fatalf("finalize partial intent: %v", err)
	}
	storedIntent, err = repository.GetIntentByRequestKey(ctx, intentRequest.RequestKey)
	if err != nil || storedIntent.State != models.BookingIntentPartial || storedIntent.GameID == nil {
		t.Fatalf("partial terminal intent = intent %+v err %v", storedIntent, err)
	}
	var resultCount, gameCount int
	if err := testPool.QueryRow(ctx, `SELECT COUNT(*) FROM auto_booking_results WHERE management_booking_intent_id = $1`, intentID).Scan(&resultCount); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `SELECT COUNT(*) FROM games WHERE management_booking_intent_id = $1`, intentID).Scan(&gameCount); err != nil {
		t.Fatal(err)
	}
	if resultCount != 1 || gameCount != 1 {
		t.Fatalf("materialized results/games = %d/%d, want 1/1", resultCount, gameCount)
	}
	if err := repository.FinalizeIntent(ctx, intentID, models.BookingIntentPartial); err != nil {
		t.Fatalf("replay partial intent finalization: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT COUNT(*) FROM games WHERE management_booking_intent_id = $1`, intentID).Scan(&gameCount); err != nil || gameCount != 1 {
		t.Fatalf("replayed materialized game count = %d err = %v", gameCount, err)
	}
	byCredential, err = courtBookings.HasActiveByCredentialID(ctx, credential.ID)
	if err != nil {
		t.Fatalf("HasActiveByCredentialID after completion: %v", err)
	}
	byVenue, err = courtBookings.HasActiveByVenueID(ctx, venueID)
	if err != nil {
		t.Fatalf("HasActiveByVenueID after completion: %v", err)
	}
	if byCredential || byVenue {
		t.Fatalf("terminal historical guards = credential %t venue %t, want both false", byCredential, byVenue)
	}
	if _, err := testPool.Exec(ctx, `DELETE FROM venue_credentials WHERE id = $1`, credential.ID); err != nil {
		t.Fatalf("delete terminal credential: %v", err)
	}
	if _, err := testPool.Exec(ctx, `DELETE FROM venues WHERE id = $1`, venueID); err != nil {
		t.Fatalf("delete terminal venue: %v", err)
	}
	var storedCredentialID *int64
	var storedVenueID int64
	if err := testPool.QueryRow(ctx, `
		SELECT mbo.credential_id, mbi.venue_id
		FROM management_booking_operations mbo
		JOIN management_booking_intents mbi ON mbi.id = mbo.intent_id
		WHERE mbo.operation_id = $1`, operationID).Scan(&storedCredentialID, &storedVenueID); err != nil {
		t.Fatalf("read retained terminal provenance: %v", err)
	}
	if storedCredentialID != nil || storedVenueID != venueID {
		t.Fatalf("retained terminal provenance = credential %v venue %d", storedCredentialID, storedVenueID)
	}
}

func isBookingIntentConflict(err error) bool {
	var conflict *storage.BookingIntentConflictError
	return errors.As(err, &conflict)
}

func managementBookingScope() bookingapi.Scope {
	return bookingapi.Scope{
		ServiceKey: "booking.eversports.primary", Provider: "eversports",
		FacilityID: "76443", FacilityUUID: "facility-uuid", FacilitySlug: "facility-slug", Sport: "squash",
	}
}
