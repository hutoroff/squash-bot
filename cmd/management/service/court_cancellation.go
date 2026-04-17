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
		entries, err := j.courtBookingRepo.GetByVenueAndDate(ctx, game.Venue.ID, game.GameDate)
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
				j.logger.Error("court cancellation: get credential for cancel",
					"game_id", game.ID, "cred_id", *entry.CredentialID, "err", err)
				// Proceed with empty credentials (env-var fallback).
			} else if cred != nil {
				login, password = cred.Login, cred.Password
			}
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

// cancelUsingListMatches is the legacy fallback path for courts that were booked
// before the court_bookings table was introduced. It queries the booking service
// with the default (env-var) account and cancels using empty credentials.
func (j *CancellationReminderJob) cancelUsingListMatches(
	ctx context.Context,
	game *models.Game,
	courtsToCancel int,
	loc *time.Location,
	updateFn courtsUpdater,
) (*courtCancellationResult, error) {
	// Format time parameters in local (venue) time.
	date, startHHMM, endHHMM := slotQueryWindow(game.GameDate.In(loc))

	slots, err := j.bookingClient.ListMatches(ctx, date, startHHMM, endHHMM, true, "", "")
	if err != nil {
		return nil, fmt.Errorf("list matches: %w", err)
	}

	type bookedCourt struct {
		courtID   int
		matchUUID string
	}
	var booked []bookedCourt
	for _, sl := range slots {
		if sl.IsUserBookingOwner && sl.Match != nil && sl.Match.UUID != "" {
			booked = append(booked, bookedCourt{courtID: sl.Court, matchUUID: sl.Match.UUID})
		}
	}

	if len(booked) == 0 {
		j.logger.Info("court cancellation: no owned bookings found", "game_id", game.ID)
		return buildNoOpResult(game), nil
	}

	sort.Slice(booked, func(i, k int) bool { return booked[i].courtID < booked[k].courtID })

	courtIDs := make([]int, len(booked))
	for i, b := range booked {
		courtIDs[i] = b.courtID
	}

	// Build Eversports ID → court name-number mapping for priority-based selection.
	var nameNumByID map[int]int
	if game.Venue != nil && game.Venue.AutoBookingCourts != "" {
		courts, listErr := j.bookingClient.ListCourts(ctx, date, "", "")
		if listErr != nil {
			j.logger.Warn("court cancellation: list courts failed, skipping priority selection",
				"game_id", game.ID, "err", listErr)
		} else {
			nameNumByID = make(map[int]int, len(courts))
			for _, c := range courts {
				id, err := strconv.Atoi(c.ID)
				if err != nil {
					continue
				}
				if n := extractCourtNumber(c.Name); n > 0 && id > 0 {
					nameNumByID[id] = n
				}
			}
		}
	}

	var selected []int
	if game.Venue != nil && game.Venue.AutoBookingCourts != "" && len(nameNumByID) > 0 {
		bookedByNameNum := make(map[int]int, len(booked))
		for _, b := range booked {
			if n := nameNumByID[b.courtID]; n > 0 {
				bookedByNameNum[n] = b.courtID
			}
		}
		orderedPreferred := parseCourtIDs(game.Venue.AutoBookingCourts)
		for i := len(orderedPreferred) - 1; i >= 0 && len(selected) < courtsToCancel; i-- {
			if eversportsID, ok := bookedByNameNum[orderedPreferred[i]]; ok {
				selected = append(selected, eversportsID)
			}
		}
	}
	if len(selected) < courtsToCancel {
		selectedSet := make(map[int]bool, len(selected))
		for _, cID := range selected {
			selectedSet[cID] = true
		}
		var remaining []int
		for _, cID := range courtIDs {
			if !selectedSet[cID] {
				remaining = append(remaining, cID)
			}
		}
		selected = append(selected, selectCourtsToCancel(remaining, courtsToCancel-len(selected))...)
	}

	uuidByCourtID := make(map[int]string, len(booked))
	for _, b := range booked {
		uuidByCourtID[b.courtID] = b.matchUUID
	}

	var canceled []int
	var cancelErrors []error
	for _, cID := range selected {
		uuid := uuidByCourtID[cID]
		if err := j.bookingClient.CancelMatch(ctx, uuid, "", ""); err != nil {
			j.logger.Error("court cancellation: cancel match failed",
				"game_id", game.ID, "court_id", cID, "match_uuid", uuid, "err", err)
			cancelErrors = append(cancelErrors, err)
			continue
		}
		j.logger.Info("court cancellation: canceled",
			"game_id", game.ID, "court_id", cID, "match_uuid", uuid)
		canceled = append(canceled, cID)
	}

	if len(canceled) == 0 {
		result := buildNoOpResult(game)
		result.cancelErrors = cancelErrors
		return result, nil
	}

	gameCourts := splitCourts(game.Courts)
	newCourts, err := removeCanceledFromGameCourts(gameCourts, canceled, courtIDs)
	if err != nil {
		j.logger.Error("court cancellation: cannot map canceled courts to game record",
			"game_id", game.ID,
			"game_courts", len(gameCourts),
			"booked_courts", len(courtIDs),
			"err", err)
		return nil, fmt.Errorf("map canceled courts: %w", err)
	}

	newCourtsStr := strings.Join(newCourts, ",")
	newCount := len(newCourts)

	if err := updateFn(ctx, game.ID, newCourtsStr, newCount); err != nil {
		j.logger.Error("court cancellation: update game courts", "game_id", game.ID, "err", err)
		return nil, fmt.Errorf("persist updated courts: %w", err)
	}

	return &courtCancellationResult{
		canceledCourts:  canceled,
		remainingCourts: newCourtsStr,
		remainingCount:  newCount,
		cancelErrors:    cancelErrors,
	}, nil
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
