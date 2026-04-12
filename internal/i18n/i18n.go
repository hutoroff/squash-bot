// Package i18n provides localisation support for the squash bot.
// Three languages are supported: English (default/fallback), German, Russian.
package i18n

import (
	"fmt"
	"strings"
	"time"
)

// Lang is a supported language code.
type Lang string

const (
	En Lang = "en"
	De Lang = "de"
	Ru Lang = "ru"
)

// Normalize maps a Telegram LanguageCode (e.g. "de-DE", "ru") to a supported Lang.
// Falls back to English for unsupported or empty codes.
func Normalize(code string) Lang {
	base := Lang(strings.ToLower(strings.SplitN(code, "-", 2)[0]))
	switch base {
	case De, Ru:
		return base
	default:
		return En
	}
}

// Translation key constants.
const (
	// Game message (formatter)
	GameHeader      = "game.header"
	GameCourts      = "game.courts"
	GamePlayers     = "game.players"
	GameGuestLine   = "game.guest_line"
	GameLastUpdated = "game.last_updated"
	GameCompleted   = "game.completed"

	// Scheduler notifications
	SchedOverCapacity    = "sched.over_capacity"
	SchedUnderCapacity   = "sched.under_capacity"
	SchedWeeklyReminder  = "sched.weekly_reminder" // kept for backward compat; use SchedBookingReminder for new code
	SchedBookingReminder = "sched.booking_reminder"

	// Auto-booking notifications.
	// Group message when auto-booking succeeds.
	// Args: %d = courts booked, %s = venue name, %s = game date (YYYY-MM-DD), %s = preferred time (HH:MM)
	SchedAutoBookingSuccess = "sched.auto_booking_success"
	// Booking reminder group message when auto-booking was already done today.
	// Args: %s = venue name, %s = game date (YYYY-MM-DD), %s = preferred time (HH:MM)
	SchedBookingReminderAutoBooked = "sched.booking_reminder_auto_booked"
	// DM to admins when auto-booking partially failed or fully failed.
	// Args: %s = venue name, %s = game date (YYYY-MM-DD), %s = preferred time (HH:MM), %d = booked count, %d = target count
	SchedAutoBookingFailDM = "sched.auto_booking_fail_dm"

	// Cancellation reminder — outcome scenarios (always sent).
	// Scenario 1: no cancellation, player count is even and fits courts.
	// Args: %s = game date+time, %d = player count, %d = capacity, %d = courts count
	SchedReminderAllGood = "sched.reminder_all_good"
	// Scenario 2: courts were canceled and player count is now perfectly balanced.
	// Args: %s = canceled courts (comma-sep), %s = game date+time, %d = player count, %d = new capacity, %d = new courts count
	SchedReminderCanceled = "sched.reminder_canceled"
	// Scenario 3a: odd player count, no courts needed to be canceled.
	// Args: %s = game date+time, %d = player count, %d = capacity, %d = courts count
	SchedReminderOddNoCancel = "sched.reminder_odd_no_cancel"
	// Scenario 3b: odd player count, some courts were canceled.
	// Args: %s = canceled courts (comma-sep), %s = game date+time, %d = player count, %d = new capacity, %d = new courts count
	SchedReminderOddCanceled = "sched.reminder_odd_canceled"
	// Scenario 4: all courts were canceled — the game will not happen.
	// Args: %s = canceled courts (all, comma-sep), %s = game date+time
	SchedReminderAllCanceled = "sched.reminder_all_canceled"
	// Scenario 5: even player count but courts are under-utilised and no cancellation happened
	// (booking service not configured or no owned bookings found). Reminder to cancel manually.
	// Args: %s = game date+time, %d = player count, %d = capacity, %d = courts count
	SchedReminderEvenNoCancel = "sched.reminder_even_no_cancel"

	// Game keyboard buttons
	BtnImIn     = "btn.im_in"
	BtnIllSkip  = "btn.ill_skip"
	BtnPlusOne  = "btn.plus_one"
	BtnMinusOne = "btn.minus_one"

	// Management buttons
	BtnKickPlayer  = "btn.kick_player"
	BtnKickGuest   = "btn.kick_guest"
	BtnEditCourts  = "btn.edit_courts"
	BtnClose       = "btn.close"
	BtnBack        = "btn.back"
	BtnViewInGroup = "btn.view_in_group"

	// Trigger buttons
	BtnDayBefore            = "btn.day_before"      // kept for compat
	BtnDayAfter             = "btn.day_after"       // kept for compat
	BtnWeeklyReminder       = "btn.weekly_reminder" // kept for compat
	BtnCancellationReminder = "btn.cancellation_reminder"
	BtnDayAfterCleanup      = "btn.day_after_cleanup"
	BtnBookingReminder      = "btn.booking_reminder"
	BtnAutoBooking          = "btn.auto_booking"

	// Language selection buttons
	BtnLangEn = "btn.lang_en"
	BtnLangDe = "btn.lang_de"
	BtnLangRu = "btn.lang_ru"

	// Handler status / error messages
	MsgSomethingWentWrong        = "msg.something_went_wrong"
	MsgGameFullCapacity          = "msg.game_full_capacity"
	MsgNoGuestsToRemove          = "msg.no_guests_to_remove"
	MsgNoPlayersToKick           = "msg.no_players_to_kick"
	MsgSelectPlayerToKick        = "msg.select_player_to_kick"
	MsgPlayerKicked              = "msg.player_kicked"
	MsgNoGuestsToKick            = "msg.no_guests_to_kick"
	MsgSelectGuestToKick         = "msg.select_guest_to_kick"
	MsgGuestKicked               = "msg.guest_kicked"
	MsgNotAuthorized             = "msg.not_authorized"
	MsgUnknownEvent              = "msg.unknown_event"
	MsgFailedTrigger             = "msg.failed_trigger"
	MsgTriggered                 = "msg.triggered"
	MsgManageGameHeader          = "msg.manage_game_header"
	MsgSendGameDetails           = "msg.send_game_details"
	MsgManagementPrivateOnly     = "msg.management_private_only"
	MsgFailedVerifyPermissions   = "msg.failed_verify_permissions"
	MsgOnlyAdminCreate           = "msg.only_admin_create"
	MsgInvalidFormat             = "msg.invalid_format"
	MsgWhichGroup                = "msg.which_group"
	MsgSessionExpired            = "msg.session_expired"
	MsgNotAdminInGroup           = "msg.not_admin_in_group"
	MsgCreatingGame              = "msg.creating_game"
	MsgFailedCreateGame          = "msg.failed_create_game"
	MsgGameCreatedFailedAnnounce = "msg.game_created_failed_announce"
	MsgGameCreatedPinned         = "msg.game_created_pinned"
	MsgGameNotFound              = "msg.game_not_found"
	MsgNoUpcomingGames           = "msg.no_upcoming_games"
	MsgSendNewCourts             = "msg.send_new_courts"
	MsgLostAdminAccess           = "msg.lost_admin_access"
	MsgKickPlayerLabel           = "msg.kick_player_label"
	MsgKickGuestLabel            = "msg.kick_guest_label"
	MsgKickPlayerNotFound        = "msg.kick_player_not_found"
	MsgGuestNotFound             = "msg.guest_not_found"

	// Command handler messages
	MsgUnknownCommand                = "msg.unknown_command"
	MsgAvailableCommands             = "msg.available_commands"
	MsgCmdMyGame                     = "msg.cmd_my_game"
	MsgCmdHelp                       = "msg.cmd_help"
	MsgCmdLanguage                   = "msg.cmd_language"
	MsgAdminCommands                 = "msg.admin_commands"
	MsgCmdNewGame                    = "msg.cmd_new_game"
	MsgCmdGames                      = "msg.cmd_games"
	MsgCmdVenues                     = "msg.cmd_venues"
	MsgServiceAdminCommands          = "msg.service_admin_commands"
	MsgCmdTrigger                    = "msg.cmd_trigger"
	MsgFailedFetchGame               = "msg.failed_fetch_game"
	MsgNoUpcomingRegistered          = "msg.no_upcoming_registered"
	MsgFailedFetchDetails            = "msg.failed_fetch_details"
	MsgYourNextGame                  = "msg.your_next_game"
	MsgOnlyAdminCanUse               = "msg.only_admin_can_use"
	MsgFailedFetchGames              = "msg.failed_fetch_games"
	MsgFailedFetchGroupInfo          = "msg.failed_fetch_group_info"
	MsgSendGameDetailsCmd            = "msg.send_game_details_cmd"
	MsgInvalidFormatCmd              = "msg.invalid_format_cmd"
	MsgOnlyAdminCreateGames          = "msg.only_admin_create_games"
	MsgCourtsUpdated                 = "msg.courts_updated"
	MsgFailedUpdateCourts            = "msg.failed_update_courts"
	MsgCourtsUpdatedRefreshFailed    = "msg.courts_updated_refresh_failed"
	MsgInvalidCourtsFormat           = "msg.invalid_courts_format"
	MsgCourtsStringTooLong           = "msg.courts_string_too_long"
	MsgGameNotFoundPeriod            = "msg.game_not_found_period"
	MsgFailedVerifyPermissionsPeriod = "msg.failed_verify_permissions_period"
	MsgLostAdminAccessPeriod         = "msg.lost_admin_access_period"
	MsgNotAuthorizedCmd              = "msg.not_authorized_cmd"
	MsgSelectTriggerEvent            = "msg.select_trigger_event"
	MsgUpcomingGames                 = "msg.upcoming_games"
	MsgGameCourtsCapacity            = "msg.game_courts_capacity"
	MsgGroupLabel                    = "msg.group_label"
	MsgManageGameBtn                 = "msg.manage_game_btn"

	// Membership notifications
	MsgAddedNoAdmin = "msg.added_no_admin"
	MsgLostAdmin    = "msg.lost_admin"

	// Language command
	MsgSelectGroupForLanguage = "msg.select_group_for_language"
	MsgSelectGroupForVenues   = "msg.select_group_for_venues"
	MsgSelectLanguage         = "msg.select_language"
	MsgLanguageSet            = "msg.language_set"
	MsgOnlyAdminSetLanguage   = "msg.only_admin_set_language"
	MsgSelectGroupForTimezone = "msg.select_group_for_timezone"
	MsgSelectTimezone         = "msg.select_timezone"
	MsgTimezoneSet            = "msg.timezone_set"
	BtnSetTimezone            = "btn.set_timezone"

	// New game wizard
	MsgNewGameSelectDate         = "msg.new_game_select_date"
	MsgNewGameEnterTime          = "msg.new_game_enter_time"
	MsgNewGameInvalidTime        = "msg.new_game_invalid_time"
	MsgNewGameTimePast           = "msg.new_game_time_past"
	MsgNewGameEnterCourts        = "msg.new_game_enter_courts"
	MsgNewGameSelectVenue        = "msg.new_game_select_venue"
	MsgNewGameNoVenue            = "msg.new_game_no_venue"
	MsgNewGameSelectCourts       = "msg.new_game_select_courts"
	MsgNewGameConfirmCourts      = "msg.new_game_confirm_courts"
	MsgNewGameNoCourtsSelected   = "msg.new_game_no_courts_selected"
	MsgNewGameSelectTime         = "msg.new_game_select_time"
	MsgNewGameCustomTime         = "msg.new_game_custom_time"
	MsgNewGameNoVenuesConfigured = "msg.new_game_no_venues_configured"

	// Venue management
	MsgVenueList             = "msg.venue_list"
	MsgVenueNoVenues         = "msg.venue_no_venues"
	MsgVenueCreated          = "msg.venue_created"
	MsgVenueUpdated          = "msg.venue_updated"
	MsgVenueDeleted          = "msg.venue_deleted"
	MsgVenueNotFound         = "msg.venue_not_found"
	MsgVenueAskName          = "msg.venue_ask_name"
	MsgVenueAskCourts        = "msg.venue_ask_courts"
	MsgVenueAskTimeSlots     = "msg.venue_ask_time_slots"
	MsgVenueAskAddress       = "msg.venue_ask_address"
	MsgVenueSkipAddress      = "msg.venue_skip_address"
	MsgVenueEditMenu         = "msg.venue_edit_menu"
	MsgVenueConfirmDelete    = "msg.venue_confirm_delete"
	MsgVenueInvalidTimeSlots = "msg.venue_invalid_time_slots"
	BtnVenueEditName         = "btn.venue_edit_name"
	BtnVenueEditCourts       = "btn.venue_edit_courts"
	BtnVenueEditTimeSlots    = "btn.venue_edit_time_slots"
	BtnVenueEditAddress      = "btn.venue_edit_address"
	BtnVenueDelete           = "btn.venue_delete"
	BtnVenueAdd              = "btn.venue_add"
	BtnVenueConfirmDelete    = "btn.venue_confirm_delete"
	BtnVenueEditGameDays     = "btn.venue_edit_game_days"
	BtnVenueEditGracePeriod  = "btn.venue_edit_grace_period"

	MsgVenueAskGameDays        = "msg.venue_ask_game_days"
	MsgVenueAskGracePeriod     = "msg.venue_ask_grace_period"
	MsgVenueConfirmDays        = "msg.venue_confirm_days"
	MsgVenueAskPreferredTime   = "msg.venue_ask_preferred_time"
	MsgVenueNoPreferredTime    = "msg.venue_no_preferred_time"
	MsgVenuePreferredTimeLine  = "msg.venue_preferred_time_line"
	BtnVenueEditPreferredTime  = "btn.venue_edit_preferred_time"
	BtnVenueClearPreferredTime = "btn.venue_clear_preferred_time"

	// Auto-booking courts priority
	// Args for MsgVenueAskAutoBookingCourts: %s = courts list (from venue.Courts)
	MsgVenueAskAutoBookingCourts     = "msg.venue_ask_auto_booking_courts"
	MsgVenueAutoBookingCourtsLine    = "msg.venue_auto_booking_courts_line"
	MsgVenueInvalidAutoBookingCourts = "msg.venue_invalid_auto_booking_courts"
	BtnVenueEditAutoBookingCourts    = "btn.venue_edit_auto_booking_courts"

	// Booking opens days
	// Args for MsgVenueBookingOpensDaysLine: %d = days
	MsgVenueAskBookingOpensDays  = "msg.venue_ask_booking_opens_days"
	MsgVenueBookingOpensDaysLine = "msg.venue_booking_opens_days_line"
	BtnVenueEditBookingOpensDays = "btn.venue_edit_booking_opens_days"

	// Game message — venue line
	GameVenueLine = "game.venue_line"

	// Calendar — weekday names
	WeekdaySunday    = "weekday.sunday"
	WeekdayMonday    = "weekday.monday"
	WeekdayTuesday   = "weekday.tuesday"
	WeekdayWednesday = "weekday.wednesday"
	WeekdayThursday  = "weekday.thursday"
	WeekdayFriday    = "weekday.friday"
	WeekdaySaturday  = "weekday.saturday"

	// Calendar — full month names (used in game date header)
	MonthJanuary   = "month.january"
	MonthFebruary  = "month.february"
	MonthMarch     = "month.march"
	MonthApril     = "month.april"
	MonthMay       = "month.may"
	MonthJune      = "month.june"
	MonthJuly      = "month.july"
	MonthAugust    = "month.august"
	MonthSeptember = "month.september"
	MonthOctober   = "month.october"
	MonthNovember  = "month.november"
	MonthDecember  = "month.december"

	// Calendar — abbreviated month names (used in "Last updated" footer and button labels)
	MonthShortJanuary   = "month_short.january"
	MonthShortFebruary  = "month_short.february"
	MonthShortMarch     = "month_short.march"
	MonthShortApril     = "month_short.april"
	MonthShortMay       = "month_short.may"
	MonthShortJune      = "month_short.june"
	MonthShortJuly      = "month_short.july"
	MonthShortAugust    = "month_short.august"
	MonthShortSeptember = "month_short.september"
	MonthShortOctober   = "month_short.october"
	MonthShortNovember  = "month_short.november"
	MonthShortDecember  = "month_short.december"
)

// translations holds all strings keyed by Lang then message key.
var translations = map[Lang]map[string]string{
	En: {
		// Game message
		GameHeader:      "🏸 Squash Game",
		GameCourts:      "🎾 Courts: %s (capacity: %d players)",
		GamePlayers:     "Players (%d/%d):",
		GameGuestLine:   "+1 (by %s)",
		GameLastUpdated: "Last updated: %s",
		GameCompleted:   "Game completed ✓",

		// Scheduler
		SchedOverCapacity:    "⚠️ Too many players! %d registered but only %d spots (%d courts). Consider booking an extra court.",
		SchedUnderCapacity:   "📢 Free spots available! %d/%d players registered (%d courts). Invite more friends!",
		SchedWeeklyReminder:  "👋 Reminder: no squash game has been scheduled for this week yet. Don't forget to create one!",
		SchedBookingReminder: "📅 Booking reminder for *%s*: game in %d days — courts booking is open now! Don't forget to reserve your courts.",

		SchedReminderAllGood:      "✅ Upcoming game on %s — all good! %d/%d players, %d courts confirmed.",
		SchedReminderCanceled:     "✅ Courts %s canceled. Game on %s is all set! %d/%d players, %d courts.",
		SchedReminderOddNoCancel:  "⚠️ 1 free spot for the game on %s. %d/%d players, %d courts.",
		SchedReminderOddCanceled:  "⚠️ Courts %s canceled. 1 free spot for the game on %s. %d/%d players, %d courts.",
		SchedReminderAllCanceled:  "❌ All courts (%s) canceled for the game on %s. The game will not happen.",
		SchedReminderEvenNoCancel: "⚠️ Upcoming game on %s — %d/%d players, %d courts. Please cancel unused courts.",

		// Keyboard buttons
		BtnImIn:     "I'm in",
		BtnIllSkip:  "I'll skip",
		BtnPlusOne:  "+1",
		BtnMinusOne: "-1",

		BtnKickPlayer:  "Kick Player",
		BtnKickGuest:   "Kick Guest",
		BtnEditCourts:  "Edit Courts",
		BtnClose:       "✕ Close",
		BtnBack:        "← Back",
		BtnViewInGroup: "View in group →",

		BtnDayBefore:            "Day Before Check",
		BtnDayAfter:             "Day After Cleanup",
		BtnWeeklyReminder:       "Weekly Reminder",
		BtnCancellationReminder: "Cancellation Reminder",
		BtnDayAfterCleanup:      "Day After Cleanup",
		BtnBookingReminder:      "Booking Reminder",
		BtnAutoBooking:          "Auto Booking",

		BtnLangEn: "🇬🇧 English",
		BtnLangDe: "🇩🇪 Deutsch",
		BtnLangRu: "🇷🇺 Русский",

		// Handler messages
		MsgSomethingWentWrong:        "Something went wrong, please try again",
		MsgGameFullCapacity:          "Game is already at full capacity",
		MsgNoGuestsToRemove:          "You haven't invited any guests",
		MsgNoPlayersToKick:           "No registered players to kick",
		MsgSelectPlayerToKick:        "Select a player to kick:",
		MsgPlayerKicked:              "Player kicked ✓",
		MsgNoGuestsToKick:            "No guests to kick",
		MsgSelectGuestToKick:         "Select a guest to kick:",
		MsgGuestKicked:               "Guest kicked ✓",
		MsgNotAuthorized:             "Not authorized",
		MsgUnknownEvent:              "Unknown event",
		MsgFailedTrigger:             "Failed to trigger — check service health",
		MsgTriggered:                 "Triggered ✓",
		MsgManageGameHeader:          "*Manage game:*\n📅 %s · %s\n🎾 Courts: %s\nPlayers: %d/%d, Guests: %d",
		MsgSendGameDetails:           "Send game details after the command:\n/new\\_game\nYYYY-MM-DD HH:MM\ncourts: 2,3,4",
		MsgManagementPrivateOnly:     "Management commands work in private messages. Start a chat with me and use /help.",
		MsgFailedVerifyPermissions:   "Failed to verify permissions",
		MsgOnlyAdminCreate:           "Only group administrators can create games",
		MsgInvalidFormat:             "Invalid format. Use:\nYYYY-MM-DD HH:MM\ncourts: 2,3,4",
		MsgWhichGroup:                "Which group should I post the game announcement in?",
		MsgSessionExpired:            "Session expired, please send the game details again",
		MsgNotAdminInGroup:           "You are not an admin in that group",
		MsgCreatingGame:              "Creating game...",
		MsgFailedCreateGame:          "Failed to create game",
		MsgGameCreatedFailedAnnounce: "Game created but failed to send announcement",
		MsgGameCreatedPinned:         "Game created and pinned ✓",
		MsgGameNotFound:              "Game not found",
		MsgNoUpcomingGames:           "No upcoming games in your groups.",
		MsgSendNewCourts:             "Send the new courts (e.g.: 2,3,4):",
		MsgLostAdminAccess:           "You no longer have admin access to this group",
		MsgKickPlayerLabel:           "Kick %s",
		MsgKickGuestLabel:            "Kick +1 (by %s)",
		MsgKickPlayerNotFound:        "Player not found in this game",
		MsgGuestNotFound:             "Guest not found",

		// Commands
		MsgUnknownCommand:                "Unknown command. Send /help to see available commands.",
		MsgAvailableCommands:             "Available commands:\n",
		MsgCmdMyGame:                     "/mygame — Show your next upcoming game\n",
		MsgCmdHelp:                       "/help — Show this help message\n",
		MsgCmdLanguage:                   "/language — Set the bot language for your group\n",
		MsgAdminCommands:                 "\nAdmin commands:\n",
		MsgCmdNewGame:                    "/newgame — Create a new game\n",
		MsgCmdGames:                      "/games — Show and manage upcoming games\n",
		MsgCmdVenues:                     "/venues — Manage venues for your group\n",
		MsgServiceAdminCommands:          "\nService admin commands:\n",
		MsgCmdTrigger:                    "/trigger — Manually trigger a scheduled event\n",
		MsgFailedFetchGame:               "Failed to fetch your next game. Please try again.",
		MsgNoUpcomingRegistered:          "You have no upcoming registered games.",
		MsgFailedFetchDetails:            "Failed to fetch game details. Please try again.",
		MsgYourNextGame:                  "Your next game:\n\n",
		MsgOnlyAdminCanUse:               "Only group administrators can use this command.",
		MsgFailedFetchGames:              "Failed to fetch games. Please try again.",
		MsgFailedFetchGroupInfo:          "Failed to fetch group info. Please try again.",
		MsgSendGameDetailsCmd:            "Send the game details after the command:\n\n/new\\_game\nYYYY-MM-DD HH:MM\ncourts: 2,3,4",
		MsgInvalidFormatCmd:              "Invalid format. Use:\n\n/new\\_game\nYYYY-MM-DD HH:MM\ncourts: 2,3,4",
		MsgOnlyAdminCreateGames:          "Only group administrators can create games.",
		MsgCourtsUpdated:                 "Courts updated to: %s ✓",
		MsgFailedUpdateCourts:            "Failed to update courts. Please try again.",
		MsgCourtsUpdatedRefreshFailed:    "Courts updated, but failed to refresh the group message.",
		MsgInvalidCourtsFormat:           "Invalid format. Expected courts like: 2,3,4",
		MsgCourtsStringTooLong:           "Courts string too long (max %d chars)",
		MsgGameNotFoundPeriod:            "Game not found.",
		MsgFailedVerifyPermissionsPeriod: "Failed to verify permissions.",
		MsgLostAdminAccessPeriod:         "You no longer have admin access to this group.",
		MsgNotAuthorizedCmd:              "You are not authorized to use this command.",
		MsgSelectTriggerEvent:            "Select a scheduled event to trigger manually:",
		MsgUpcomingGames:                 "*Upcoming games:*\n\n",
		MsgGameCourtsCapacity:            "🎾 Courts: %s — capacity %d\n",
		MsgGroupLabel:                    "Group: %s\n\n",
		MsgManageGameBtn:                 "Manage: %s %s",

		// Membership
		MsgAddedNoAdmin: "I've been added to \"%s\" but I don't have administrator permissions.\n\nTo pin game announcements, please grant me admin rights in that group.",
		MsgLostAdmin:    "I've lost administrator permissions in \"%s\".\n\nWithout admin rights I can no longer pin game announcements.",

		// Language command
		MsgSelectGroupForLanguage: "Which group's language do you want to change?",
		MsgSelectGroupForVenues:   "Which group's venues do you want to manage?",
		MsgSelectLanguage:         "Select a language for your group:",
		MsgLanguageSet:            "Language updated ✓",
		MsgOnlyAdminSetLanguage:   "Only group administrators can change the language.",
		MsgSelectGroupForTimezone: "Which group's timezone do you want to change?",
		MsgSelectTimezone:         "Select a timezone for your group:",
		MsgTimezoneSet:            "Timezone updated ✓",
		BtnSetTimezone:            "🕐 Set Timezone",

		// New game wizard
		MsgNewGameSelectDate:         "Select a date for the new game:",
		MsgNewGameEnterTime:          "Game on %s.\n\nEnter the time (HH:MM, e.g. 19:30):",
		MsgNewGameInvalidTime:        "Invalid time. Please enter time as HH:MM (e.g. 19:30):",
		MsgNewGameTimePast:           "That time is already in the past. Please enter a future time (e.g. 19:30):",
		MsgNewGameEnterCourts:        "Game on %s at %s.\n\nEnter the courts (e.g. 2,3 or 2 3):",
		MsgNewGameSelectVenue:        "Game on %s.\n\nSelect a venue:",
		MsgNewGameNoVenue:            "No venue / manual",
		MsgNewGameSelectCourts:       "Select courts for the game (tap to toggle):",
		MsgNewGameConfirmCourts:      "✓ Confirm (%s)",
		MsgNewGameNoCourtsSelected:   "Please select at least one court.",
		MsgNewGameSelectTime:         "Game on %s at %s.\n\nSelect a time slot:",
		MsgNewGameCustomTime:         "✎ Custom time",
		MsgNewGameNoVenuesConfigured: "No venues are configured for your group. Please add at least one venue with /venues before creating a game.",

		// Venue management
		MsgVenueList:                     "*Venues for %s:*\n\n",
		MsgVenueNoVenues:                 "No venues configured yet.",
		MsgVenueCreated:                  "Venue created ✓",
		MsgVenueUpdated:                  "Venue updated ✓",
		MsgVenueDeleted:                  "Venue deleted ✓",
		MsgVenueNotFound:                 "Venue not found.",
		MsgVenueAskName:                  "Enter the venue name (e.g. City Sports Center):",
		MsgVenueAskCourts:                "Enter the available courts (e.g. 1,2,3,4,5,6):",
		MsgVenueAskTimeSlots:             "Enter preset time slots (e.g. 18:00,19:00,20:00), or send - to skip:",
		MsgVenueAskAddress:               "Enter the venue address or link (optional), or send - to skip:",
		MsgVenueSkipAddress:              "-",
		MsgVenueEditMenu:                 "*%s*\nCourts: %s\nTime slots: %s\nAddress: %s",
		MsgVenueConfirmDelete:            "Delete venue *%s*? This cannot be undone.",
		MsgVenueInvalidTimeSlots:         "Invalid time slots. Each slot must be in HH:MM format (e.g. 18:00,19:00,20:00):",
		MsgVenueAskGameDays:              "Which day(s) do games happen at this venue? Tap to toggle, then press Confirm. Send - to skip.",
		MsgVenueAskGracePeriod:           "Grace period for cancellation reminders in hours (default: 24). Send - to use default:",
		MsgVenueConfirmDays:              "✓ Confirm",
		MsgVenueAskPreferredTime:         "Select the preferred game time for this venue (it will be highlighted when creating a new game):",
		MsgVenueNoPreferredTime:          "No preferred time set.",
		MsgVenuePreferredTimeLine:        "Preferred time: %s",
		BtnVenueEditName:                 "✏️ Name",
		BtnVenueEditCourts:               "🎾 Courts",
		BtnVenueEditTimeSlots:            "🕐 Time Slots",
		BtnVenueEditAddress:              "📍 Address",
		BtnVenueDelete:                   "🗑 Delete",
		BtnVenueAdd:                      "+ Add Venue",
		BtnVenueConfirmDelete:            "Yes, delete",
		BtnVenueEditGameDays:             "📅 Game Days",
		BtnVenueEditGracePeriod:          "⏱ Grace Period",
		BtnVenueEditPreferredTime:        "⭐ Preferred Time",
		BtnVenueClearPreferredTime:       "✕ No preference",
		MsgVenueAskAutoBookingCourts:     "Enter court IDs for auto-booking in priority order (comma-separated subset of %s). Send - to book any available court:",
		MsgVenueAutoBookingCourtsLine:    "Auto-booking courts: %s",
		MsgVenueInvalidAutoBookingCourts: "Invalid courts — each ID must be in the venue's court list. Please try again:",
		BtnVenueEditAutoBookingCourts:    "🤖 Auto-booking Courts",

		MsgVenueAskBookingOpensDays:  "How many days in advance does booking open? (default: 14). Send - to use default:",
		MsgVenueBookingOpensDaysLine: "Booking opens: %d days before",
		BtnVenueEditBookingOpensDays: "📆 Booking Opens",

		// Game message — venue
		GameVenueLine: "📍 %s",

		// Weekdays
		WeekdaySunday:    "Sunday",
		WeekdayMonday:    "Monday",
		WeekdayTuesday:   "Tuesday",
		WeekdayWednesday: "Wednesday",
		WeekdayThursday:  "Thursday",
		WeekdayFriday:    "Friday",
		WeekdaySaturday:  "Saturday",

		// Months (full — English nominative; used as "Month Day" in game date)
		MonthJanuary:   "January",
		MonthFebruary:  "February",
		MonthMarch:     "March",
		MonthApril:     "April",
		MonthMay:       "May",
		MonthJune:      "June",
		MonthJuly:      "July",
		MonthAugust:    "August",
		MonthSeptember: "September",
		MonthOctober:   "October",
		MonthNovember:  "November",
		MonthDecember:  "December",

		// Months (short — for "Last updated" footer and button labels)
		MonthShortJanuary:   "Jan",
		MonthShortFebruary:  "Feb",
		MonthShortMarch:     "Mar",
		MonthShortApril:     "Apr",
		MonthShortMay:       "May",
		MonthShortJune:      "Jun",
		MonthShortJuly:      "Jul",
		MonthShortAugust:    "Aug",
		MonthShortSeptember: "Sep",
		MonthShortOctober:   "Oct",
		MonthShortNovember:  "Nov",
		MonthShortDecember:  "Dec",

		// Auto-booking
		SchedAutoBookingSuccess:        "🤖 Auto-booked %d courts for %s on %s at %s.",
		SchedBookingReminderAutoBooked: "🤖 Courts were automatically booked for %s on %s at %s. All set!",
		SchedAutoBookingFailDM:         "⚠️ Auto-booking for %s on %s at %s: booked %d of %d courts. Please book the remaining courts manually.",
	},

	De: {
		// Game message
		GameHeader:      "🏸 Squash-Spiel",
		GameCourts:      "🎾 Plätze: %s (Kapazität: %d Spieler)",
		GamePlayers:     "Spieler (%d/%d):",
		GameGuestLine:   "+1 (von %s)",
		GameLastUpdated: "Zuletzt aktualisiert: %s",
		GameCompleted:   "Spiel beendet ✓",

		// Scheduler
		SchedOverCapacity:    "⚠️ Zu viele Spieler! %d angemeldet, aber nur %d Plätze (%d Plätze). Erwäge, einen weiteren Platz zu buchen.",
		SchedUnderCapacity:   "📢 Freie Plätze verfügbar! %d/%d Spieler angemeldet (%d Plätze). Lade mehr Freunde ein!",
		SchedWeeklyReminder:  "👋 Erinnerung: Für diese Woche ist noch kein Squash-Spiel geplant. Vergiss nicht, eines zu erstellen!",
		SchedBookingReminder: "📅 Buchungserinnerung für *%s*: Spiel in %d Tagen — Buchung ist jetzt offen! Vergiss nicht, deine Plätze zu reservieren.",

		SchedReminderAllGood:      "✅ Spiel am %s — alles gut! %d/%d Spieler, %d Plätze bestätigt.",
		SchedReminderCanceled:     "✅ Plätze %s storniert. Spiel am %s ist bereit! %d/%d Spieler, %d Plätze.",
		SchedReminderOddNoCancel:  "⚠️ 1 freier Platz für das Spiel am %s. %d/%d Spieler, %d Plätze.",
		SchedReminderOddCanceled:  "⚠️ Plätze %s storniert. 1 freier Platz für das Spiel am %s. %d/%d Spieler, %d Plätze.",
		SchedReminderAllCanceled:  "❌ Alle Plätze (%s) für das Spiel am %s storniert. Das Spiel findet nicht statt.",
		SchedReminderEvenNoCancel: "⚠️ Spiel am %s — %d/%d Spieler, %d Plätze. Bitte ungenutzte Plätze stornieren.",

		// Keyboard buttons
		BtnImIn:     "Ich bin dabei",
		BtnIllSkip:  "Ich passe",
		BtnPlusOne:  "+1",
		BtnMinusOne: "-1",

		BtnKickPlayer:  "Spieler entfernen",
		BtnKickGuest:   "Gast entfernen",
		BtnEditCourts:  "Plätze bearbeiten",
		BtnClose:       "✕ Schließen",
		BtnBack:        "← Zurück",
		BtnViewInGroup: "In Gruppe ansehen →",

		BtnDayBefore:            "Tag-Vorher-Prüfung",
		BtnDayAfter:             "Tag-Danach-Bereinigung",
		BtnWeeklyReminder:       "Wöchentliche Erinnerung",
		BtnCancellationReminder: "Absage-Erinnerung",
		BtnDayAfterCleanup:      "Tag-Danach-Bereinigung",
		BtnBookingReminder:      "Buchungserinnerung",
		BtnAutoBooking:          "Automatische Buchung",

		BtnLangEn: "🇬🇧 English",
		BtnLangDe: "🇩🇪 Deutsch",
		BtnLangRu: "🇷🇺 Русский",

		// Handler messages
		MsgSomethingWentWrong:        "Etwas ist schiefgelaufen, bitte versuche es erneut",
		MsgGameFullCapacity:          "Das Spiel ist bereits voll besetzt",
		MsgNoGuestsToRemove:          "Du hast keine Gäste eingeladen",
		MsgNoPlayersToKick:           "Keine angemeldeten Spieler zum Entfernen",
		MsgSelectPlayerToKick:        "Spieler zum Entfernen auswählen:",
		MsgPlayerKicked:              "Spieler entfernt ✓",
		MsgNoGuestsToKick:            "Keine Gäste zum Entfernen",
		MsgSelectGuestToKick:         "Gast zum Entfernen auswählen:",
		MsgGuestKicked:               "Gast entfernt ✓",
		MsgNotAuthorized:             "Nicht autorisiert",
		MsgUnknownEvent:              "Unbekanntes Ereignis",
		MsgFailedTrigger:             "Auslösung fehlgeschlagen — Servicestatus prüfen",
		MsgTriggered:                 "Ausgelöst ✓",
		MsgManageGameHeader:          "*Spiel verwalten:*\n📅 %s · %s\n🎾 Plätze: %s\nSpieler: %d/%d, Gäste: %d",
		MsgSendGameDetails:           "Spieldetails nach dem Befehl senden:\n/new\\_game\nJJJJ-MM-TT HH:MM\ncourts: 2,3,4",
		MsgManagementPrivateOnly:     "Verwaltungsbefehle funktionieren in privaten Nachrichten. Starte einen Chat mit mir und verwende /help.",
		MsgFailedVerifyPermissions:   "Berechtigungen konnten nicht überprüft werden",
		MsgOnlyAdminCreate:           "Nur Gruppenadministratoren können Spiele erstellen",
		MsgInvalidFormat:             "Ungültiges Format. Verwende:\nJJJJ-MM-TT HH:MM\ncourts: 2,3,4",
		MsgWhichGroup:                "In welcher Gruppe soll die Spielankündigung gepostet werden?",
		MsgSessionExpired:            "Sitzung abgelaufen, bitte Spieldetails erneut senden",
		MsgNotAdminInGroup:           "Du bist kein Administrator in dieser Gruppe",
		MsgCreatingGame:              "Spiel wird erstellt...",
		MsgFailedCreateGame:          "Spiel konnte nicht erstellt werden",
		MsgGameCreatedFailedAnnounce: "Spiel erstellt, aber Ankündigung konnte nicht gesendet werden",
		MsgGameCreatedPinned:         "Spiel erstellt und angeheftet ✓",
		MsgGameNotFound:              "Spiel nicht gefunden",
		MsgNoUpcomingGames:           "Keine bevorstehenden Spiele in deinen Gruppen.",
		MsgSendNewCourts:             "Neue Plätze senden (z.B.: 2,3,4):",
		MsgLostAdminAccess:           "Du hast keinen Admin-Zugriff mehr auf diese Gruppe",
		MsgKickPlayerLabel:           "%s entfernen",
		MsgKickGuestLabel:            "+1 entfernen (von %s)",
		MsgKickPlayerNotFound:        "Spieler nicht in diesem Spiel gefunden",
		MsgGuestNotFound:             "Gast nicht gefunden",

		// Commands
		MsgUnknownCommand:                "Unbekannter Befehl. Sende /help, um verfügbare Befehle zu sehen.",
		MsgAvailableCommands:             "Verfügbare Befehle:\n",
		MsgCmdMyGame:                     "/mygame — Dein nächstes Spiel anzeigen\n",
		MsgCmdHelp:                       "/help — Diese Hilfe anzeigen\n",
		MsgCmdLanguage:                   "/language — Botsprache für deine Gruppe festlegen\n",
		MsgAdminCommands:                 "\nAdmin-Befehle:\n",
		MsgCmdNewGame:                    "/newgame — Ein neues Spiel erstellen\n",
		MsgCmdGames:                      "/games — Bevorstehende Spiele anzeigen und verwalten\n",
		MsgCmdVenues:                     "/venues — Orte für deine Gruppe verwalten\n",
		MsgServiceAdminCommands:          "\nService-Admin-Befehle:\n",
		MsgCmdTrigger:                    "/trigger — Geplantes Ereignis manuell auslösen\n",
		MsgFailedFetchGame:               "Dein nächstes Spiel konnte nicht abgerufen werden. Bitte versuche es erneut.",
		MsgNoUpcomingRegistered:          "Du hast keine bevorstehenden angemeldeten Spiele.",
		MsgFailedFetchDetails:            "Spieldetails konnten nicht abgerufen werden. Bitte versuche es erneut.",
		MsgYourNextGame:                  "Dein nächstes Spiel:\n\n",
		MsgOnlyAdminCanUse:               "Nur Gruppenadministratoren können diesen Befehl verwenden.",
		MsgFailedFetchGames:              "Spiele konnten nicht abgerufen werden. Bitte versuche es erneut.",
		MsgFailedFetchGroupInfo:          "Gruppeninfo konnte nicht abgerufen werden. Bitte versuche es erneut.",
		MsgSendGameDetailsCmd:            "Spieldetails nach dem Befehl senden:\n\n/new\\_game\nJJJJ-MM-TT HH:MM\ncourts: 2,3,4",
		MsgInvalidFormatCmd:              "Ungültiges Format. Verwende:\n\n/new\\_game\nJJJJ-MM-TT HH:MM\ncourts: 2,3,4",
		MsgOnlyAdminCreateGames:          "Nur Gruppenadministratoren können Spiele erstellen.",
		MsgCourtsUpdated:                 "Plätze aktualisiert auf: %s ✓",
		MsgFailedUpdateCourts:            "Plätze konnten nicht aktualisiert werden. Bitte versuche es erneut.",
		MsgCourtsUpdatedRefreshFailed:    "Plätze aktualisiert, aber Gruppenansicht konnte nicht aktualisiert werden.",
		MsgInvalidCourtsFormat:           "Ungültiges Format. Erwartet z.B.: 2,3,4",
		MsgCourtsStringTooLong:           "Platznummern zu lang (max %d Zeichen)",
		MsgGameNotFoundPeriod:            "Spiel nicht gefunden.",
		MsgFailedVerifyPermissionsPeriod: "Berechtigungen konnten nicht überprüft werden.",
		MsgLostAdminAccessPeriod:         "Du hast keinen Admin-Zugriff mehr auf diese Gruppe.",
		MsgNotAuthorizedCmd:              "Du bist nicht berechtigt, diesen Befehl zu verwenden.",
		MsgSelectTriggerEvent:            "Geplantes Ereignis zum manuellen Auslösen auswählen:",
		MsgUpcomingGames:                 "*Bevorstehende Spiele:*\n\n",
		MsgGameCourtsCapacity:            "🎾 Plätze: %s — Kapazität %d\n",
		MsgGroupLabel:                    "Gruppe: %s\n\n",
		MsgManageGameBtn:                 "Verwalten: %s %s",

		// Membership
		MsgAddedNoAdmin: "Ich wurde zu \"%s\" hinzugefügt, habe aber keine Administrator-Rechte.\n\nUm Spielankündigungen anzuheften, gewähre mir bitte Admin-Rechte in dieser Gruppe.",
		MsgLostAdmin:    "Ich habe die Administrator-Rechte in \"%s\" verloren.\n\nOhne Admin-Rechte kann ich keine Spielankündigungen mehr anheften.",

		// Language command
		MsgSelectGroupForLanguage: "Für welche Gruppe möchtest du die Sprache ändern?",
		MsgSelectGroupForVenues:   "Für welche Gruppe möchtest du die Veranstaltungsorte verwalten?",
		MsgSelectLanguage:         "Sprache für deine Gruppe auswählen:",
		MsgLanguageSet:            "Sprache aktualisiert ✓",
		MsgOnlyAdminSetLanguage:   "Nur Gruppenadministratoren können die Sprache ändern.",
		MsgSelectGroupForTimezone: "Für welche Gruppe möchtest du die Zeitzone ändern?",
		MsgSelectTimezone:         "Zeitzone für deine Gruppe auswählen:",
		MsgTimezoneSet:            "Zeitzone aktualisiert ✓",
		BtnSetTimezone:            "🕐 Zeitzone festlegen",

		// New game wizard
		MsgNewGameSelectDate:         "Datum für das neue Spiel auswählen:",
		MsgNewGameEnterTime:          "Spiel am %s.\n\nGib die Uhrzeit ein (HH:MM, z.B. 19:30):",
		MsgNewGameInvalidTime:        "Ungültige Uhrzeit. Bitte gib die Uhrzeit als HH:MM ein (z.B. 19:30):",
		MsgNewGameTimePast:           "Diese Uhrzeit liegt bereits in der Vergangenheit. Bitte gib eine zukünftige Uhrzeit ein (z.B. 19:30):",
		MsgNewGameEnterCourts:        "Spiel am %s um %s.\n\nGib die Plätze ein (z.B. 2,3 oder 2 3):",
		MsgNewGameSelectVenue:        "Spiel am %s.\n\nWähle einen Veranstaltungsort:",
		MsgNewGameNoVenue:            "Kein Ort / manuell",
		MsgNewGameSelectCourts:       "Plätze für das Spiel auswählen (tippen zum Umschalten):",
		MsgNewGameConfirmCourts:      "✓ Bestätigen (%s)",
		MsgNewGameNoCourtsSelected:   "Bitte mindestens einen Platz auswählen.",
		MsgNewGameSelectTime:         "Spiel am %s um %s.\n\nZeitfenster auswählen:",
		MsgNewGameCustomTime:         "✎ Eigene Uhrzeit",
		MsgNewGameNoVenuesConfigured: "Für deine Gruppe sind keine Orte konfiguriert. Bitte füge mindestens einen Ort mit /venues hinzu, bevor du ein Spiel erstellst.",

		// Venue management
		MsgVenueList:                     "*Orte für %s:*\n\n",
		MsgVenueNoVenues:                 "Noch keine Orte konfiguriert.",
		MsgVenueCreated:                  "Ort erstellt ✓",
		MsgVenueUpdated:                  "Ort aktualisiert ✓",
		MsgVenueDeleted:                  "Ort gelöscht ✓",
		MsgVenueNotFound:                 "Ort nicht gefunden.",
		MsgVenueAskName:                  "Gib den Namen des Ortes ein (z.B. Stadtsportszentrum):",
		MsgVenueAskCourts:                "Gib die verfügbaren Plätze ein (z.B. 1,2,3,4,5,6):",
		MsgVenueAskTimeSlots:             "Gib voreingestellte Zeitfenster ein (z.B. 18:00,19:00,20:00) oder sende - zum Überspringen:",
		MsgVenueAskAddress:               "Gib die Adresse oder einen Link zum Ort ein (optional) oder sende - zum Überspringen:",
		MsgVenueSkipAddress:              "-",
		MsgVenueEditMenu:                 "*%s*\nPlätze: %s\nZeitfenster: %s\nAdresse: %s",
		MsgVenueConfirmDelete:            "Ort *%s* löschen? Dies kann nicht rückgängig gemacht werden.",
		MsgVenueInvalidTimeSlots:         "Ungültige Zeitfenster. Jedes Zeitfenster muss im Format HH:MM sein (z.B. 18:00,19:00,20:00):",
		MsgVenueAskGameDays:              "An welchen Tag(en) finden Spiele in diesem Ort statt? Tippe zum Umschalten, dann Bestätigen drücken. Sende - zum Überspringen.",
		MsgVenueAskGracePeriod:           "Kulanzzeit für Stornierungserinnerungen in Stunden (Standard: 24). Sende - für Standard:",
		MsgVenueConfirmDays:              "✓ Bestätigen",
		MsgVenueAskPreferredTime:         "Bevorzugte Spielzeit für diesen Ort auswählen (wird bei neuen Spielen hervorgehoben):",
		MsgVenueNoPreferredTime:          "Keine bevorzugte Zeit festgelegt.",
		MsgVenuePreferredTimeLine:        "Bevorzugte Zeit: %s",
		BtnVenueEditName:                 "✏️ Name",
		BtnVenueEditCourts:               "🎾 Plätze",
		BtnVenueEditTimeSlots:            "🕐 Zeitfenster",
		BtnVenueEditAddress:              "📍 Adresse",
		BtnVenueDelete:                   "🗑 Löschen",
		BtnVenueAdd:                      "+ Ort hinzufügen",
		BtnVenueConfirmDelete:            "Ja, löschen",
		BtnVenueEditGameDays:             "📅 Spieltage",
		BtnVenueEditGracePeriod:          "⏱ Kulanzzeit",
		BtnVenueEditPreferredTime:        "⭐ Bevorzugte Zeit",
		BtnVenueClearPreferredTime:       "✕ Keine Präferenz",
		MsgVenueAskAutoBookingCourts:     "Platz-IDs für die automatische Buchung in Prioritätsreihenfolge eingeben (kommagetrennte Teilmenge von %s). - senden, um einen beliebigen freien Platz zu buchen:",
		MsgVenueAutoBookingCourtsLine:    "Auto-Buchungsplätze: %s",
		MsgVenueInvalidAutoBookingCourts: "Ungültige Plätze — jede ID muss in der Platzkonfiguration des Ortes enthalten sein. Bitte erneut versuchen:",
		BtnVenueEditAutoBookingCourts:    "🤖 Auto-Buchungsplätze",

		MsgVenueAskBookingOpensDays:  "Wie viele Tage im Voraus öffnet die Buchung? (Standard: 14). Sende - für Standard:",
		MsgVenueBookingOpensDaysLine: "Buchung öffnet: %d Tage vorher",
		BtnVenueEditBookingOpensDays: "📆 Buchung öffnet",

		GameVenueLine: "📍 %s",

		// Weekdays
		WeekdaySunday:    "Sonntag",
		WeekdayMonday:    "Montag",
		WeekdayTuesday:   "Dienstag",
		WeekdayWednesday: "Mittwoch",
		WeekdayThursday:  "Donnerstag",
		WeekdayFriday:    "Freitag",
		WeekdaySaturday:  "Samstag",

		// Months (German nominative — used as "Day. Month" in game date)
		MonthJanuary:   "Januar",
		MonthFebruary:  "Februar",
		MonthMarch:     "März",
		MonthApril:     "April",
		MonthMay:       "Mai",
		MonthJune:      "Juni",
		MonthJuly:      "Juli",
		MonthAugust:    "August",
		MonthSeptember: "September",
		MonthOctober:   "Oktober",
		MonthNovember:  "November",
		MonthDecember:  "Dezember",

		// Months (short)
		MonthShortJanuary:   "Jan",
		MonthShortFebruary:  "Feb",
		MonthShortMarch:     "Mär",
		MonthShortApril:     "Apr",
		MonthShortMay:       "Mai",
		MonthShortJune:      "Jun",
		MonthShortJuly:      "Jul",
		MonthShortAugust:    "Aug",
		MonthShortSeptember: "Sep",
		MonthShortOctober:   "Okt",
		MonthShortNovember:  "Nov",
		MonthShortDecember:  "Dez",

		// Auto-booking
		SchedAutoBookingSuccess:        "🤖 %d Plätze automatisch gebucht für %s am %s um %s.",
		SchedBookingReminderAutoBooked: "🤖 Plätze wurden automatisch gebucht für %s am %s um %s. Alles klar!",
		SchedAutoBookingFailDM:         "⚠️ Automatische Buchung für %s am %s um %s: %d von %d Plätzen gebucht. Bitte die restlichen Plätze manuell buchen.",
	},

	Ru: {
		// Game message
		GameHeader:      "🏸 Игра в сквош",
		GameCourts:      "🎾 Корты: %s (вместимость: %d игроков)",
		GamePlayers:     "Игроки (%d/%d):",
		GameGuestLine:   "+1 (от %s)",
		GameLastUpdated: "Обновлено: %s",
		GameCompleted:   "Игра завершена ✓",

		// Scheduler
		SchedOverCapacity:    "⚠️ Слишком много игроков! Записалось %d, но только %d мест (%d корта). Рассмотрите бронирование дополнительного корта.",
		SchedUnderCapacity:   "📢 Есть свободные места! Зарегистрировалось %d/%d игроков (%d корта). Приглашайте друзей!",
		SchedWeeklyReminder:  "👋 Напоминание: на эту неделю ещё не запланировано ни одной игры в сквош. Не забудь создать!",
		SchedBookingReminder: "📅 Напоминание о бронировании для *%s*: игра через %d дней — бронирование открыто сейчас! Не забудь зарезервировать корты.",

		SchedReminderAllGood:      "✅ Игра %s — всё отлично! %d/%d игроков, %d корт(а) подтверждено.",
		SchedReminderCanceled:     "✅ Корт(а) %s отменены. Игра %s — всё в порядке! %d/%d игроков, %d корт(а).",
		SchedReminderOddNoCancel:  "⚠️ 1 свободное место на игру %s. %d/%d игроков, %d корт(а).",
		SchedReminderOddCanceled:  "⚠️ Корт(а) %s отменены. 1 свободное место на игру %s. %d/%d игроков, %d корт(а).",
		SchedReminderAllCanceled:  "❌ Все корты (%s) отменены для игры %s. Игра не состоится.",
		SchedReminderEvenNoCancel: "⚠️ Игра %s — %d/%d игроков, %d корт(а). Отмените незаполненные корты.",

		// Keyboard buttons
		BtnImIn:     "Я играю",
		BtnIllSkip:  "Пропускаю",
		BtnPlusOne:  "+1",
		BtnMinusOne: "-1",

		BtnKickPlayer:  "Убрать игрока",
		BtnKickGuest:   "Убрать гостя",
		BtnEditCourts:  "Изменить корты",
		BtnClose:       "✕ Закрыть",
		BtnBack:        "← Назад",
		BtnViewInGroup: "Открыть в группе →",

		BtnDayBefore:            "Проверка за день",
		BtnDayAfter:             "Очистка после игры",
		BtnWeeklyReminder:       "Еженедельное напоминание",
		BtnCancellationReminder: "Напоминание об отмене",
		BtnDayAfterCleanup:      "Очистка после игры",
		BtnBookingReminder:      "Напоминание о бронировании",
		BtnAutoBooking:          "Автобронирование",

		BtnLangEn: "🇬🇧 English",
		BtnLangDe: "🇩🇪 Deutsch",
		BtnLangRu: "🇷🇺 Русский",

		// Handler messages
		MsgSomethingWentWrong:        "Что-то пошло не так, попробуй ещё раз",
		MsgGameFullCapacity:          "Игра уже заполнена",
		MsgNoGuestsToRemove:          "Ты не приглашал гостей",
		MsgNoPlayersToKick:           "Нет зарегистрированных игроков для удаления",
		MsgSelectPlayerToKick:        "Выбери игрока для удаления:",
		MsgPlayerKicked:              "Игрок удалён ✓",
		MsgNoGuestsToKick:            "Нет гостей для удаления",
		MsgSelectGuestToKick:         "Выбери гостя для удаления:",
		MsgGuestKicked:               "Гость удалён ✓",
		MsgNotAuthorized:             "Нет прав доступа",
		MsgUnknownEvent:              "Неизвестное событие",
		MsgFailedTrigger:             "Не удалось запустить — проверьте состояние сервиса",
		MsgTriggered:                 "Запущено ✓",
		MsgManageGameHeader:          "*Управление игрой:*\n📅 %s · %s\n🎾 Корты: %s\nИгроки: %d/%d, Гостей: %d",
		MsgSendGameDetails:           "Отправь детали игры после команды:\n/new\\_game\nГГГГ-ММ-ДД ЧЧ:ММ\ncourts: 2,3,4",
		MsgManagementPrivateOnly:     "Команды управления работают в личных сообщениях. Напиши мне в личку и используй /help.",
		MsgFailedVerifyPermissions:   "Не удалось проверить права",
		MsgOnlyAdminCreate:           "Только администраторы группы могут создавать игры",
		MsgInvalidFormat:             "Неверный формат. Используй:\nГГГГ-ММ-ДД ЧЧ:ММ\ncourts: 2,3,4",
		MsgWhichGroup:                "В какую группу опубликовать объявление об игре?",
		MsgSessionExpired:            "Сессия истекла, пожалуйста отправь детали игры снова",
		MsgNotAdminInGroup:           "Ты не администратор этой группы",
		MsgCreatingGame:              "Создаём игру...",
		MsgFailedCreateGame:          "Не удалось создать игру",
		MsgGameCreatedFailedAnnounce: "Игра создана, но не удалось отправить объявление",
		MsgGameCreatedPinned:         "Игра создана и закреплена ✓",
		MsgGameNotFound:              "Игра не найдена",
		MsgNoUpcomingGames:           "Нет предстоящих игр в твоих группах.",
		MsgSendNewCourts:             "Отправь номера новых кортов (например: 2,3,4):",
		MsgLostAdminAccess:           "У тебя больше нет прав администратора в этой группе",
		MsgKickPlayerLabel:           "Убрать %s",
		MsgKickGuestLabel:            "Убрать +1 (от %s)",
		MsgKickPlayerNotFound:        "Игрок не найден в этой игре",
		MsgGuestNotFound:             "Гость не найден",

		// Commands
		MsgUnknownCommand:                "Неизвестная команда. Отправь /help, чтобы увидеть доступные команды.",
		MsgAvailableCommands:             "Доступные команды:\n",
		MsgCmdMyGame:                     "/mygame — Показать твою следующую игру\n",
		MsgCmdHelp:                       "/help — Показать эту справку\n",
		MsgCmdLanguage:                   "/language — Установить язык бота для твоей группы\n",
		MsgAdminCommands:                 "\nКоманды администратора:\n",
		MsgCmdNewGame:                    "/newgame — Создать новую игру\n",
		MsgCmdGames:                      "/games — Показать и управлять предстоящими играми\n",
		MsgCmdVenues:                     "/venues — Управлять площадками для твоей группы\n",
		MsgServiceAdminCommands:          "\nКоманды сервисного администратора:\n",
		MsgCmdTrigger:                    "/trigger — Вручную запустить запланированное событие\n",
		MsgFailedFetchGame:               "Не удалось получить твою следующую игру. Попробуй ещё раз.",
		MsgNoUpcomingRegistered:          "У тебя нет предстоящих зарегистрированных игр.",
		MsgFailedFetchDetails:            "Не удалось получить детали игры. Попробуй ещё раз.",
		MsgYourNextGame:                  "Твоя следующая игра:\n\n",
		MsgOnlyAdminCanUse:               "Только администраторы группы могут использовать эту команду.",
		MsgFailedFetchGames:              "Не удалось получить игры. Попробуй ещё раз.",
		MsgFailedFetchGroupInfo:          "Не удалось получить информацию о группе. Попробуй ещё раз.",
		MsgSendGameDetailsCmd:            "Отправь детали игры после команды:\n\n/new\\_game\nГГГГ-ММ-ДД ЧЧ:ММ\ncourts: 2,3,4",
		MsgInvalidFormatCmd:              "Неверный формат. Используй:\n\n/new\\_game\nГГГГ-ММ-ДД ЧЧ:ММ\ncourts: 2,3,4",
		MsgOnlyAdminCreateGames:          "Только администраторы группы могут создавать игры.",
		MsgCourtsUpdated:                 "Корты обновлены: %s ✓",
		MsgFailedUpdateCourts:            "Не удалось обновить корты. Попробуй ещё раз.",
		MsgCourtsUpdatedRefreshFailed:    "Корты обновлены, но не удалось обновить сообщение в группе.",
		MsgInvalidCourtsFormat:           "Неверный формат. Ожидается, например: 2,3,4",
		MsgCourtsStringTooLong:           "Строка кортов слишком длинная (макс. %d символов)",
		MsgGameNotFoundPeriod:            "Игра не найдена.",
		MsgFailedVerifyPermissionsPeriod: "Не удалось проверить права.",
		MsgLostAdminAccessPeriod:         "У тебя больше нет прав администратора в этой группе.",
		MsgNotAuthorizedCmd:              "У тебя нет прав для использования этой команды.",
		MsgSelectTriggerEvent:            "Выбери запланированное событие для ручного запуска:",
		MsgUpcomingGames:                 "*Предстоящие игры:*\n\n",
		MsgGameCourtsCapacity:            "🎾 Корты: %s — вместимость %d\n",
		MsgGroupLabel:                    "Группа: %s\n\n",
		MsgManageGameBtn:                 "Управлять: %s %s",

		// Membership
		MsgAddedNoAdmin: "Я был добавлен в \"%s\", но не имею прав администратора.\n\nЧтобы закрепить объявления об играх, пожалуйста, предоставьте мне права администратора в этой группе.",
		MsgLostAdmin:    "Я потерял права администратора в \"%s\".\n\nБез прав администратора я больше не могу закреплять объявления об играх.",

		// Language command
		MsgSelectGroupForLanguage: "Для какой группы изменить язык?",
		MsgSelectGroupForVenues:   "Для какой группы управлять площадками?",
		MsgSelectLanguage:         "Выбери язык для своей группы:",
		MsgLanguageSet:            "Язык обновлён ✓",
		MsgOnlyAdminSetLanguage:   "Только администраторы группы могут менять язык.",
		MsgSelectGroupForTimezone: "Для какой группы изменить часовой пояс?",
		MsgSelectTimezone:         "Выбери часовой пояс для своей группы:",
		MsgTimezoneSet:            "Часовой пояс обновлён ✓",
		BtnSetTimezone:            "🕐 Часовой пояс",

		// New game wizard
		MsgNewGameSelectDate:         "Выбери дату новой игры:",
		MsgNewGameEnterTime:          "Игра %s.\n\nВведи время (ЧЧ:ММ, например 19:30):",
		MsgNewGameInvalidTime:        "Неверный формат времени. Введи время как ЧЧ:ММ (например 19:30):",
		MsgNewGameTimePast:           "Это время уже прошло. Введи будущее время (например 19:30):",
		MsgNewGameEnterCourts:        "Игра %s в %s.\n\nВведи корты (например 2,3 или 2 3):",
		MsgNewGameSelectVenue:        "Игра %s.\n\nВыбери площадку:",
		MsgNewGameNoVenue:            "Без площадки / вручную",
		MsgNewGameSelectCourts:       "Выбери корты для игры (нажми для переключения):",
		MsgNewGameConfirmCourts:      "✓ Подтвердить (%s)",
		MsgNewGameNoCourtsSelected:   "Пожалуйста, выбери хотя бы один корт.",
		MsgNewGameSelectTime:         "Игра %s в %s.\n\nВыбери временной слот:",
		MsgNewGameCustomTime:         "✎ Своё время",
		MsgNewGameNoVenuesConfigured: "Для вашей группы не настроены площадки. Добавьте хотя бы одну площадку через /venues, прежде чем создавать игру.",

		// Venue management
		MsgVenueList:                     "*Площадки для %s:*\n\n",
		MsgVenueNoVenues:                 "Площадки ещё не настроены.",
		MsgVenueCreated:                  "Площадка создана ✓",
		MsgVenueUpdated:                  "Площадка обновлена ✓",
		MsgVenueDeleted:                  "Площадка удалена ✓",
		MsgVenueNotFound:                 "Площадка не найдена.",
		MsgVenueAskName:                  "Введи название площадки (например, Городской спортивный центр):",
		MsgVenueAskCourts:                "Введи доступные корты (например, 1,2,3,4,5,6):",
		MsgVenueAskTimeSlots:             "Введи временные слоты (например, 18:00,19:00,20:00) или отправь - для пропуска:",
		MsgVenueAskAddress:               "Введи адрес или ссылку на площадку (необязательно) или отправь - для пропуска:",
		MsgVenueSkipAddress:              "-",
		MsgVenueEditMenu:                 "*%s*\nКорты: %s\nВременные слоты: %s\nАдрес: %s",
		MsgVenueConfirmDelete:            "Удалить площадку *%s*? Это действие нельзя отменить.",
		MsgVenueInvalidTimeSlots:         "Неверный формат временных слотов. Каждый слот должен быть в формате ЧЧ:ММ (например 18:00,19:00,20:00):",
		MsgVenueAskGameDays:              "В какие дни проходят игры на этой площадке? Нажми для выбора, затем подтверди. Отправь - для пропуска.",
		MsgVenueAskGracePeriod:           "Период отмены для напоминаний в часах (по умолчанию: 24). Отправь - для значения по умолчанию:",
		MsgVenueConfirmDays:              "✓ Подтвердить",
		MsgVenueAskPreferredTime:         "Выбери предпочтительное время игры на этой площадке (будет выделено при создании новой игры):",
		MsgVenueNoPreferredTime:          "Предпочтительное время не задано.",
		MsgVenuePreferredTimeLine:        "Предпочт. время: %s",
		BtnVenueEditName:                 "✏️ Название",
		BtnVenueEditCourts:               "🎾 Корты",
		BtnVenueEditTimeSlots:            "🕐 Временные слоты",
		BtnVenueEditAddress:              "📍 Адрес",
		BtnVenueDelete:                   "🗑 Удалить",
		BtnVenueAdd:                      "+ Добавить площадку",
		BtnVenueConfirmDelete:            "Да, удалить",
		BtnVenueEditGameDays:             "📅 Игровые дни",
		BtnVenueEditGracePeriod:          "⏱ Период отмены",
		BtnVenueEditPreferredTime:        "⭐ Предпочт. время",
		BtnVenueClearPreferredTime:       "✕ Без предпочтений",
		MsgVenueAskAutoBookingCourts:     "Введи ID кортов для автобронирования в порядке приоритета (через запятую, подмножество %s). Отправь - для бронирования любого свободного корта:",
		MsgVenueAutoBookingCourtsLine:    "Корты автобронирования: %s",
		MsgVenueInvalidAutoBookingCourts: "Неверные корты — каждый ID должен быть в списке кортов площадки. Попробуй снова:",
		BtnVenueEditAutoBookingCourts:    "🤖 Корты автобронирования",

		MsgVenueAskBookingOpensDays:  "За сколько дней открывается бронирование? (по умолчанию: 14). Отправь - для значения по умолчанию:",
		MsgVenueBookingOpensDaysLine: "Бронирование: за %d дней",
		BtnVenueEditBookingOpensDays: "📆 Бронирование",

		GameVenueLine: "📍 %s",

		// Weekdays (nominative)
		WeekdaySunday:    "Воскресенье",
		WeekdayMonday:    "Понедельник",
		WeekdayTuesday:   "Вторник",
		WeekdayWednesday: "Среда",
		WeekdayThursday:  "Четверг",
		WeekdayFriday:    "Пятница",
		WeekdaySaturday:  "Суббота",

		// Months (Russian genitive — used in "22 марта" style dates)
		MonthJanuary:   "января",
		MonthFebruary:  "февраля",
		MonthMarch:     "марта",
		MonthApril:     "апреля",
		MonthMay:       "мая",
		MonthJune:      "июня",
		MonthJuly:      "июля",
		MonthAugust:    "августа",
		MonthSeptember: "сентября",
		MonthOctober:   "октября",
		MonthNovember:  "ноября",
		MonthDecember:  "декабря",

		// Months (short)
		MonthShortJanuary:   "янв",
		MonthShortFebruary:  "фев",
		MonthShortMarch:     "мар",
		MonthShortApril:     "апр",
		MonthShortMay:       "май",
		MonthShortJune:      "июн",
		MonthShortJuly:      "июл",
		MonthShortAugust:    "авг",
		MonthShortSeptember: "сен",
		MonthShortOctober:   "окт",
		MonthShortNovember:  "ноя",
		MonthShortDecember:  "дек",

		// Auto-booking
		SchedAutoBookingSuccess:        "🤖 Автоматически забронировано %d кортов для %s на %s в %s.",
		SchedBookingReminderAutoBooked: "🤖 Корты автоматически забронированы для %s на %s в %s. Всё готово!",
		SchedAutoBookingFailDM:         "⚠️ Автобронирование для %s на %s в %s: забронировано %d из %d кортов. Пожалуйста, забронируйте оставшиеся корты вручную.",
	},
}

// Localizer holds a resolved language and provides localised strings.
type Localizer struct {
	lang Lang
}

// New returns a Localizer for the given Lang.
func New(lang Lang) *Localizer {
	return &Localizer{lang: lang}
}

// Lang returns the language this Localizer uses.
func (l *Localizer) Lang() Lang { return l.lang }

// T returns the translation for key in the localizer's language,
// falling back to English if the key is missing.
func (l *Localizer) T(key string) string {
	if m, ok := translations[l.lang]; ok {
		if s, ok := m[key]; ok {
			return s
		}
	}
	if s, ok := translations[En][key]; ok {
		return s
	}
	return key
}

// Tf is like T but passes the translated string through fmt.Sprintf with args.
func (l *Localizer) Tf(key string, args ...any) string {
	return fmt.Sprintf(l.T(key), args...)
}

// FormatGameDate returns a locale-specific date string for game announcements.
//
//	English: "Sunday, March 22"
//	German:  "Sonntag, 22. März"
//	Russian: "Воскресенье, 22 марта"
func (l *Localizer) FormatGameDate(t time.Time) string {
	weekday := l.T(weekdayKey(t.Weekday()))
	month := l.T(monthKey(t.Month()))
	switch l.lang {
	case De:
		return fmt.Sprintf("%s, %d. %s", weekday, t.Day(), month)
	case Ru:
		return fmt.Sprintf("%s, %d %s", weekday, t.Day(), month)
	default:
		return fmt.Sprintf("%s, %s %d", weekday, month, t.Day())
	}
}

// FormatUpdatedAt returns a locale-specific "last updated" timestamp.
// Format is "D Mon YYYY, HH:MM" in all locales (only the month abbreviation is localised).
func (l *Localizer) FormatUpdatedAt(t time.Time) string {
	month := l.T(monthShortKey(t.Month()))
	return fmt.Sprintf("%d %s %d, %s", t.Day(), month, t.Year(), t.Format("15:04"))
}

// FormatDayMonth returns a short date string like "22 Mar" for button labels.
func (l *Localizer) FormatDayMonth(t time.Time) string {
	month := l.T(monthShortKey(t.Month()))
	return fmt.Sprintf("%d %s", t.Day(), month)
}

// ShortWeekday returns the first three runes of the localized weekday name.
// Used for compact date-picker button labels (e.g. "Mon", "Mo", "Пон").
func (l *Localizer) ShortWeekday(w time.Weekday) string {
	full := []rune(l.T(weekdayKey(w)))
	if len(full) <= 3 {
		return string(full)
	}
	return string(full[:3])
}

// weekdayKey maps a time.Weekday to its translation key.
func weekdayKey(w time.Weekday) string {
	switch w {
	case time.Sunday:
		return WeekdaySunday
	case time.Monday:
		return WeekdayMonday
	case time.Tuesday:
		return WeekdayTuesday
	case time.Wednesday:
		return WeekdayWednesday
	case time.Thursday:
		return WeekdayThursday
	case time.Friday:
		return WeekdayFriday
	default:
		return WeekdaySaturday
	}
}

// monthKey maps a time.Month to its full-name translation key.
func monthKey(m time.Month) string {
	switch m {
	case time.January:
		return MonthJanuary
	case time.February:
		return MonthFebruary
	case time.March:
		return MonthMarch
	case time.April:
		return MonthApril
	case time.May:
		return MonthMay
	case time.June:
		return MonthJune
	case time.July:
		return MonthJuly
	case time.August:
		return MonthAugust
	case time.September:
		return MonthSeptember
	case time.October:
		return MonthOctober
	case time.November:
		return MonthNovember
	default:
		return MonthDecember
	}
}

// monthShortKey maps a time.Month to its abbreviated translation key.
func monthShortKey(m time.Month) string {
	switch m {
	case time.January:
		return MonthShortJanuary
	case time.February:
		return MonthShortFebruary
	case time.March:
		return MonthShortMarch
	case time.April:
		return MonthShortApril
	case time.May:
		return MonthShortMay
	case time.June:
		return MonthShortJune
	case time.July:
		return MonthShortJuly
	case time.August:
		return MonthShortAugust
	case time.September:
		return MonthShortSeptember
	case time.October:
		return MonthShortOctober
	case time.November:
		return MonthShortNovember
	default:
		return MonthShortDecember
	}
}
