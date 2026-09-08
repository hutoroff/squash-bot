package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/models"
)

func (j *CancellationReminderJob) cancelUsingBookingEntriesReliable(
	ctx context.Context,
	game *models.Game,
	entryByLabel map[int]*models.CourtBooking,
	selected []int,
	courtsToCancel int,
	updateFn courtsUpdater,
) (*courtCancellationResult, error) {
	if len(selected) == 0 {
		return buildNoOpResult(game), nil
	}
	firstEntry := entryByLabel[selected[0]]
	gameTime := firstEntry.GameTime
	originScope := firstEntry.OriginScope
	if originScope.ServiceKey == "" || originScope != j.reliableBooking.scope {
		result := buildNoOpResult(game)
		result.cancelErrors = []error{fmt.Errorf("court booking origin is missing or does not match the configured booking binding")}
		return result, nil
	}
	for _, label := range selected[1:] {
		if entryByLabel[label] == nil || entryByLabel[label].OriginScope != originScope {
			result := buildNoOpResult(game)
			result.cancelErrors = []error{fmt.Errorf("selected court bookings have mixed provider origins")}
			return result, nil
		}
	}
	gameID := game.ID
	intent, _, err := j.reliableBooking.intents.AdmitIntent(ctx, BookingIntentRequest{
		RequestKey: fmt.Sprintf("scheduled-cancel:%d:target-%d:g1", game.ID, game.CourtsCount-courtsToCancel),
		Action:     bookingapi.OperationCancel, Source: models.BookingIntentScheduled, Scope: originScope,
		GroupID: game.ChatID, VenueID: game.Venue.ID, GameID: &gameID, GameDate: game.GameDate,
		GameTime: gameTime, RequestedCount: courtsToCancel, Generation: 1,
	})
	if err != nil {
		return nil, fmt.Errorf("admit durable cancellation intent: %w", err)
	}

	var canceledLabels []int
	var cancelErrors []error
	unresolved := false
	for _, label := range selected {
		entry := entryByLabel[label]
		if entry == nil || entry.MatchID == "" || entry.CredentialID == nil {
			cancelErrors = append(cancelErrors, fmt.Errorf("court %d has no exact match/credential binding", label))
			continue
		}
		operation, _, admitErr := j.reliableBooking.intents.AdmitOperation(ctx, BookingOperationRequest{
			IntentID: intent.ID, DedupKey: fmt.Sprintf("cancel:%s:g%d", entry.MatchID, intent.Generation),
			Kind: bookingapi.OperationCancel, TargetMatchID: entry.MatchID, CredentialID: entry.CredentialID,
		})
		if admitErr != nil {
			return nil, fmt.Errorf("admit durable cancellation operation: %w", admitErr)
		}
		wasSucceeded := ManagementBookingSucceeded(operation)
		if managementBookingOperationPending(operation) {
			if operation.State == models.ManagementBookingOperationPlanned {
				credential, credentialErr := j.credService.GetDecryptedByID(ctx, *entry.CredentialID)
				if credentialErr != nil {
					cancelErrors = append(cancelErrors, fmt.Errorf("credential lookup failed for court %d: %w", label, credentialErr))
					unresolved = true
					continue
				}
				operation, err = j.reliableBooking.executor.ExecuteCancel(ctx, operation, bookingapi.Credentials{Email: credential.Login, Password: credential.Password})
			} else {
				operation, err = j.reliableBooking.executor.Observe(ctx, operation.OperationID)
			}
			if err != nil {
				cancelErrors = append(cancelErrors, fmt.Errorf("cancellation for court %d remains pending", label))
				unresolved = true
				continue
			}
		}
		if !ManagementBookingSucceeded(operation) {
			if operation.State == models.ManagementBookingOperationOutcomeUnknown || managementBookingOperationPending(operation) {
				unresolved = true
				cancelErrors = append(cancelErrors, fmt.Errorf("cancellation outcome for court %d is unresolved", label))
			} else {
				cancelErrors = append(cancelErrors, fmt.Errorf("cancellation for court %d was rejected with no provider effect", label))
			}
			continue
		}
		if err := j.courtBookingRepo.MarkCanceled(ctx, entry.MatchID); err != nil {
			cancelErrors = append(cancelErrors, fmt.Errorf("persist cancellation for court %d: %w", label, err))
			continue
		}
		canceledLabels = append(canceledLabels, label)
		if !wasSucceeded && j.auditSvc != nil {
			j.auditSvc.RecordCourtCanceled(ctx, game.Venue.ID, game.ChatID, game.Venue.Name, strconv.Itoa(label), game.GameDate)
		}
	}

	if !unresolved {
		state := models.BookingIntentCompleted
		if len(canceledLabels) > 0 && len(canceledLabels) < courtsToCancel {
			state = models.BookingIntentPartial
		}
		if err := j.reliableBooking.intents.FinalizeIntent(ctx, intent.ID, state); err != nil {
			return nil, fmt.Errorf("finalize durable cancellation intent: %w", err)
		}
	}
	if len(canceledLabels) == 0 {
		result := buildNoOpResult(game)
		result.cancelErrors = cancelErrors
		return result, nil
	}

	canceledCount := make(map[string]int, len(canceledLabels))
	for _, label := range canceledLabels {
		canceledCount[strconv.Itoa(label)]++
	}
	var remaining []string
	for _, court := range splitCourts(game.Courts) {
		court = strings.TrimSpace(court)
		if canceledCount[court] > 0 {
			canceledCount[court]--
			continue
		}
		remaining = append(remaining, court)
	}
	remainingCourts := strings.Join(remaining, ",")
	if err := updateFn(ctx, game.ID, remainingCourts, len(remaining)); err != nil {
		return nil, fmt.Errorf("persist updated courts: %w", err)
	}
	return &courtCancellationResult{
		canceledCourts: canceledLabels, remainingCourts: remainingCourts,
		remainingCount: len(remaining), cancelErrors: cancelErrors,
	}, nil
}
