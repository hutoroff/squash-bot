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
	SchedOverCapacity   = "sched.over_capacity"
	SchedUnderCapacity  = "sched.under_capacity"
	SchedWeeklyReminder = "sched.weekly_reminder"

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
	BtnDayBefore      = "btn.day_before"
	BtnDayAfter       = "btn.day_after"
	BtnWeeklyReminder = "btn.weekly_reminder"

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
	MsgSelectLanguage         = "msg.select_language"
	MsgLanguageSet            = "msg.language_set"
	MsgOnlyAdminSetLanguage   = "msg.only_admin_set_language"

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
		SchedOverCapacity:   "⚠️ Too many players! %d registered but only %d spots (%d courts). Consider booking an extra court.",
		SchedUnderCapacity:  "📢 Free spots available! %d/%d players registered (%d courts). Invite more friends!",
		SchedWeeklyReminder: "👋 Reminder: no squash game has been scheduled for this week yet. Don't forget to create one!",

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

		BtnDayBefore:      "Day Before Check",
		BtnDayAfter:       "Day After Cleanup",
		BtnWeeklyReminder: "Weekly Reminder",

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
		MsgCmdMyGame:                     "/my\\_game — Show your next upcoming game\n",
		MsgCmdHelp:                       "/help — Show this help message\n",
		MsgCmdLanguage:                   "/language — Set the bot language for your group\n",
		MsgAdminCommands:                 "\nAdmin commands:\n",
		MsgCmdNewGame:                    "/new\\_game — Create a new game\n",
		MsgCmdGames:                      "/games — Show and manage upcoming games\n",
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
		MsgSelectLanguage:         "Select a language for your group:",
		MsgLanguageSet:            "Language updated ✓",
		MsgOnlyAdminSetLanguage:   "Only group administrators can change the language.",

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
		SchedOverCapacity:   "⚠️ Zu viele Spieler! %d angemeldet, aber nur %d Plätze (%d Plätze). Erwäge, einen weiteren Platz zu buchen.",
		SchedUnderCapacity:  "📢 Freie Plätze verfügbar! %d/%d Spieler angemeldet (%d Plätze). Lade mehr Freunde ein!",
		SchedWeeklyReminder: "👋 Erinnerung: Für diese Woche ist noch kein Squash-Spiel geplant. Vergiss nicht, eines zu erstellen!",

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

		BtnDayBefore:      "Tag-Vorher-Prüfung",
		BtnDayAfter:       "Tag-Danach-Bereinigung",
		BtnWeeklyReminder: "Wöchentliche Erinnerung",

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
		MsgCmdMyGame:                     "/my\\_game — Dein nächstes Spiel anzeigen\n",
		MsgCmdHelp:                       "/help — Diese Hilfe anzeigen\n",
		MsgCmdLanguage:                   "/language — Botsprache für deine Gruppe festlegen\n",
		MsgAdminCommands:                 "\nAdmin-Befehle:\n",
		MsgCmdNewGame:                    "/new\\_game — Ein neues Spiel erstellen\n",
		MsgCmdGames:                      "/games — Bevorstehende Spiele anzeigen und verwalten\n",
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
		MsgSelectLanguage:         "Sprache für deine Gruppe auswählen:",
		MsgLanguageSet:            "Sprache aktualisiert ✓",
		MsgOnlyAdminSetLanguage:   "Nur Gruppenadministratoren können die Sprache ändern.",

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
		SchedOverCapacity:   "⚠️ Слишком много игроков! Записалось %d, но только %d мест (%d корта). Рассмотрите бронирование дополнительного корта.",
		SchedUnderCapacity:  "📢 Есть свободные места! Зарегистрировалось %d/%d игроков (%d корта). Приглашайте друзей!",
		SchedWeeklyReminder: "👋 Напоминание: на эту неделю ещё не запланировано ни одной игры в сквош. Не забудь создать!",

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

		BtnDayBefore:      "Проверка за день",
		BtnDayAfter:       "Очистка после игры",
		BtnWeeklyReminder: "Еженедельное напоминание",

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
		MsgCmdMyGame:                     "/my\\_game — Показать твою следующую игру\n",
		MsgCmdHelp:                       "/help — Показать эту справку\n",
		MsgCmdLanguage:                   "/language — Установить язык бота для твоей группы\n",
		MsgAdminCommands:                 "\nКоманды администратора:\n",
		MsgCmdNewGame:                    "/new\\_game — Создать новую игру\n",
		MsgCmdGames:                      "/games — Показать и управлять предстоящими играми\n",
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
		MsgSelectLanguage:         "Выбери язык для своей группы:",
		MsgLanguageSet:            "Язык обновлён ✓",
		MsgOnlyAdminSetLanguage:   "Только администраторы группы могут менять язык.",

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
