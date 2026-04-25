package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

// courtCancellationResult holds the outcome of attempting court cancellations.
type courtCancellationResult struct {
	// canceledCourts are the court labels/IDs that were successfully canceled.
	canceledCourts []int
	// remainingCourts is the updated comma-separated court string for the game.
	remainingCourts string
	// remainingCount is the new courts_count value.
	remainingCount int
	// cancelErrors are per-court errors returned by the booking service (Eversports).
	// Non-empty when one or more CancelMatch calls failed.
	cancelErrors []error
}

// courtsUpdater is a function that persists an updated courts list for a game.
// It mirrors the signature of storage.GameRepo.UpdateCourts.
type courtsUpdater func(ctx context.Context, gameID int64, courts string, count int) error

// cancelUnusedCourts fetches booked slots, selects courts to cancel, cancels them,
// and updates the game record.
//
// courtsToCancel is the number of courts that should be freed up.
// loc is the group's timezone used to convert the game time to local HHMM for the fallback slot query.
func (j *CancellationReminderJob) cancelUnusedCourts(
	ctx context.Context,
	game *models.Game,
	courtsToCancel int,
	loc *time.Location,
) (*courtCancellationResult, error) {
	return j.cancelUnusedCourtsLogicOnly(ctx, game, courtsToCancel, loc,
		func(ctx context.Context, gameID int64, courts string, count int) error {
			return j.gameRepo.UpdateCourts(ctx, gameID, courts, count)
		})
}

// cancelUnusedCourtsLogicOnly is the testable core of cancelUnusedCourts.
// It accepts a courtsUpdater callback instead of using gameRepo directly.
func (j *CancellationReminderJob) cancelUnusedCourtsLogicOnly(
	ctx context.Context,
	game *models.Game,
	courtsToCancel int,
	loc *time.Location,
	updateFn courtsUpdater,
) (*courtCancellationResult, error) {
	if j.bookingClient == nil || courtsToCancel == 0 {
		return buildNoOpResult(game), nil
	}

	// New credential-aware flow: use stored court_bookings entries when available.
	if j.courtBookingRepo != nil && game.Venue != nil && game.Venue.ID != 0 {
		entries, err := j.loadCourtBookingEntries(ctx, game)
		if err != nil {
			j.logger.Warn("court cancellation: failed to load booking entries, falling back to ListMatches",
				"game_id", game.ID, "err", err)
		} else if len(entries) > 0 {
			return j.cancelUsingBookingEntries(ctx, game, entries, courtsToCancel, updateFn)
		}
	}

	// Fallback: old ListMatches(my=true) flow for courts booked before this feature.
	return j.cancelUsingListMatches(ctx, game, courtsToCancel, loc, updateFn)
}

// loadCourtBookingEntries returns the active court_bookings for the game, filtered by time slot
// when possible. If an auto_booking_result links this game_id to a specific time, only bookings
// for that time slot are returned. Falls back to all venue+date bookings for legacy rows.
func (j *CancellationReminderJob) loadCourtBookingEntries(ctx context.Context, game *models.Game) ([]*models.CourtBooking, error) {
	if j.autoBookingResultRepo != nil {
		abr, err := j.autoBookingResultRepo.GetByGameID(ctx, game.ID)
		if err != nil {
			j.logger.Warn("court cancellation: get auto_booking_result by game_id",
				"game_id", game.ID, "err", err)
		} else if abr != nil && abr.GameTime != "" {
			entries, err := j.courtBookingRepo.GetByVenueAndDateAndTime(ctx, game.Venue.ID, game.GameDate, abr.GameTime)
			if err != nil {
				j.logger.Warn("court cancellation: get by venue+date+time failed, falling back to all-date query",
					"game_id", game.ID, "err", err)
				// Fall through to GetByVenueAndDate below.
			} else {
				return entries, nil
			}
		}
	}
	return j.courtBookingRepo.GetByVenueAndDate(ctx, game.Venue.ID, game.GameDate)
}

// cancelUsingBookingEntries cancels courts using the stored court_bookings records.
// Each entry carries the credential used for booking, enabling per-credential cancellation.
func (j *CancellationReminderJob) cancelUsingBookingEntries(
	ctx context.Context,
	game *models.Game,
	entries []*models.CourtBooking,
	courtsToCancel int,
	updateFn courtsUpdater,
) (*courtCancellationResult, error) {
	// Build courtLabel(int) → CourtBooking map; collect sorted label list.
	entryByLabel := make(map[int]*models.CourtBooking, len(entries))
	sortedLabels := make([]int, 0, len(entries))
	for _, e := range entries {
		label, err := strconv.Atoi(e.CourtLabel)
		if err != nil {
			j.logger.Warn("court cancellation: non-numeric court label, skipping",
				"game_id", game.ID, "label", e.CourtLabel)
			continue
		}
		entryByLabel[label] = e
		sortedLabels = append(sortedLabels, label)
	}
	sort.Ints(sortedLabels)

	// Phase 1: priority-based selection — cancel lowest-priority courts first.
	var selected []int
	if game.Venue != nil && game.Venue.AutoBookingCourts != "" {
		orderedPreferred := parseCourtIDs(game.Venue.AutoBookingCourts)
		for i := len(orderedPreferred) - 1; i >= 0 && len(selected) < courtsToCancel; i-- {
			if _, ok := entryByLabel[orderedPreferred[i]]; ok {
				selected = append(selected, orderedPreferred[i])
			}
		}
	}

	// Phase 2: consecutive-grouping fallback for remaining slots.
	if len(selected) < courtsToCancel {
		selectedSet := make(map[int]bool, len(selected))
		for _, l := range selected {
			selectedSet[l] = true
		}
		var remaining []int
		for _, l := range sortedLabels {
			if !selectedSet[l] {
				remaining = append(remaining, l)
			}
		}
		selected = append(selected, selectCourtsToCancel(remaining, courtsToCancel-len(selected))...)
	}

	// Cancel selected courts one by one.
	var canceledLabels []int
	var cancelErrors []error
	for _, label := range selected {
		entry := entryByLabel[label]
		login, password := "", ""
		if entry.CredentialID != nil && j.credService != nil {
			cred, err := j.credService.GetDecryptedByID(ctx, *entry.CredentialID)
			if err != nil {
				j.logger.Error("court cancellation: get credential for cancel, skipping court",
					"game_id", game.ID, "cred_id", *entry.CredentialID, "err", err)
				cancelErrors = append(cancelErrors, fmt.Errorf("credential lookup failed for court %d: %w", label, err))
				continue
			} else if cred != nil {
				login, password = cred.Login, cred.Password
			}
		}
		if login == "" {
			j.logger.Warn("court cancellation: no credentials for court, skipping",
				"game_id", game.ID, "court_label", label, "match_id", entry.MatchID)
			cancelErrors = append(cancelErrors, fmt.Errorf("no credentials available for court %d", label))
			continue
		}
		if err := j.bookingClient.CancelMatch(ctx, entry.MatchID, login, password); err != nil {
			j.logger.Error("court cancellation: cancel match failed",
				"game_id", game.ID, "court_label", label, "match_id", entry.MatchID, "err", err)
			cancelErrors = append(cancelErrors, err)
			continue
		}
		j.logger.Info("court cancellation: canceled",
			"game_id", game.ID, "court_label", label, "match_id", entry.MatchID)
		if err := j.courtBookingRepo.MarkCanceled(ctx, entry.MatchID); err != nil {
			j.logger.Error("court cancellation: mark canceled in DB",
				"game_id", game.ID, "match_id", entry.MatchID, "err", err)
		}
		canceledLabels = append(canceledLabels, label)
		if j.auditSvc != nil && game.Venue != nil {
			j.auditSvc.RecordCourtCanceled(ctx, game.Venue.ID, game.ChatID, game.Venue.Name, strconv.Itoa(label), game.GameDate)
		}
	}

	if len(canceledLabels) == 0 {
		result := buildNoOpResult(game)
		result.cancelErrors = cancelErrors
		return result, nil
	}

	// Rebuild game.Courts by removing the canceled court labels.
	// Use a count-based map to handle duplicate label edge cases.
	canceledCount := make(map[string]int, len(canceledLabels))
	for _, l := range canceledLabels {
		canceledCount[strconv.Itoa(l)]++
	}
	gameCourts := splitCourts(game.Courts)
	var newCourts []string
	for _, c := range gameCourts {
		c = strings.TrimSpace(c)
		if canceledCount[c] > 0 {
			canceledCount[c]--
			continue
		}
		newCourts = append(newCourts, c)
	}
	newCourtsStr := strings.Join(newCourts, ",")
	newCount := len(newCourts)

	if err := updateFn(ctx, game.ID, newCourtsStr, newCount); err != nil {
		j.logger.Error("court cancellation: update game courts", "game_id", game.ID, "err", err)
		return nil, fmt.Errorf("persist updated courts: %w", err)
	}

	return &courtCancellationResult{
		canceledCourts:  canceledLabels,
		remainingCourts: newCourtsStr,
		remainingCount:  newCount,
		cancelErrors:    cancelErrors,
	}, nil
}

// cancelUsingListMatches was the legacy fallback for courts booked before the
// court_bookings table existed, relying on the service-level Eversports account
// (EVERSPORTS_EMAIL/PASSWORD). Those env vars have been removed — the booking
// service now requires per-request credentials. All active bookings are expected
// to carry credential records and be handled by cancelUsingBookingEntries.
func (j *CancellationReminderJob) cancelUsingListMatches(
	ctx context.Context,
	game *models.Game,
	_ int,
	_ *time.Location,
	_ courtsUpdater,
) (*courtCancellationResult, error) {
	j.logger.Warn("court cancellation: legacy ListMatches path skipped — no service-level credentials; all bookings must have per-credential records",
		"game_id", game.ID)
	return buildNoOpResult(game), nil
}

// selectCourtsToCancel applies the consecutive-grouping algorithm and returns up to n court IDs
// to cancel from the sorted input list.
func selectCourtsToCancel(sortedCourtIDs []int, n int) []int {
	if n <= 0 || len(sortedCourtIDs) == 0 {
		return nil
	}

	groups := buildConsecutiveGroups(sortedCourtIDs)

	var selected []int
	for len(selected) < n {
		best := -1
		for i, g := range groups {
			if len(g) == 0 {
				continue
			}
			if best == -1 {
				best = i
				continue
			}
			bLen, gLen := len(groups[best]), len(g)
			if gLen < bLen || (gLen == bLen && g[0] < groups[best][0]) {
				best = i
			}
		}
		if best == -1 {
			break
		}

		g := groups[best]
		last := g[len(g)-1]
		groups[best] = g[:len(g)-1]
		selected = append(selected, last)
	}

	return selected
}

// buildConsecutiveGroups splits a sorted int slice into runs of consecutive integers.
func buildConsecutiveGroups(sorted []int) [][]int {
	if len(sorted) == 0 {
		return nil
	}
	var groups [][]int
	cur := []int{sorted[0]}
	for _, v := range sorted[1:] {
		if v == cur[len(cur)-1]+1 {
			cur = append(cur, v)
		} else {
			groups = append(groups, cur)
			cur = []int{v}
		}
	}
	groups = append(groups, cur)
	return groups
}

// splitCourts splits a comma-separated courts string into trimmed, non-empty tokens.
func splitCourts(courts string) []string {
	var out []string
	for _, p := range strings.Split(courts, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// removeCanceledFromGameCourts removes from gameCourts the entries that correspond
// to the canceled Eversports court IDs, using positional matching after sorting.
func removeCanceledFromGameCourts(gameCourts []string, canceledIDs []int, sortedBookedIDs []int) ([]string, error) {
	if len(gameCourts) != len(sortedBookedIDs) {
		return nil, fmt.Errorf(
			"game has %d courts but booking service returned %d booked slots: cannot map canceled courts unambiguously",
			len(gameCourts), len(sortedBookedIDs),
		)
	}

	cancelSet := make(map[int]bool, len(canceledIDs))
	for _, id := range canceledIDs {
		cancelSet[id] = true
	}

	var result []string
	for i, name := range gameCourts {
		if !cancelSet[sortedBookedIDs[i]] {
			result = append(result, name)
		}
	}
	return result, nil
}

// buildNoOpResult returns a result reflecting no change to the game.
func buildNoOpResult(game *models.Game) *courtCancellationResult {
	return &courtCancellationResult{
		canceledCourts:  nil,
		remainingCourts: game.Courts,
		remainingCount:  game.CourtsCount,
	}
}

// formatCanceledCourts returns a human-readable comma-separated list of canceled court IDs.
func formatCanceledCourts(courtIDs []int) string {
	parts := make([]string, len(courtIDs))
	for i, id := range courtIDs {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ", ")
}
