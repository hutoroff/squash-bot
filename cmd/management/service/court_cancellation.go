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
	// canceledCourts are the court IDs that were successfully canceled (sorted, ascending).
	canceledCourts []int
	// remainingCourts is the updated comma-separated court string for the game.
	remainingCourts string
	// remainingCount is the new courts_count value.
	remainingCount int
}

// courtsUpdater is a function that persists an updated courts list for a game.
// It mirrors the signature of storage.GameRepo.UpdateCourts.
type courtsUpdater func(ctx context.Context, gameID int64, courts string, count int) error

// cancelUnusedCourts fetches booked slots from the booking service, selects courts
// to cancel according to the grouping algorithm, cancels them, and updates the game record.
//
// courtsToCancel is the number of courts that should be freed up.
// loc is the group's timezone used to convert the game time to local HHMM for the slot query.
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

	// Format time parameters in local (venue) time: Eversports /api/slot returns
	// slot Start values in the venue's local timezone, not UTC.
	date, startHHMM, endHHMM := slotQueryWindow(game.GameDate.In(loc))

	slots, err := j.bookingClient.ListMatches(ctx, date, startHHMM, endHHMM, true)
	if err != nil {
		return nil, fmt.Errorf("list matches: %w", err)
	}

	// Collect slots that are our bookings (IsUserBookingOwner) and have a match UUID.
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

	// Sort by court ID for deterministic grouping.
	sort.Slice(booked, func(i, k int) bool { return booked[i].courtID < booked[k].courtID })

	// Build ordered list of court IDs for the selection algorithm.
	courtIDs := make([]int, len(booked))
	for i, b := range booked {
		courtIDs[i] = b.courtID
	}

	// Select which courts to cancel.
	// When the venue has auto_booking_courts configured, first cancel in reversed priority
	// order (lowest-priority court first). Then fall back to the consecutive-grouping
	// algorithm for any remaining slots.
	var selected []int
	if game.Venue != nil && game.Venue.AutoBookingCourts != "" {
		bookedSet := make(map[int]bool, len(booked))
		for _, b := range booked {
			bookedSet[b.courtID] = true
		}
		selected = selectCourtsByCancellationPriority(parseCourtIDs(game.Venue.AutoBookingCourts), bookedSet, courtsToCancel)
	}
	if len(selected) < courtsToCancel {
		// Build the set of courts already selected to exclude them from the fallback input.
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

	// Build a lookup: courtID → matchUUID.
	uuidByCourtID := make(map[int]string, len(booked))
	for _, b := range booked {
		uuidByCourtID[b.courtID] = b.matchUUID
	}

	// Cancel selected courts one by one, collecting successes.
	var canceled []int
	for _, cID := range selected {
		uuid := uuidByCourtID[cID]
		if err := j.bookingClient.CancelMatch(ctx, uuid); err != nil {
			j.logger.Error("court cancellation: cancel match failed",
				"game_id", game.ID, "court_id", cID, "match_uuid", uuid, "err", err)
			// Continue trying remaining courts.
			continue
		}
		j.logger.Info("court cancellation: canceled",
			"game_id", game.ID, "court_id", cID, "match_uuid", uuid)
		canceled = append(canceled, cID)
	}

	if len(canceled) == 0 {
		return buildNoOpResult(game), nil
	}

	// Rebuild game courts string by removing canceled court entries.
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

// selectCourtsByCancellationPriority returns up to n court IDs to cancel, selected from
// bookedSet in the reversed order of orderedPreferred (lowest-priority first).
func selectCourtsByCancellationPriority(orderedPreferred []int, bookedSet map[int]bool, n int) []int {
	if n <= 0 || len(orderedPreferred) == 0 {
		return nil
	}
	var selected []int
	for i := len(orderedPreferred) - 1; i >= 0 && len(selected) < n; i-- {
		if bookedSet[orderedPreferred[i]] {
			selected = append(selected, orderedPreferred[i])
		}
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
