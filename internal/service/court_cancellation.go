package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vkhutorov/squash_bot/internal/models"
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
// Returns the cancellation result and any error that aborted the operation.
// Partial cancellations (some courts canceled, then an error) are reflected in the result.
func (s *SchedulerService) cancelUnusedCourts(
	ctx context.Context,
	game *models.Game,
	courtsToCancel int,
) (*courtCancellationResult, error) {
	return s.cancelUnusedCourtsLogicOnly(ctx, game, courtsToCancel,
		func(ctx context.Context, gameID int64, courts string, count int) error {
			return s.gameRepo.UpdateCourts(ctx, gameID, courts, count)
		})
}

// cancelUnusedCourtsLogicOnly is the testable core of cancelUnusedCourts.
// It accepts a courtsUpdater callback instead of using gameRepo directly.
func (s *SchedulerService) cancelUnusedCourtsLogicOnly(
	ctx context.Context,
	game *models.Game,
	courtsToCancel int,
	updateFn courtsUpdater,
) (*courtCancellationResult, error) {
	if s.bookingClient == nil || courtsToCancel == 0 {
		return buildNoOpResult(game), nil
	}

	// Format time parameters: startTime = HHMM of game, endTime = startTime + 10 min.
	date := game.GameDate.UTC().Format("2006-01-02")
	startHHMM := game.GameDate.UTC().Format("1504")
	endHHMM := game.GameDate.UTC().Add(10 * time.Minute).Format("1504")

	slots, err := s.bookingClient.ListMatches(ctx, date, startHHMM, endHHMM, true)
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
		s.logger.Info("court cancellation: no owned bookings found", "game_id", game.ID)
		return buildNoOpResult(game), nil
	}

	// Sort by court ID for deterministic grouping.
	sort.Slice(booked, func(i, j int) bool { return booked[i].courtID < booked[j].courtID })

	// Build ordered list of court IDs for the selection algorithm.
	courtIDs := make([]int, len(booked))
	for i, b := range booked {
		courtIDs[i] = b.courtID
	}

	// Select which courts to cancel using the consecutive-grouping algorithm.
	selected := selectCourtsToCancel(courtIDs, courtsToCancel)

	// Build a lookup: courtID → matchUUID.
	uuidByCourtID := make(map[int]string, len(booked))
	for _, b := range booked {
		uuidByCourtID[b.courtID] = b.matchUUID
	}

	// Cancel selected courts one by one, collecting successes.
	var canceled []int
	for _, cID := range selected {
		uuid := uuidByCourtID[cID]
		if err := s.bookingClient.CancelMatch(ctx, uuid); err != nil {
			s.logger.Error("court cancellation: cancel match failed",
				"game_id", game.ID, "court_id", cID, "match_uuid", uuid, "err", err)
			// Continue trying remaining courts.
			continue
		}
		s.logger.Info("court cancellation: canceled",
			"game_id", game.ID, "court_id", cID, "match_uuid", uuid)
		canceled = append(canceled, cID)
	}

	if len(canceled) == 0 {
		return buildNoOpResult(game), nil
	}

	// Rebuild game courts string by removing canceled court entries.
	// The game stores courts as names (e.g. "1,2,3"), and the canceled list uses Eversports
	// numeric IDs — by convention they match (the venue courts list uses the same identifiers).
	gameCourts := splitCourts(game.Courts)
	newCourts, err := removeCanceledFromGameCourts(gameCourts, canceled, courtIDs)
	if err != nil {
		// Cannot unambiguously determine the updated court list; treat as no-op so the
		// DB record and the notification remain consistent with each other.
		s.logger.Error("court cancellation: cannot map canceled courts to game record",
			"game_id", game.ID,
			"game_courts", len(gameCourts),
			"booked_courts", len(courtIDs),
			"err", err)
		return nil, fmt.Errorf("map canceled courts: %w", err)
	}

	newCourtsStr := strings.Join(newCourts, ",")
	newCount := len(newCourts)

	// Persist updated court list. If the write fails, treat as no-op so the notification
	// sent to the group reflects the DB state and not a court list that was never stored.
	if err := updateFn(ctx, game.ID, newCourtsStr, newCount); err != nil {
		s.logger.Error("court cancellation: update game courts", "game_id", game.ID, "err", err)
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
//
// Algorithm:
//  1. Split sortedCourtIDs into consecutive groups (gap > 1 between neighbours = new group).
//  2. Repeatedly pick the group with the fewest courts; break ties by picking the group whose
//     first court has the lowest ID.
//  3. Cancel from the END of the chosen group.
//
// Repeats until n courts are selected or all courts are exhausted.
func selectCourtsToCancel(sortedCourtIDs []int, n int) []int {
	if n <= 0 || len(sortedCourtIDs) == 0 {
		return nil
	}

	// Build mutable groups (slices, sorted ascending within each group).
	groups := buildConsecutiveGroups(sortedCourtIDs)

	var selected []int
	for len(selected) < n {
		// Find the smallest group; tie-break by smallest first element.
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
			break // all groups exhausted
		}

		// Cancel from the end of the chosen group.
		g := groups[best]
		last := g[len(g)-1]
		groups[best] = g[:len(g)-1]
		selected = append(selected, last)
	}

	return selected
}

// buildConsecutiveGroups splits a sorted int slice into runs of consecutive integers
// (where consecutive means adjacent values differ by exactly 1).
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
// to the canceled Eversports court IDs.
//
// Matching strategy: the game courts list and the Eversports numeric IDs are matched
// positionally after sorting. gameCourts[i] corresponds to sortedBookedIDs[i].
// Any game court whose booked counterpart appears in canceledIDs is removed.
//
// Returns an error when the lengths of gameCourts and sortedBookedIDs differ, because
// the positional mapping would be ambiguous and the wrong courts could be removed.
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
