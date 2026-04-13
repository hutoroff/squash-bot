package telegram

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// callbackHandler is the function signature for all callback routing entries.
// rawID is everything after the first colon in cb.Data.
type callbackHandler func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string)

// buildCallbackRouter returns a map from callback action string to handler closure.
// It is called once during Bot construction and stored in b.callbackRouter.
func (b *Bot) buildCallbackRouter() map[string]callbackHandler {
	// int64H wraps a handler that expects a single parsed int64 payload.
	int64H := func(fn func(context.Context, *tgbotapi.CallbackQuery, int64)) callbackHandler {
		return func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
			id, err := strconv.ParseInt(rawID, 10, 64)
			if err != nil {
				slog.Debug("invalid int64 in callback", "data", cb.Data)
				b.answerCallback(cb.ID, "")
				return
			}
			fn(ctx, cb, id)
		}
	}

	// splitTwo splits rawID on the first colon into two parts.
	splitTwo := func(rawID, data string) (string, string, bool) {
		sub := strings.SplitN(rawID, ":", 2)
		if len(sub) != 2 {
			slog.Debug("invalid two-part callback format", "data", data)
			return "", "", false
		}
		return sub[0], sub[1], true
	}

	// parseVenueGroup parses a "venueID:groupID" rawID into two int64 values.
	parseVenueGroup := func(rawID, data string) (venueID, groupID int64, ok bool) {
		p1, p2, split := splitTwo(rawID, data)
		if !split {
			return 0, 0, false
		}
		v, err1 := strconv.ParseInt(p1, 10, 64)
		g, err2 := strconv.ParseInt(p2, 10, 64)
		if err1 != nil || err2 != nil {
			slog.Debug("invalid IDs in venue callback", "data", data)
			return 0, 0, false
		}
		return v, g, true
	}

	return map[string]callbackHandler{
		// ── Participation ─────────────────────────────────────────────────────────
		"join":         int64H(b.handleJoin),
		"skip":         int64H(b.handleSkip),
		"guest_add":    int64H(b.handleGuestAdd),
		"guest_remove": int64H(b.handleGuestRemove),

		// ── Game management ───────────────────────────────────────────────────────
		"manage":         int64H(b.handleManage),
		"manage_players": int64H(b.handleManageShowPlayers),
		"manage_guests":  int64H(b.handleManageShowGuests),
		"manage_courts":  int64H(b.handleManageEditCourts),
		"manage_close": func(ctx context.Context, cb *tgbotapi.CallbackQuery, _ string) {
			b.handleManageClose(ctx, cb)
		},
		"manage_kick": func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
			p1, p2, ok := splitTwo(rawID, cb.Data)
			if !ok {
				b.answerCallback(cb.ID, "")
				return
			}
			gid, err1 := strconv.ParseInt(p1, 10, 64)
			tid, err2 := strconv.ParseInt(p2, 10, 64)
			if err1 != nil || err2 != nil {
				slog.Debug("invalid IDs in manage_kick callback", "data", cb.Data)
				b.answerCallback(cb.ID, "")
				return
			}
			b.handleManageKickPlayer(ctx, cb, gid, tid)
		},
		"manage_kick_guest": func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
			p1, p2, ok := splitTwo(rawID, cb.Data)
			if !ok {
				b.answerCallback(cb.ID, "")
				return
			}
			gid, err1 := strconv.ParseInt(p1, 10, 64)
			tid, err2 := strconv.ParseInt(p2, 10, 64)
			if err1 != nil || err2 != nil {
				slog.Debug("invalid IDs in manage_kick_guest callback", "data", cb.Data)
				b.answerCallback(cb.ID, "")
				return
			}
			b.handleManageKickGuest(ctx, cb, gid, tid)
		},
		"manage_court_toggle":  b.handleManageCourtsToggle,
		"manage_court_confirm": int64H(b.handleManageCourtsConfirm),

		// ── New-game wizard ───────────────────────────────────────────────────────
		"select_group": func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
			// rawID: originChatID:originMessageID:groupID
			sub := strings.SplitN(rawID, ":", 3)
			if len(sub) != 3 {
				slog.Debug("invalid select_group callback format", "data", cb.Data)
				b.answerCallback(cb.ID, "")
				return
			}
			originChatID, err1 := strconv.ParseInt(sub[0], 10, 64)
			originMsgID, err2 := strconv.ParseInt(sub[1], 10, 64)
			groupID, err3 := strconv.ParseInt(sub[2], 10, 64)
			if err1 != nil || err2 != nil || err3 != nil {
				slog.Debug("invalid IDs in select_group callback", "data", cb.Data)
				b.answerCallback(cb.ID, "")
				return
			}
			b.handleGroupSelection(ctx, cb, pendingGameKey{chatID: originChatID, messageID: int(originMsgID)}, groupID)
		},
		"ng_date":          b.handleNewGameDate,
		"ng_group":         b.handleNewGameGroup,
		"ng_venue":         b.handleNewGameVenue,
		"ng_court_toggle":  b.handleNewGameCourtToggle,
		"ng_court_confirm": b.handleNewGameCourtConfirm,
		"ng_timeslot":      b.handleNewGameTimeSlot,
		"ng_time_custom": func(ctx context.Context, cb *tgbotapi.CallbackQuery, _ string) {
			b.handleNewGameTimeCustom(ctx, cb)
		},
		"ng_gvenue": b.handleNewGameGroupVenue,

		// ── Settings ──────────────────────────────────────────────────────────────
		"trigger":        b.handleTrigger,
		"set_lang_group": int64H(b.handleSetLangGroup),
		"set_lang": func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
			// rawID: lang:groupID
			p1, p2, ok := splitTwo(rawID, cb.Data)
			if !ok {
				b.answerCallback(cb.ID, "")
				return
			}
			groupID, err := strconv.ParseInt(p2, 10, 64)
			if err != nil {
				slog.Debug("invalid group_id in set_lang callback", "data", cb.Data)
				b.answerCallback(cb.ID, "")
				return
			}
			b.handleSetLang(ctx, cb, p1, groupID)
		},
		"set_tz_pick": int64H(b.handleSetTzPick),
		"set_tz": func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
			// rawID: groupID:tz  (tz may contain "/")
			colonIdx := strings.Index(rawID, ":")
			if colonIdx < 0 {
				slog.Debug("invalid set_tz callback format", "data", cb.Data)
				b.answerCallback(cb.ID, "")
				return
			}
			groupID, err := strconv.ParseInt(rawID[:colonIdx], 10, 64)
			if err != nil {
				slog.Debug("invalid group_id in set_tz callback", "data", cb.Data)
				b.answerCallback(cb.ID, "")
				return
			}
			b.handleSetTz(ctx, cb, rawID[colonIdx+1:], groupID)
		},

		// ── Venue management ──────────────────────────────────────────────────────
		"venue_list": int64H(b.handleVenueList),
		"venue_add":  int64H(b.handleVenueAdd),
		"venue_edit": int64H(b.handleVenueEditMenu),
		"venue_edit_name": func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
			venueID, groupID, ok := parseVenueGroup(rawID, cb.Data)
			if !ok {
				b.answerCallback(cb.ID, "")
				return
			}
			b.handleVenueStartEdit(ctx, cb, venueID, groupID, venueEditFieldName)
		},
		"venue_edit_courts": func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
			venueID, groupID, ok := parseVenueGroup(rawID, cb.Data)
			if !ok {
				b.answerCallback(cb.ID, "")
				return
			}
			b.handleVenueStartEdit(ctx, cb, venueID, groupID, venueEditFieldCourts)
		},
		"venue_edit_slots": func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
			venueID, groupID, ok := parseVenueGroup(rawID, cb.Data)
			if !ok {
				b.answerCallback(cb.ID, "")
				return
			}
			b.handleVenueStartEdit(ctx, cb, venueID, groupID, venueEditFieldTimeSlots)
		},
		"venue_edit_addr": func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
			venueID, groupID, ok := parseVenueGroup(rawID, cb.Data)
			if !ok {
				b.answerCallback(cb.ID, "")
				return
			}
			b.handleVenueStartEdit(ctx, cb, venueID, groupID, venueEditFieldAddress)
		},
		"venue_edit_gamedays": func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
			venueID, groupID, ok := parseVenueGroup(rawID, cb.Data)
			if !ok {
				b.answerCallback(cb.ID, "")
				return
			}
			b.handleVenueStartEdit(ctx, cb, venueID, groupID, venueEditFieldGameDays)
		},
		"venue_edit_graceperiod": func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
			venueID, groupID, ok := parseVenueGroup(rawID, cb.Data)
			if !ok {
				b.answerCallback(cb.ID, "")
				return
			}
			b.handleVenueStartEdit(ctx, cb, venueID, groupID, venueEditFieldGracePeriod)
		},
		"venue_edit_preferred_time": func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
			venueID, groupID, ok := parseVenueGroup(rawID, cb.Data)
			if !ok {
				b.answerCallback(cb.ID, "")
				return
			}
			b.handleVenueStartEdit(ctx, cb, venueID, groupID, venueEditFieldPreferredTime)
		},
		"venue_edit_auto_booking_courts": func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
			venueID, groupID, ok := parseVenueGroup(rawID, cb.Data)
			if !ok {
				b.answerCallback(cb.ID, "")
				return
			}
			b.handleVenueStartEdit(ctx, cb, venueID, groupID, venueEditFieldAutoBookingCourts)
		},
		"venue_edit_booking_opens_days": func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
			venueID, groupID, ok := parseVenueGroup(rawID, cb.Data)
			if !ok {
				b.answerCallback(cb.ID, "")
				return
			}
			b.handleVenueStartEdit(ctx, cb, venueID, groupID, venueEditFieldBookingOpensDays)
		},
		"venue_delete": func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
			venueID, groupID, ok := parseVenueGroup(rawID, cb.Data)
			if !ok {
				b.answerCallback(cb.ID, "")
				return
			}
			b.handleVenueDeleteConfirm(ctx, cb, venueID, groupID)
		},
		"venue_delete_ok": func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
			venueID, groupID, ok := parseVenueGroup(rawID, cb.Data)
			if !ok {
				b.answerCallback(cb.ID, "")
				return
			}
			b.handleVenueDelete(ctx, cb, venueID, groupID)
		},
		"venue_day_toggle": b.handleVenueDayToggle,
		"venue_day_confirm": func(ctx context.Context, cb *tgbotapi.CallbackQuery, _ string) {
			b.handleVenueDayConfirm(ctx, cb)
		},
		"venue_wiz_autobooking": b.handleVenueWizAutoBookingPick,
		"venue_toggle_autobooking": func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
			venueID, groupID, ok := parseVenueGroup(rawID, cb.Data)
			if !ok {
				b.answerCallback(cb.ID, "")
				return
			}
			b.handleVenueToggleAutoBooking(ctx, cb, venueID, groupID)
		},
		"venue_wiz_ptime": b.handleVenueWizPreferredTimePick,
		"venue_ptime_set": func(ctx context.Context, cb *tgbotapi.CallbackQuery, rawID string) {
			// rawID: venueID:slot  (slot may contain ":")
			colonIdx := strings.Index(rawID, ":")
			if colonIdx < 0 {
				slog.Debug("invalid venue_ptime_set format", "data", cb.Data)
				b.answerCallback(cb.ID, "")
				return
			}
			venueID, err := strconv.ParseInt(rawID[:colonIdx], 10, 64)
			if err != nil {
				slog.Debug("invalid venue_id in venue_ptime_set", "data", cb.Data)
				b.answerCallback(cb.ID, "")
				return
			}
			b.handleVenuePtimeSet(ctx, cb, venueID, rawID[colonIdx+1:])
		},
	}
}
