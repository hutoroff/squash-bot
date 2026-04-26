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
// Standard squash slot at this facility is 45 minutes.
const autoBookingCourtDuration = 45 * time.Minute

// AutoBookingJob attempts to automatically book courts for upcoming game days
// when booking opens. Fires in the [00:00, 00:05) window of each group's timezone
// on configured game days. Requires bookingClient to be non-nil.
type AutoBookingJob struct {
	api                   TelegramAPI
	groupRepo             GroupRepository
	venueRepo             VenueRepository
	bookingClient         BookingServiceClient
	credService           *VenueCredentialService
	autoBookingResultRepo AutoBookingResultRepository
	courtBookingRepo      CourtBookingRepository
	auditSvc              *AuditService
	loc          *time.Location
	logger       *slog.Logger
	credCooldown time.Duration
}

func NewAutoBookingJob(
	api TelegramAPI,
	groupRepo GroupRepository,
	venueRepo VenueRepository,
	bookingClient BookingServiceClient,
	credService *VenueCredentialService,
	autoBookingResultRepo AutoBookingResultRepository,
	courtBookingRepo CourtBookingRepository,
	auditSvc *AuditService,
	loc *time.Location,
	logger *slog.Logger,
	credCooldown time.Duration,
) *AutoBookingJob {
	return &AutoBookingJob{
		api:                   api,
		groupRepo:             groupRepo,
		venueRepo:             venueRepo,
		bookingClient:         bookingClient,
		credService:           credService,
		autoBookingResultRepo: autoBookingResultRepo,
		courtBookingRepo:      courtBookingRepo,
		auditSvc:              auditSvc,
		loc:                   loc,
		logger:                logger,
		credCooldown:          credCooldown,
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

		lz := i18n.New(i18n.Normalize(g.Language))

		for _, venue := range venues {
			if venue.GameDays == "" || venue.PreferredGameTimes == "" || venue.Courts == "" {
				continue
			}
			if !containsDay(venue.GameDays, int(localNow.AddDate(0, 0, venue.BookingOpensDays).Weekday())) {
				continue
			}

			if j.processAutoBookingForVenue(ctx, g.ChatID, venue, localNow, groupTZ, lz) {
				booked++
			}
		}
	}
	j.logger.Info("auto-booking done", "venues_booked", booked)
}

// processAutoBookingForVenue attempts to book courts for each configured time slot of a venue.
// Returns true if at least one time slot had courts booked (triggers last_auto_booking_at update).
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
	courtsCount := len(parseCourtIDs(venue.AutoBookingCourts))
	if courtsCount == 0 {
		j.logger.Warn("auto-booking: skipping venue with no auto_booking_courts configured", "venue_id", venue.ID)
		return false
	}
	gameDate := localNow.AddDate(0, 0, venue.BookingOpensDays)
	gameDateStr := fmt.Sprintf("%d-%02d-%02d", gameDate.Year(), gameDate.Month(), gameDate.Day())

	times := splitPreferredTimes(venue.PreferredGameTimes)
	if len(times) == 0 {
		return false
	}

	anyBooked := false
	for _, gameTime := range times {
		// Per-slot dedup: skip if a result already exists for this exact (venue, date, time).
		existing, err := j.autoBookingResultRepo.GetByVenueAndDateAndTime(ctx, venue.ID, gameDate, gameTime)
		if err != nil {
			j.logger.Error("auto-booking: check slot dedup, skipping slot", "venue_id", venue.ID, "time", gameTime, "err", err)
			continue // conservative: skip rather than risk double-booking on transient DB error
		}
		if existing != nil {
			j.logger.Info("auto-booking: slot already done, skipping", "venue_id", venue.ID, "time", gameTime)
			continue
		}

		if j.processTimeSlot(ctx, chatID, venue, gameDate, gameDateStr, gameTime, groupTZ, lz, courtsCount) {
			anyBooked = true
		}
	}

	if anyBooked {
		if err := j.venueRepo.SetLastAutoBookingAt(ctx, venue.ID); err != nil {
			j.logger.Error("auto-booking: update last auto booking at", "venue_id", venue.ID, "err", err)
		}
	}
	return anyBooked
}

// processTimeSlot books courts for a single (venue, date, time) combination.
// Returns true if at least one court was successfully booked.
//
// Algorithm:
//  1. Call ListCourts to get all courts at the facility for the game date.
//  2. Call ListMatches for the exact target start time — courts appearing in the
//     response are occupied (reserved, training, club-blocked). Courts ABSENT
//     from the response are truly free for ad-hoc booking.
//  3. Book free courts up to len(venue.AutoBookingCourts) using their UUID from step 1.
func (j *AutoBookingJob) processTimeSlot(
	ctx context.Context,
	chatID int64,
	venue *models.Venue,
	gameDate time.Time,
	gameDateStr string,
	gameTime string,
	groupTZ *time.Location,
	lz *i18n.Localizer,
	courtsCount int,
) bool {
	// Parse "HH:MM" into a concrete time.Time for booking.
	gameStart, err := parsePreferredTime(gameDateStr, gameTime, groupTZ)
	if err != nil {
		j.logger.Error("auto-booking: parse preferred time",
			"venue_id", venue.ID, "time", gameTime, "err", err)
		return false
	}

	j.logger.Debug("auto-booking: game time resolved",
		"venue_id", venue.ID,
		"game_date", gameDateStr,
		"game_time", gameTime,
		"game_start", gameStart.Format(time.RFC3339),
		"game_start_tz", gameStart.Location().String(),
	)

	checkDateLocal := gameStart.Format("2006-01-02")
	checkStartHHMM := gameStart.Format("1504")

	j.logger.Debug("auto-booking: querying availability",
		"venue_id", venue.ID,
		"date", checkDateLocal, "start_hhmm", checkStartHHMM,
	)

	// Load credentials before any Eversports calls so we can bail out early.
	creds, err := j.credService.ListForBooking(ctx, venue.ID, j.credCooldown)
	if err != nil {
		j.logger.Error("auto-booking: list credentials", "venue_id", venue.ID, "err", err)
		j.notifyAutoBookingFailure(ctx, chatID, venue, gameDateStr, gameTime, 0, courtsCount, lz)
		return false
	}
	if len(creds) == 0 {
		j.logger.Warn("auto-booking: no usable credentials", "venue_id", venue.ID)
		j.notifyNoCredentials(ctx, chatID, venue, lz)
		return false
	}
	firstLogin, firstPassword := creds[0].Login, creds[0].Password

	// Step 1: Fetch all courts at the facility for the game date.
	allCourts, err := j.bookingClient.ListCourts(ctx, checkDateLocal, firstLogin, firstPassword)
	if err != nil {
		j.logger.Error("auto-booking: list courts",
			"venue_id", venue.ID, "date", checkDateLocal, "err", err)
		j.notifyAutoBookingFailure(ctx, chatID, venue, gameDateStr, gameTime, 0, courtsCount, lz)
		return false
	}
	j.logger.Debug("auto-booking: courts fetched",
		"venue_id", venue.ID, "date", checkDateLocal, "count", len(allCourts))

	// Step 2: Fetch matches at target start time; courts in this response are occupied.
	occupiedSlots, err := j.bookingClient.ListMatches(ctx, checkDateLocal, checkStartHHMM, checkStartHHMM, false, firstLogin, firstPassword)
	if err != nil {
		j.logger.Error("auto-booking: list matches",
			"venue_id", venue.ID, "date", checkDateLocal, "time", checkStartHHMM, "err", err)
		j.notifyAutoBookingFailure(ctx, chatID, venue, gameDateStr, gameTime, 0, courtsCount, lz)
		return false
	}
	j.logger.Debug("auto-booking: matches at target time",
		"venue_id", venue.ID, "date", checkDateLocal, "time", checkStartHHMM,
		"occupied_count", len(occupiedSlots))

	occupied := make(map[int]bool, len(occupiedSlots))
	for _, sl := range occupiedSlots {
		occupied[sl.Court] = true
		j.logger.Debug("auto-booking: occupied court",
			"venue_id", venue.ID, "court", sl.Court,
			"booked", sl.Booking != nil, "present", sl.Present, "title", sl.Title)
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

	j.logger.Debug("auto-booking: courts selected for booking",
		"venue_id", venue.ID,
		"venue_courts_config", venue.Courts,
		"auto_booking_courts_config", venue.AutoBookingCourts,
		"available_count", len(available),
		"available_uuids", available,
	)

	if len(available) == 0 {
		j.logger.Info("auto-booking: no available courts",
			"venue_id", venue.ID, "date", gameDateStr, "time", gameTime,
			"total_courts", len(allCourts), "occupied", len(occupiedSlots))
		j.notifyAutoBookingFailure(ctx, chatID, venue, gameDateStr, gameTime, 0, courtsCount, lz)
		return false
	}

	gameEnd := gameStart.Add(autoBookingCourtDuration)
	startRFC := gameStart.Format(time.RFC3339)
	endRFC := gameEnd.Format(time.RFC3339)

	j.logger.Debug("auto-booking: booking params",
		"venue_id", venue.ID,
		"start_rfc", startRFC,
		"end_rfc", endRFC,
		"courts_target", courtsCount,
	)

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

	// ── Credential-rotation booking loop ─────────────────────────────────────

	remaining := courtsCount
	bookedCount := 0
	var bookedCourtLabels []string

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

			j.logger.Debug("auto-booking: attempting court",
				"venue_id", venue.ID,
				"court_uuid", courtUUID,
				"login", cred.Login,
				"start", startRFC,
				"end", endRFC,
			)
			bookResult, err := j.bookingClient.BookMatch(ctx, courtUUID, startRFC, endRFC, cred.Login, cred.Password)
			if err != nil {
				j.logger.Error("auto-booking: book court failed",
					"venue_id", venue.ID, "court_uuid", courtUUID, "login", cred.Login,
					"start", startRFC, "end", endRFC, "err", err)
				if markErr := j.credService.MarkError(ctx, cred.ID); markErr != nil {
					j.logger.Error("auto-booking: mark credential error", "cred_id", cred.ID, "err", markErr)
				}
				j.notifyCredentialError(ctx, chatID, venue, cred.Login, err, j.credCooldown, lz)
				available = append([]string{courtUUID}, available...)
				break
			}
			j.logger.Info("auto-booking: court booked",
				"venue_id", venue.ID, "court_uuid", courtUUID, "login", cred.Login,
				"date", gameDateStr, "time", gameTime)
			bookedCount++
			remaining--
			label := ""
			if l, ok := uuidToCourtNum[courtUUID]; ok {
				label = l
				bookedCourtLabels = append(bookedCourtLabels, label)
			}
			if j.auditSvc != nil {
				j.auditSvc.RecordCourtBooked(ctx, venue.ID, chatID, venue.Name, label, gameDate)
			}
			if j.courtBookingRepo != nil && bookResult.MatchID != "" {
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
				if saveErr := j.courtBookingRepo.Save(ctx, cb); saveErr != nil {
					j.logger.Error("auto-booking: save court booking",
						"venue_id", venue.ID, "match_id", bookResult.MatchID, "err", saveErr)
				}
			} else if bookResult.MatchID == "" {
				j.logger.Warn("auto-booking: match_id empty after booking, court booking record not saved",
					"venue_id", venue.ID, "court_uuid", courtUUID, "booking_uuid", bookResult.BookingUUID)
			}
		}
	}

	if remaining > 0 && len(available) > 0 {
		j.notifyCredentialsExhausted(ctx, chatID, venue, bookedCount, courtsCount, lz)
	}

	if bookedCount == 0 {
		return false
	}

	// Persist result for BookingReminderJob to consume at 10:00.
	courtsStr := strings.Join(bookedCourtLabels, ",")
	if err := j.autoBookingResultRepo.Save(ctx, venue.ID, gameDate, gameTime, courtsStr, bookedCount); err != nil {
		j.logger.Error("auto-booking: save result", "venue_id", venue.ID, "time", gameTime, "err", err)
	}

	j.notifyAutoBookingSuccess(ctx, chatID, venue, gameDateStr, gameTime, bookedCount, lz)
	return true
}

// splitPreferredTimes splits a comma-separated preferred times string and returns
// non-empty trimmed entries.
func splitPreferredTimes(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// notifyAutoBookingSuccess DMs all group admins about a successful auto-booking.
// Messages are sent silently (DisableNotification=true).
func (j *AutoBookingJob) notifyAutoBookingSuccess(
	ctx context.Context,
	chatID int64,
	venue *models.Venue,
	gameDateStr, preferredHHMM string,
	bookedCount int,
	lz *i18n.Localizer,
) {
	admins, err := j.api.GetChatAdministrators(tgbotapi.ChatAdministratorsConfig{
		ChatConfig: tgbotapi.ChatConfig{ChatID: chatID},
	})
	if err != nil {
		j.logger.Error("auto-booking: get chat administrators", "chat_id", chatID, "err", err)
		return
	}

	text := lz.Tf(i18n.SchedAutoBookingSuccess, bookedCount, venue.Name, gameDateStr, preferredHHMM)
	seen := make(map[int64]bool)
	for _, admin := range admins {
		if admin.User.IsBot || seen[admin.User.ID] {
			continue
		}
		seen[admin.User.ID] = true
		msg := tgbotapi.NewMessage(admin.User.ID, text)
		msg.DisableNotification = true
		if _, err := j.api.Send(msg); err != nil {
			j.logger.Error("auto-booking: send success DM",
				"user_id", admin.User.ID, "venue_id", venue.ID, "err", err)
			continue
		}
		j.logger.Info("auto-booking: success DM sent", "user_id", admin.User.ID, "venue_id", venue.ID)
	}
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

// notifyNoCredentials DMs all group admins WITH notification when no usable credentials exist.
func (j *AutoBookingJob) notifyNoCredentials(
	ctx context.Context,
	chatID int64,
	venue *models.Venue,
	lz *i18n.Localizer,
) {
	admins, err := j.api.GetChatAdministrators(tgbotapi.ChatAdministratorsConfig{
		ChatConfig: tgbotapi.ChatConfig{ChatID: chatID},
	})
	if err != nil {
		j.logger.Error("auto-booking: get chat administrators", "chat_id", chatID, "err", err)
		return
	}
	text := lz.Tf(i18n.SchedAutoBookingNoCredentials, venue.Name)
	seen := make(map[int64]bool)
	for _, admin := range admins {
		if admin.User.IsBot || seen[admin.User.ID] {
			continue
		}
		seen[admin.User.ID] = true
		msg := tgbotapi.NewMessage(admin.User.ID, text)
		msg.ParseMode = "Markdown"
		if _, err := j.api.Send(msg); err != nil {
			j.logger.Error("auto-booking: send no-credentials DM",
				"user_id", admin.User.ID, "venue_id", venue.ID, "err", err)
		}
	}
}

// notifyCredentialError DMs all group admins WITH notification when a credential fails.
func (j *AutoBookingJob) notifyCredentialError(
	ctx context.Context,
	chatID int64,
	venue *models.Venue,
	login string,
	bookingErr error,
	cooldown time.Duration,
	lz *i18n.Localizer,
) {
	admins, err := j.api.GetChatAdministrators(tgbotapi.ChatAdministratorsConfig{
		ChatConfig: tgbotapi.ChatConfig{ChatID: chatID},
	})
	if err != nil {
		j.logger.Error("auto-booking: get chat administrators", "chat_id", chatID, "err", err)
		return
	}
	text := lz.Tf(i18n.SchedAutoBookingCredError, venue.Name, login, bookingErr.Error(), cooldown.String())
	seen := make(map[int64]bool)
	for _, admin := range admins {
		if admin.User.IsBot || seen[admin.User.ID] {
			continue
		}
		seen[admin.User.ID] = true
		msg := tgbotapi.NewMessage(admin.User.ID, text)
		msg.ParseMode = "Markdown"
		if _, err := j.api.Send(msg); err != nil {
			j.logger.Error("auto-booking: send credential-error DM",
				"user_id", admin.User.ID, "venue_id", venue.ID, "err", err)
		}
	}
}

// notifyCredentialsExhausted DMs all group admins silently when all credentials
// have been tried but courts remain unbooked.
func (j *AutoBookingJob) notifyCredentialsExhausted(
	ctx context.Context,
	chatID int64,
	venue *models.Venue,
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
	text := lz.Tf(i18n.SchedAutoBookingCredExhausted, venue.Name, bookedCount, targetCount)
	seen := make(map[int64]bool)
	for _, admin := range admins {
		if admin.User.IsBot || seen[admin.User.ID] {
			continue
		}
		seen[admin.User.ID] = true
		msg := tgbotapi.NewMessage(admin.User.ID, text)
		msg.ParseMode = "Markdown"
		msg.DisableNotification = true
		if _, err := j.api.Send(msg); err != nil {
			j.logger.Error("auto-booking: send credentials-exhausted DM",
				"user_id", admin.User.ID, "venue_id", venue.ID, "err", err)
		}
	}
}

// filterFreeCourts returns the UUIDs of courts that are free at the target time.
// allCourts is the full court list from ListCourts.
// occupied is the set of Eversports court IDs that appeared in the ListMatches response —
// any court in this set is not free (reserved, training, club-blocked).
// venueCourts and orderedPreferred are keyed by the sequential court number extracted
// from the court name (e.g. "Court 7" → 7), which aligns with how users configure
// venue.Courts and venue.AutoBookingCourts.
// If none of the courts' name-numbers match venueCourts, the filter is skipped and all
// free courts are used. orderedPreferred falls back the same way if nothing matches.
func filterFreeCourts(allCourts []BookingCourt, occupied map[int]bool, venueCourts map[int]bool, orderedPreferred []int) []string {
	allFree := make(map[int]string)   // courtNum → UUID, all free courts
	venueFree := make(map[int]string) // courtNum → UUID, free courts matching venueCourts

	for _, c := range allCourts {
		// Occupancy check uses the Eversports numeric court ID (matches sl.Court from ListMatches).
		courtID, err := strconv.Atoi(c.ID)
		if err != nil || c.UUID == "" {
			continue
		}
		if occupied[courtID] {
			continue
		}
		// Venue filtering and priority matching use the number in the court name
		// (e.g. "Court 7" → 7), which matches what users store in venue.Courts and
		// venue.AutoBookingCourts.
		courtNum := extractCourtNumber(c.Name)
		if courtNum <= 0 {
			continue
		}
		allFree[courtNum] = c.UUID
		if venueCourts[courtNum] {
			venueFree[courtNum] = c.UUID
		}
	}

	// Use venue-scoped courts when at least one matched; fall back to all free courts
	// when none of the name-numbers match the configured venue court numbers.
	courtUUIDs := venueFree
	if len(courtUUIDs) == 0 {
		courtUUIDs = allFree
	}

	if len(orderedPreferred) > 0 {
		// Ordered subset mode: emit only preferred courts in declared priority order.
		var result []string
		for _, courtNum := range orderedPreferred {
			if uuid, ok := courtUUIDs[courtNum]; ok {
				result = append(result, uuid)
			}
		}
		// If no preferred number matched, fall through to emit all eligible courts.
		if len(result) > 0 {
			return result
		}
	}

	// Emit all eligible courts in API response order (preserves facility ordering).
	seen := make(map[int]bool)
	var result []string
	for _, c := range allCourts {
		courtID, _ := strconv.Atoi(c.ID)
		if occupied[courtID] {
			continue
		}
		courtNum := extractCourtNumber(c.Name)
		if _, ok := courtUUIDs[courtNum]; ok && !seen[courtNum] {
			seen[courtNum] = true
			result = append(result, courtUUIDs[courtNum])
		}
	}
	return result
}

// extractCourtNumber extracts the trailing integer from a court name like "Court 7".
// Returns -1 if the name is empty or its last word is not a positive integer.
func extractCourtNumber(name string) int {
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return -1
	}
	n, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil || n <= 0 {
		return -1
	}
	return n
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
// Used by the cancellation reminder to query a ±10 min window around the game time.
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
