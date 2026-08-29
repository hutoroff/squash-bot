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
	"github.com/hutoroff/squash-bot/internal/sport"
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
	gameRepo              GameRepository
	bookingClient         BookingServiceClient
	credService           *VenueCredentialService
	autoBookingResultRepo AutoBookingResultRepository
	courtBookingRepo      CourtBookingRepository
	auditSvc              *AuditService
	loc                   *time.Location
	logger                *slog.Logger
	credCooldown          time.Duration
}

func NewAutoBookingJob(
	api TelegramAPI,
	groupRepo GroupRepository,
	venueRepo VenueRepository,
	gameRepo GameRepository,
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
		gameRepo:              gameRepo,
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
	j.logger.Debug("auto-booking check started")
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

		if !g.AutoBookingAllowed {
			continue
		}

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
	j.logger.Debug("auto-booking done", "venues_booked", booked)
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
	courtsCount := venue.AutoBookingCourtsCount
	if courtsCount == 0 {
		j.logger.Warn("auto-booking: skipping venue with no courts per game configured", "venue_id", venue.ID)
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
// Algorithm delegates to bookFreeCourts (steps 1–5), then handles notifications
// and result persistence which must stay in this function.
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
	deps := bookingDeps{
		bookingClient:    j.bookingClient,
		credService:      j.credService,
		courtBookingRepo: j.courtBookingRepo,
		auditSvc:         j.auditSvc,
		credCooldown:     j.credCooldown,
		logger:           j.logger,
	}

	res, err := bookFreeCourts(ctx, deps, venue, chatID, gameDate, gameDateStr, gameTime, groupTZ, courtsCount)
	if err != nil {
		j.logger.Error("auto-booking: bookFreeCourts failed", "venue_id", venue.ID, "time", gameTime, "err", err)
		j.notifyAutoBookingFailure(ctx, chatID, venue, gameDateStr, gameTime, 0, courtsCount, lz)
		return false
	}

	if res == nil {
		return false
	}

	// No credentials available.
	if len(res.BookedLabels) == 0 && len(res.Failures) == 0 {
		j.logger.Warn("auto-booking: no usable credentials", "venue_id", venue.ID)
		j.notifyNoCredentials(ctx, chatID, venue, lz)
		return false
	}

	// Check if failures contain a credential error that warrants per-cred notifications.
	// bookFreeCourts already called MarkError for each failing cred.
	// We need to surface notifyCredentialError for the first cred-specific failure
	// and notifyCredentialsExhausted for remaining capacity failures.
	// For backward compat, map Failures → existing notify functions.
	for _, f := range res.Failures {
		if strings.Contains(f.Reason, "credential ") {
			// Extract login from "credential <login>: <err>" prefix.
			rest := strings.TrimPrefix(f.Reason, "credential ")
			colonIdx := strings.Index(rest, ":")
			login := rest
			var bookErr error
			if colonIdx >= 0 {
				login = rest[:colonIdx]
				bookErr = fmt.Errorf("%s", strings.TrimSpace(rest[colonIdx+1:]))
			} else {
				bookErr = fmt.Errorf("%s", rest)
			}
			j.notifyCredentialError(ctx, chatID, venue, login, bookErr, j.credCooldown, lz)
		} else if strings.Contains(f.Reason, "credentials exhausted") {
			bookedCount := len(res.BookedLabels)
			j.notifyCredentialsExhausted(ctx, chatID, venue, bookedCount, courtsCount, lz)
		} else {
			// No-free-courts or other failure.
			j.notifyAutoBookingFailure(ctx, chatID, venue, gameDateStr, gameTime, len(res.BookedLabels), courtsCount, lz)
		}
	}

	if len(res.BookedLabels) == 0 {
		return false
	}

	// Persist result and eagerly create the unpublished game record.
	courtsStr := strings.Join(res.BookedLabels, ",")
	bookedCount := len(res.BookedLabels)
	resultID, err := j.autoBookingResultRepo.Save(ctx, venue.ID, gameDate, gameTime, courtsStr, bookedCount)
	if err != nil {
		j.logger.Error("auto-booking: save result", "venue_id", venue.ID, "time", gameTime, "err", err)
	} else {
		j.createUnpublishedGame(ctx, chatID, venue, gameDate, gameTime, courtsStr, bookedCount, resultID, groupTZ, lz)
	}

	j.notifyAutoBookingSuccess(ctx, chatID, venue, gameDateStr, gameTime, bookedCount, lz)
	return true
}

// createUnpublishedGame creates a games row with message_id=NULL immediately after booking succeeds.
// On failure, DMs admins and logs the error; the booking itself is NOT rolled back.
// No-ops when gameRepo is nil (allows tests that don't need game creation to omit it).
func (j *AutoBookingJob) createUnpublishedGame(
	ctx context.Context,
	chatID int64,
	venue *models.Venue,
	gameDate time.Time,
	gameTime string,
	courts string,
	courtsCount int,
	resultID int64,
	groupTZ *time.Location,
	lz *i18n.Localizer,
) {
	if j.gameRepo == nil {
		return
	}

	// Combine gameDate + gameTime in the group timezone (mirrors booking_reminder.go).
	gameDateWithTime := gameDate
	if gameTime != "" {
		parts := strings.SplitN(gameTime, ":", 2)
		if len(parts) == 2 {
			h, errH := strconv.Atoi(parts[0])
			m, errM := strconv.Atoi(parts[1])
			if errH == nil && errM == nil {
				gameDateWithTime = time.Date(
					gameDate.Year(), gameDate.Month(), gameDate.Day(),
					h, m, 0, 0, groupTZ,
				)
			}
		}
	}

	venueID := venue.ID
	created, err := j.gameRepo.Create(ctx, &models.Game{
		ChatID:          chatID,
		GameDate:        gameDateWithTime,
		Courts:          courts,
		CourtsCount:     courtsCount,
		Sport:           string(sport.Default),
		PlayersPerCourt: playersPerCourtFor(venue, sport.Default),
		VenueID:         &venueID,
		// MessageID intentionally nil — game is unpublished until BookingReminderJob or manual publish.
	})
	if err != nil {
		j.logger.Error("auto-booking: create unpublished game", "venue_id", venue.ID, "time", gameTime, "err", err)
		j.notifyAutoBookingGameCreateFailed(ctx, chatID, venue, gameDate, gameTime, lz)
		return
	}

	if err := j.autoBookingResultRepo.SetGameID(ctx, resultID, created.ID); err != nil {
		j.logger.Error("auto-booking: set game_id on result",
			"result_id", resultID, "game_id", created.ID, "err", err)
		// Non-fatal: game exists; cancellation falls back to GetByVenueAndDate.
	}

	j.logger.Info("auto-booking: unpublished game created",
		"game_id", created.ID, "venue_id", venue.ID,
		"game_date", gameDate.Format(time.DateOnly), "game_time", gameTime)
}

func playersPerCourtFor(venue *models.Venue, name sport.Sport) int {
	for _, venueSport := range venue.Sports {
		if venueSport.Sport == string(name) && venueSport.PlayersPerCourt != nil {
			return *venueSport.PlayersPerCourt
		}
	}
	return sport.Get(name).DefaultPlayersPerCourt
}

// notifyAutoBookingGameCreateFailed DMs group admins when booking succeeded but game DB insert failed.
func (j *AutoBookingJob) notifyAutoBookingGameCreateFailed(
	ctx context.Context,
	chatID int64,
	venue *models.Venue,
	gameDate time.Time,
	gameTime string,
	lz *i18n.Localizer,
) {
	admins, err := j.api.GetChatAdministrators(tgbotapi.ChatAdministratorsConfig{
		ChatConfig: tgbotapi.ChatConfig{ChatID: chatID},
	})
	if err != nil {
		j.logger.Error("auto-booking: get chat administrators for game-create-failed DM", "chat_id", chatID, "err", err)
		return
	}

	text := lz.Tf(i18n.SchedAutoBookingGameCreateFailed, venue.Name, gameDate.Format(time.DateOnly), gameTime)
	seen := make(map[int64]bool)
	for _, admin := range admins {
		if admin.User.IsBot || seen[admin.User.ID] {
			continue
		}
		seen[admin.User.ID] = true
		msg := tgbotapi.NewMessage(admin.User.ID, text)
		msg.ParseMode = "Markdown"
		if _, err := j.api.Send(msg); err != nil {
			j.logger.Error("auto-booking: send game-create-failed DM",
				"user_id", admin.User.ID, "venue_id", venue.ID, "err", err)
		}
	}
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
