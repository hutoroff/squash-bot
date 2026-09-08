package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/models"
)

func bookFreeCourtsReliable(
	ctx context.Context,
	deps bookingDeps,
	venue *models.Venue,
	chatID int64,
	gameDate time.Time,
	gameDateStr string,
	gameTime string,
	groupTZ *time.Location,
	count int,
	business bookingIntentContext,
) (*BookFreeCourtsResult, error) {
	result := &BookFreeCourtsResult{Requested: count}
	if business.requestKey == "" || business.source == "" {
		return nil, fmt.Errorf("durable booking business identity is required")
	}
	gameStart, err := parsePreferredTime(gameDateStr, gameTime, groupTZ)
	if err != nil {
		return nil, fmt.Errorf("parse preferred time: %w", err)
	}
	intent, intentCreated, err := deps.reliable.intents.AdmitIntent(ctx, BookingIntentRequest{
		RequestKey: business.requestKey, Action: bookingapi.OperationCreate, Source: business.source,
		Scope: deps.reliable.scope, GroupID: chatID, VenueID: venue.ID, GameID: business.gameID,
		GameDate: gameDate, GameTime: gameTime, RequestedCount: count, Generation: business.generation,
	})
	if err != nil {
		return nil, fmt.Errorf("admit durable booking intent: %w", err)
	}
	operations, err := deps.reliable.intents.ListOperations(ctx, intent.ID)
	if err != nil {
		return nil, fmt.Errorf("list durable booking operations: %w", err)
	}

	bookedCourts := make(map[string]bool)
	blockedCount := 0
	for _, operation := range operations {
		wasSucceeded := ManagementBookingSucceeded(operation)
		if managementBookingOperationPending(operation) {
			if operation.State == models.ManagementBookingOperationPlanned {
				if operation.CredentialID == nil {
					return nil, fmt.Errorf("planned booking operation %s has no credential reference", operation.OperationID)
				}
				credential, credErr := deps.credService.GetDecryptedByID(ctx, *operation.CredentialID)
				if credErr != nil {
					result.Failures = append(result.Failures, BookingFailure{Reason: "credential unavailable for durable operation"})
					blockedCount++
					continue
				}
				operation, err = deps.reliable.executor.ExecuteCreate(ctx, operation, bookingapi.Credentials{Email: credential.Login, Password: credential.Password})
			} else {
				operation, err = deps.reliable.executor.Observe(ctx, operation.OperationID)
			}
			if err != nil {
				result.Failures = append(result.Failures, BookingFailure{Reason: "durable booking operation remains pending"})
				blockedCount++
				continue
			}
		}
		if ManagementBookingSucceeded(operation) {
			appendReliableBookingResult(ctx, deps, result, venue, chatID, gameDate, gameTime, operation, !wasSucceeded, bookedCourts)
			continue
		}
		if operation.State == models.ManagementBookingOperationOutcomeUnknown || managementBookingOperationPending(operation) {
			blockedCount++
			result.Failures = append(result.Failures, BookingFailure{Reason: "booking outcome is unresolved"})
		}
	}

	needed := count - len(result.BookedLabels) - blockedCount
	if needed <= 0 || blockedCount > 0 || (!intentCreated && (intent.State == models.BookingIntentCompleted || intent.State == models.BookingIntentPartial || intent.State == models.BookingIntentUnknown)) {
		return result, nil
	}
	creds, err := deps.credService.ListForBooking(ctx, venue.ID, deps.credCooldown)
	if err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}
	if len(creds) == 0 {
		if len(operations) == 0 {
			if finalizeErr := deps.reliable.intents.FinalizeIntent(ctx, intent.ID, models.BookingIntentCompleted); finalizeErr != nil {
				return nil, fmt.Errorf("finalize no-credential booking intent: %w", finalizeErr)
			}
		}
		return result, nil
	}
	firstLogin, firstPassword := creds[0].Login, creds[0].Password
	checkDateLocal := gameStart.Format("2006-01-02")
	checkStartHHMM := gameStart.Format("1504")
	allCourts, err := deps.bookingClient.ListCourts(ctx, checkDateLocal, firstLogin, firstPassword)
	if err != nil {
		return nil, fmt.Errorf("list courts: %w", err)
	}
	occupiedSlots, err := deps.bookingClient.ListMatches(ctx, checkDateLocal, checkStartHHMM, checkStartHHMM, false, firstLogin, firstPassword)
	if err != nil {
		return nil, fmt.Errorf("list matches: %w", err)
	}
	occupied := make(map[int]bool, len(occupiedSlots))
	for _, slot := range occupiedSlots {
		occupied[slot.Court] = true
	}
	venueCourts := make(map[int]bool)
	for _, configured := range strings.Split(venue.Courts, ",") {
		if courtID, parseErr := strconv.Atoi(strings.TrimSpace(configured)); parseErr == nil {
			venueCourts[courtID] = true
		}
	}
	available := filterFreeCourts(allCourts, occupied, venueCourts, parseCourtIDs(venue.AutoBookingCourts))
	if len(available) == 0 {
		result.Failures = append(result.Failures, BookingFailure{Reason: "no free courts available"})
		if finalizeErr := finalizeReliableCreateIntent(ctx, deps.reliable.intents, intent.ID, len(result.BookedLabels), count, false); finalizeErr != nil {
			return nil, finalizeErr
		}
		return result, nil
	}
	uuidToCourtLabel := make(map[string]string, len(allCourts))
	for _, court := range allCourts {
		label := court.UUID
		if number := extractCourtNumber(court.Name); number > 0 {
			label = strconv.Itoa(number)
		}
		uuidToCourtLabel[court.UUID] = label
	}
	gameEnd := gameStart.Add(autoBookingCourtDuration)
	startProvider := gameStart.Format("2006-01-02T15:04:05.000Z07:00")
	endProvider := gameEnd.Format("2006-01-02T15:04:05.000Z07:00")

	attemptsByCourt := make(map[string]int)
	for _, operation := range operations {
		if operation.Kind == bookingapi.OperationCreate {
			attemptsByCourt[operation.CourtUUID]++
		}
	}
	for _, credential := range creds {
		if needed <= 0 || len(available) == 0 {
			break
		}
		limit := credential.MaxCourts
		if limit > needed {
			limit = needed
		}
		for used := 0; used < limit && needed > 0 && len(available) > 0; used++ {
			courtUUID := available[0]
			available = available[1:]
			attemptsByCourt[courtUUID]++
			dedupKey := fmt.Sprintf("create:%s:g%d:a%d", courtUUID, intent.Generation, attemptsByCourt[courtUUID])
			operation, _, admitErr := deps.reliable.intents.AdmitOperation(ctx, BookingOperationRequest{
				IntentID: intent.ID, DedupKey: dedupKey, Kind: bookingapi.OperationCreate,
				CourtUUID: courtUUID, CourtLabel: uuidToCourtLabel[courtUUID], StartProvider: startProvider,
				EndProvider: endProvider, CredentialID: &credential.ID,
			})
			if admitErr != nil {
				return nil, fmt.Errorf("admit durable create operation: %w", admitErr)
			}
			wasSucceeded := ManagementBookingSucceeded(operation)
			operation, executeErr := deps.reliable.executor.ExecuteCreate(ctx, operation, bookingapi.Credentials{Email: credential.Login, Password: credential.Password})
			if executeErr != nil {
				result.Failures = append(result.Failures, BookingFailure{Reason: "durable booking operation remains pending"})
				needed--
				continue
			}
			if ManagementBookingSucceeded(operation) {
				appendReliableBookingResult(ctx, deps, result, venue, chatID, gameDate, gameTime, operation, !wasSucceeded, bookedCourts)
				needed--
				continue
			}
			if ManagementBookingCredentialRejected(operation) {
				if markErr := deps.credService.MarkError(ctx, credential.ID); markErr != nil {
					deps.logger.Error("reliable booking: mark credential rejected", "credential_id", credential.ID, "err", markErr)
				}
				result.Failures = append(result.Failures, BookingFailure{Reason: "provider authentication was rejected with no effect"})
				available = append([]string{courtUUID}, available...)
				break
			}
			if operation.State == models.ManagementBookingOperationOutcomeUnknown || managementBookingOperationPending(operation) {
				result.Failures = append(result.Failures, BookingFailure{Reason: "booking outcome is unresolved"})
				needed--
				continue
			}
			if operation.State == models.ManagementBookingOperationNotDispatched && operation.Code == bookingapi.ErrorAccountBlocked {
				result.Failures = append(result.Failures, BookingFailure{Reason: "provider account is blocked with no dispatched operation"})
				available = append([]string{courtUUID}, available...)
				break
			}
			result.Failures = append(result.Failures, BookingFailure{Reason: "booking was rejected with no provider effect"})
		}
	}
	unresolved := needed < count-len(result.BookedLabels)
	if needed > 0 && len(available) > 0 {
		result.Failures = append(result.Failures, BookingFailure{Reason: fmt.Sprintf("credentials exhausted: booked %d of %d", len(result.BookedLabels), count)})
	}
	if finalizeErr := finalizeReliableCreateIntent(ctx, deps.reliable.intents, intent.ID, len(result.BookedLabels), count, unresolved); finalizeErr != nil {
		return nil, finalizeErr
	}
	return result, nil
}

func appendReliableBookingResult(ctx context.Context, deps bookingDeps, result *BookFreeCourtsResult, venue *models.Venue, chatID int64, gameDate time.Time, gameTime string, operation *models.ManagementBookingOperation, recordAudit bool, seen map[string]bool) {
	if seen[operation.CourtUUID] {
		return
	}
	seen[operation.CourtUUID] = true
	result.BookedLabels = append(result.BookedLabels, operation.CourtLabel)
	if recordAudit && deps.auditSvc != nil {
		deps.auditSvc.RecordCourtBooked(ctx, venue.ID, chatID, venue.Name, operation.CourtLabel, gameDate)
	}
	if operation.MatchID == "" {
		result.Failures = append(result.Failures, BookingFailure{Reason: "booking succeeded without a cancellable match ID"})
		return
	}
	booking := &models.CourtBooking{
		VenueID: venue.ID, GameDate: gameDate, GameTime: gameTime, CourtUUID: operation.CourtUUID,
		CourtLabel: operation.CourtLabel, MatchID: operation.MatchID, BookingUUID: operation.BookingUUID,
		CredentialID: operation.CredentialID, CreateOperationID: operation.OperationID,
		OriginScope: deps.reliable.scope,
	}
	if deps.courtBookingRepo != nil {
		if err := deps.courtBookingRepo.Save(ctx, booking); err != nil {
			deps.logger.Error("reliable booking: save court booking", "venue_id", venue.ID, "operation_id", operation.OperationID, "err", err)
			return
		}
	}
	result.BookedCourtBookings = append(result.BookedCourtBookings, booking)
}

func finalizeReliableCreateIntent(ctx context.Context, intents *BookingIntentService, intentID int64, successes, requested int, unresolved bool) error {
	if unresolved {
		return nil
	}
	state := models.BookingIntentCompleted
	if successes > 0 && successes < requested {
		state = models.BookingIntentPartial
	}
	if err := intents.FinalizeIntent(ctx, intentID, state); err != nil {
		return fmt.Errorf("finalize durable booking intent: %w", err)
	}
	return nil
}
