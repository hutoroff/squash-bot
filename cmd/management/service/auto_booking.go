package service

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/models"
)

// autoBookingCourtDuration is the duration of a court booking created automatically.
// Standard squash booking is 1 hour.
const autoBookingCourtDuration = 60 * time.Minute

// AutoBookingJob attempts to automatically book courts for upcoming game days
// when booking opens. Fires in the [00:00, 00:05) window of each group's timezone
// on configured game days. Requires bookingClient to be non-nil.
type AutoBookingJob struct {
	api           TelegramAPI
	groupRepo     GroupRepository
	venueRepo     VenueRepository
	bookingClient BookingServiceClient
	loc           *time.Location
	logger        *slog.Logger
	courtsCount   int // number of courts to book automatically
}

func NewAutoBookingJob(
	api TelegramAPI,
	groupRepo GroupRepository,
	venueRepo VenueRepository,
	bookingClient BookingServiceClient,
	loc *time.Location,
	logger *slog.Logger,
	courtsCount int,
) *AutoBookingJob {
	return &AutoBookingJob{
		api:           api,
		groupRepo:     groupRepo,
		venueRepo:     venueRepo,
		bookingClient: bookingClient,
		loc:           loc,
		logger:        logger,
		courtsCount:   courtsCount,
	}
}

func (j *AutoBookingJob) name() string   { return "auto_booking" }
func (j *AutoBookingJob) run(force bool) { j.runAutoBooking(force) }

func (j *AutoBookingJob) runAutoBooking(force bool) {
	if j.bookingClient == nil {
		return
	}
	j.logger.Info("auto-booking check started")
	ctx := context.Background()
	now := time.Now()

	groups, err := j.groupRepo.GetAll(ctx)
	if err != nil {
		j.logger.Error("auto-booking: get groups", "err", err)
		return
	}

	booked := 0
	for _, g := range groups {
		groupTZ := resolveGroupTimezone(&g, j.loc, j.logger)
		localNow := now.In(groupTZ)

		// Only fire in the [00:00, 00:05) window in the group's local time.
		if !force && (localNow.Hour() != 0 || localNow.Minute() >= 5) {
			continue
		}

		venues, err := j.venueRepo.GetByGroupID(ctx, g.ChatID)
		if err != nil {
			j.logger.Error("auto-booking: get venues", "chat_id", g.ChatID, "err", err)
			continue
		}

		todayStr := localNow.Format("2006-01-02")
		lz := i18n.New(i18n.Normalize(g.Language))

		for _, venue := range venues {
			if venue.GameDays == "" || venue.PreferredGameTime == "" || venue.Courts == "" {
				continue
			}
			if !containsDay(venue.GameDays, int(localNow.AddDate(0, 0, venue.BookingOpensDays).Weekday())) {
				continue
			}
			// Dedup: skip if already booked today in this group's timezone.
			if venue.LastAutoBookingAt != nil &&
				venue.LastAutoBookingAt.In(groupTZ).Format("2006-01-02") == todayStr {
				j.logger.Info("auto-booking: already done today", "venue_id", venue.ID)
				continue
			}

			if j.processAutoBookingForVenue(ctx, g.ChatID, venue, localNow, groupTZ, lz) {
				booked++
			}
		}
	}
	j.logger.Info("auto-booking done", "venues_booked", booked)
}

// processAutoBookingForVenue attempts to book courts for a single venue.
// Returns true if the auto-booking timestamp should be updated (at least one court booked).
func (j *AutoBookingJob) processAutoBookingForVenue(
	ctx context.Context,
	chatID int64,
	venue *models.Venue,
	localNow time.Time,
	groupTZ *time.Location,
	lz *i18n.Localizer,
) bool {
	if !venue.AutoBookingEnabled {
		return false
	}
	// Game date = today + BookingOpensDays in group timezone.
	gameDate := localNow.AddDate(0, 0, venue.BookingOpensDays)
	gameDateStr := fmt.Sprintf("%d-%02d-%02d", gameDate.Year(), gameDate.Month(), gameDate.Day())

	// Parse preferred game time "HH:MM" into a concrete time.Time for booking.
	gameStart, err := parsePreferredTime(gameDateStr, venue.PreferredGameTime, groupTZ)
	if err != nil {
		j.logger.Error("auto-booking: parse preferred time",
			"venue_id", venue.ID, "preferred_time", venue.PreferredGameTime, "err", err)
		return false
	}

	// Time window for availability check: preferred time +0/+10 min (narrow window).
	checkDateLocal, checkStartHHMM, checkEndHHMM := slotQueryWindow(gameStart)

	// Fetch all non-user-owned slots at the preferred time to find available courts.
	slots, err := j.bookingClient.ListMatches(ctx, checkDateLocal, checkStartHHMM, checkEndHHMM, false)
	if err != nil {
		j.logger.Error("auto-booking: list available slots",
			"venue_id", venue.ID, "date", checkDateLocal, "err", err)
		j.notifyAutoBookingFailure(ctx, chatID, venue, gameDateStr, venue.PreferredGameTime, 0, j.courtsCount, lz)
		return false
	}

	j.logger.Debug("auto-booking: slots received",
		"venue_id", venue.ID, "date", checkDateLocal, "start", checkStartHHMM, "end", checkEndHHMM,
		"count", len(slots))

	// Build the set of court IDs configured for this venue.
	venueCourts := make(map[int]bool)
	for _, c := range strings.Split(venue.Courts, ",") {
		if t := strings.TrimSpace(c); t != "" {
			if id, err := strconv.Atoi(t); err == nil {
				venueCourts[id] = true
			}
		}
	}

	// Parse the ordered preference list (empty = no preference, all venue courts eligible).
	orderedPreferred := parseCourtIDs(venue.AutoBookingCourts)

	// Collect available (unbooked) court UUIDs restricted to venue courts, in priority order.
	available := filterAvailableCourts(slots, venueCourts, orderedPreferred)

	if len(available) == 0 {
		if j.logger.Enabled(ctx, slog.LevelDebug) {
			// Log a breakdown to help diagnose why no courts are available.
			var nBooked, nNoUUID, nNotInVenue int
			for _, sl := range slots {
				if sl.Booking != nil {
					nBooked++
				} else if sl.CourtUUID == "" {
					nNoUUID++
				} else if !venueCourts[sl.Court] {
					nNotInVenue++
				}
			}
			j.logger.Info("auto-booking: no available courts",
				"venue_id", venue.ID, "date", gameDateStr, "time", venue.PreferredGameTime,
				"slots_total", len(slots), "booked", nBooked, "no_uuid", nNoUUID, "not_in_venue", nNotInVenue,
				"venue_courts", venue.Courts, "auto_booking_courts", venue.AutoBookingCourts)
		}
		j.notifyAutoBookingFailure(ctx, chatID, venue, gameDateStr, venue.PreferredGameTime, 0, j.courtsCount, lz)
		return false
	}

	// Book up to courtsCount courts.
	target := j.courtsCount
	if target > len(available) {
		target = len(available)
	}

	gameEnd := gameStart.Add(autoBookingCourtDuration)
	startRFC := gameStart.UTC().Format(time.RFC3339)
	endRFC := gameEnd.UTC().Format(time.RFC3339)

	bookedCount := 0
	for i := 0; i < target; i++ {
		courtUUID := available[i]
		if _, err := j.bookingClient.BookMatch(ctx, courtUUID, startRFC, endRFC); err != nil {
			j.logger.Error("auto-booking: book court failed",
				"venue_id", venue.ID, "court_uuid", courtUUID, "err", err)
			continue
		}
		j.logger.Info("auto-booking: court booked",
			"venue_id", venue.ID, "court_uuid", courtUUID, "date", gameDateStr, "time", venue.PreferredGameTime)
		bookedCount++
	}

	if bookedCount == 0 {
		j.notifyAutoBookingFailure(ctx, chatID, venue, gameDateStr, venue.PreferredGameTime, 0, j.courtsCount, lz)
		return false
	}

	// Set dedup timestamp before sending notifications (so partial success is still recorded).
	if err := j.venueRepo.SetLastAutoBookingAt(ctx, venue.ID); err != nil {
		j.logger.Error("auto-booking: update last auto booking at", "venue_id", venue.ID, "err", err)
	}

	// Notify the group about the successful auto-booking.
	text := lz.Tf(i18n.SchedAutoBookingSuccess, bookedCount, venue.Name, gameDateStr, venue.PreferredGameTime)
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := j.api.Send(msg); err != nil {
		j.logger.Error("auto-booking: send group notification",
			"venue_id", venue.ID, "chat_id", chatID, "err", err)
	}

	// If fewer courts were booked than requested, notify admins about the shortfall.
	if bookedCount < j.courtsCount {
		j.notifyAutoBookingFailure(ctx, chatID, venue, gameDateStr, venue.PreferredGameTime, bookedCount, j.courtsCount, lz)
	}

	return true
}

// notifyAutoBookingFailure DMs all group admins about an auto-booking failure or partial success.
// Messages are sent silently (DisableNotification=true).
func (j *AutoBookingJob) notifyAutoBookingFailure(
	ctx context.Context,
	chatID int64,
	venue *models.Venue,
	gameDateStr, preferredHHMM string,
	bookedCount, targetCount int,
	lz *i18n.Localizer,
) {
	admins, err := j.api.GetChatAdministrators(tgbotapi.ChatAdministratorsConfig{
		ChatConfig: tgbotapi.ChatConfig{ChatID: chatID},
	})
	if err != nil {
		j.logger.Error("auto-booking: get chat administrators", "chat_id", chatID, "err", err)
		return
	}

	text := lz.Tf(i18n.SchedAutoBookingFailDM, venue.Name, gameDateStr, preferredHHMM, bookedCount, targetCount)
	seen := make(map[int64]bool)
	for _, admin := range admins {
		if admin.User.IsBot || seen[admin.User.ID] {
			continue
		}
		seen[admin.User.ID] = true
		msg := tgbotapi.NewMessage(admin.User.ID, text)
		msg.DisableNotification = true
		if _, err := j.api.Send(msg); err != nil {
			j.logger.Error("auto-booking: send failure DM",
				"user_id", admin.User.ID, "venue_id", venue.ID, "err", err)
			continue
		}
		j.logger.Info("auto-booking: failure DM sent", "user_id", admin.User.ID, "venue_id", venue.ID)
	}
}

// filterAvailableCourts returns the UUIDs of available (unbooked) courts from
// slots, restricted to venue courts. Each court is represented at most once.
//
// orderedPreferred, if non-empty, defines both the eligible subset and the
// booking priority order. When empty all venue courts are eligible and results
// follow the API response order.
func filterAvailableCourts(slots []BookingSlot, venueCourts map[int]bool, orderedPreferred []int) []string {
	// Build courtID → UUID map for all available venue courts (first UUID wins).
	courtUUIDs := make(map[int]string)
	for _, sl := range slots {
		if sl.Booking == nil && sl.CourtUUID != "" && venueCourts[sl.Court] {
			if _, seen := courtUUIDs[sl.Court]; !seen {
				courtUUIDs[sl.Court] = sl.CourtUUID
			}
		}
	}

	if len(orderedPreferred) > 0 {
		// Ordered subset mode: emit only preferred courts, in declared order.
		var result []string
		for _, courtID := range orderedPreferred {
			if uuid, ok := courtUUIDs[courtID]; ok {
				result = append(result, uuid)
			}
		}
		return result
	}

	// No preference: emit all available venue courts in API response order.
	seen := make(map[int]bool)
	var result []string
	for _, sl := range slots {
		if _, ok := courtUUIDs[sl.Court]; ok && !seen[sl.Court] {
			seen[sl.Court] = true
			result = append(result, courtUUIDs[sl.Court])
		}
	}
	return result
}

// parseCourtIDs splits a comma-separated court ID string (e.g. "5,6,7") into a slice of ints.
// Invalid tokens are silently skipped.
func parseCourtIDs(s string) []int {
	if s == "" {
		return nil
	}
	var ids []int
	for _, part := range strings.Split(s, ",") {
		if id, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// slotQueryWindow returns the date (YYYY-MM-DD), startHHMM, and endHHMM (start+10 min)
// parameters for a BookingServiceClient.ListMatches call targeting gameStart.
// All values are in the timezone carried by gameStart.
func slotQueryWindow(gameStart time.Time) (date, startHHMM, endHHMM string) {
	return gameStart.Format("2006-01-02"),
		gameStart.Format("1504"),
		gameStart.Add(10 * time.Minute).Format("1504")
}

// parsePreferredTime parses a "HH:MM" preferred time and "YYYY-MM-DD" date string
// in the given timezone into a concrete time.Time for booking.
func parsePreferredTime(gameDateStr, preferredTime string, loc *time.Location) (time.Time, error) {
	parts := strings.SplitN(preferredTime, ":", 2)
	if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return time.Time{}, fmt.Errorf("invalid preferred time format %q, expected HH:MM", preferredTime)
	}

	dt, err := time.ParseInLocation("2006-01-02 15:04", gameDateStr+" "+preferredTime, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse game datetime: %w", err)
	}
	return dt, nil
}
