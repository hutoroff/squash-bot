package service

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

// BookingFailure records the reason a single court could not be booked.
type BookingFailure struct {
	Reason string
}

// BookFreeCourtsResult summarises the outcome of a bookFreeCourts call.
type BookFreeCourtsResult struct {
	BookedLabels      []string
	BookedCourtBookings []*models.CourtBooking
	Failures          []BookingFailure
	Requested         int
}

// bookingDeps groups the external dependencies required by bookFreeCourts.
// This allows both AutoBookingJob and GameService.BookGameCourts to share the logic.
type bookingDeps struct {
	bookingClient    BookingServiceClient
	credService      *VenueCredentialService
	courtBookingRepo CourtBookingRepository
	auditSvc         *AuditService
	credCooldown     time.Duration
	logger           *slog.Logger
}

// bookFreeCourts books up to `count` free courts for the given venue / time-slot using
// credential rotation. It mirrors steps 1–5 of the original processTimeSlot algorithm:
//
//  1. Load usable credentials (bail if none).
//  2. ListCourts for the game date.
//  3. ListMatches at exact gameTime — courts present are occupied.
//  4. filterFreeCourts to build the ordered candidate list.
//  5. Credential-rotation booking loop; per-court audit + court_bookings rows saved here.
//
// Admin DM notifications are NOT sent from this function; the caller is responsible.
// Returns (nil, error) only on hard failures (credential list, ListCourts, ListMatches).
// Individual booking failures are reported via BookFreeCourtsResult.Failures.
func bookFreeCourts(
	ctx context.Context,
	deps bookingDeps,
	venue *models.Venue,
	chatID int64,
	gameDate time.Time,
	gameDateStr string,
	gameTime string,
	groupTZ *time.Location,
	count int,
) (*BookFreeCourtsResult, error) {
	result := &BookFreeCourtsResult{Requested: count}

	// Parse "HH:MM" into a concrete time.Time for booking.
	gameStart, err := parsePreferredTime(gameDateStr, gameTime, groupTZ)
	if err != nil {
		return nil, fmt.Errorf("parse preferred time: %w", err)
	}

	checkDateLocal := gameStart.Format("2006-01-02")
	checkStartHHMM := gameStart.Format("1504")

	// Load credentials before any Eversports calls so we can bail early.
	creds, err := deps.credService.ListForBooking(ctx, venue.ID, deps.credCooldown)
	if err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}
	if len(creds) == 0 {
		return result, nil // no credentials — caller decides how to notify
	}
	firstLogin, firstPassword := creds[0].Login, creds[0].Password

	// Step 1: Fetch all courts at the facility for the game date.
	allCourts, err := deps.bookingClient.ListCourts(ctx, checkDateLocal, firstLogin, firstPassword)
	if err != nil {
		return nil, fmt.Errorf("list courts: %w", err)
	}

	// Step 2: Fetch matches at target start time; courts in this response are occupied.
	occupiedSlots, err := deps.bookingClient.ListMatches(ctx, checkDateLocal, checkStartHHMM, checkStartHHMM, false, firstLogin, firstPassword)
	if err != nil {
		return nil, fmt.Errorf("list matches: %w", err)
	}

	occupied := make(map[int]bool, len(occupiedSlots))
	for _, sl := range occupiedSlots {
		occupied[sl.Court] = true
	}

	venueCourts := make(map[int]bool)
	for _, c := range strings.Split(venue.Courts, ",") {
		if t := strings.TrimSpace(c); t != "" {
			if id, err := strconv.Atoi(t); err == nil {
				venueCourts[id] = true
			}
		}
	}

	orderedPreferred := parseCourtIDs(venue.AutoBookingCourts)

	// Step 3: Free courts = courts from ListCourts NOT in the occupied set.
	available := filterFreeCourts(allCourts, occupied, venueCourts, orderedPreferred)

	if len(available) == 0 {
		result.Failures = append(result.Failures, BookingFailure{Reason: "no free courts available"})
		return result, nil
	}

	gameEnd := gameStart.Add(autoBookingCourtDuration)
	startRFC := gameStart.Format(time.RFC3339)
	endRFC := gameEnd.Format(time.RFC3339)

	// Build UUID → court-number map for human-readable labels.
	uuidToCourtNum := make(map[string]string, len(allCourts))
	for _, c := range allCourts {
		if c.UUID == "" {
			continue
		}
		num := extractCourtNumber(c.Name)
		if num > 0 {
			uuidToCourtNum[c.UUID] = strconv.Itoa(num)
		} else {
			uuidToCourtNum[c.UUID] = c.UUID
		}
	}

	// ── Credential-rotation booking loop ──────────────────────────────────────

	remaining := count
	for _, cred := range creds {
		if remaining == 0 || len(available) == 0 {
			break
		}
		courtLimit := cred.MaxCourts
		if courtLimit > remaining {
			courtLimit = remaining
		}
		for i := 0; i < courtLimit && len(available) > 0; i++ {
			courtUUID := available[0]
			available = available[1:]

			bookResult, err := deps.bookingClient.BookMatch(ctx, courtUUID, startRFC, endRFC, cred.Login, cred.Password)
			if err != nil {
				deps.logger.Error("bookFreeCourts: book court failed",
					"venue_id", venue.ID, "court_uuid", courtUUID, "login", cred.Login, "err", err)
				if markErr := deps.credService.MarkError(ctx, cred.ID); markErr != nil {
					deps.logger.Error("bookFreeCourts: mark credential error", "cred_id", cred.ID, "err", markErr)
				}
				result.Failures = append(result.Failures, BookingFailure{
					Reason: fmt.Sprintf("credential %s: %v", cred.Login, err),
				})
				// Put court back for the next credential.
				available = append([]string{courtUUID}, available...)
				break
			}

			label := ""
			if l, ok := uuidToCourtNum[courtUUID]; ok {
				label = l
			}
			result.BookedLabels = append(result.BookedLabels, label)
			remaining--

			if deps.auditSvc != nil {
				deps.auditSvc.RecordCourtBooked(ctx, venue.ID, chatID, venue.Name, label, gameDate)
			}

			if deps.courtBookingRepo != nil && bookResult.MatchID != "" {
				cb := &models.CourtBooking{
					VenueID:      venue.ID,
					GameDate:     gameDate,
					GameTime:     gameTime,
					CourtUUID:    courtUUID,
					CourtLabel:   label,
					MatchID:      bookResult.MatchID,
					BookingUUID:  bookResult.BookingUUID,
					CredentialID: &cred.ID,
				}
				if saveErr := deps.courtBookingRepo.Save(ctx, cb); saveErr != nil {
					deps.logger.Error("bookFreeCourts: save court booking",
						"venue_id", venue.ID, "match_id", bookResult.MatchID, "err", saveErr)
				} else {
					result.BookedCourtBookings = append(result.BookedCourtBookings, cb)
				}
			} else if bookResult.MatchID == "" {
				deps.logger.Warn("bookFreeCourts: match_id empty after booking, court booking record not saved",
					"venue_id", venue.ID, "court_uuid", courtUUID)
			}
		}
	}

	if remaining > 0 && len(available) > 0 {
		result.Failures = append(result.Failures, BookingFailure{
			Reason: fmt.Sprintf("credentials exhausted: booked %d of %d", count-remaining, count),
		})
	}

	return result, nil
}
