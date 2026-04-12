package telegram

// White-box unit tests for pure functions in commands.go and handlers.go.
// No database or Telegram API is needed.

import (
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/vkhutorov/squash_bot/internal/i18n"
	"github.com/vkhutorov/squash_bot/internal/models"
)

// --- superGroupMessageLink ---

func TestSuperGroupMessageLink_Supergroup(t *testing.T) {
	// Standard supergroup chat ID: -100XXXXXXXXX
	link := superGroupMessageLink(-1001234567890, 42)
	const want = "https://t.me/c/1234567890/42"
	if link != want {
		t.Errorf("got %q, want %q", link, want)
	}
}

func TestSuperGroupMessageLink_ShortChannelID(t *testing.T) {
	link := superGroupMessageLink(-1009876543, 1)
	const want = "https://t.me/c/9876543/1"
	if link != want {
		t.Errorf("got %q, want %q", link, want)
	}
}

func TestSuperGroupMessageLink_RegularGroup_Empty(t *testing.T) {
	// Regular group: negative but doesn't start with -100.
	link := superGroupMessageLink(-12345678, 99)
	if link != "" {
		t.Errorf("regular group should produce empty link, got %q", link)
	}
}

func TestSuperGroupMessageLink_NilMessageID_Zero(t *testing.T) {
	// message_id = 0 is technically invalid but must not panic.
	link := superGroupMessageLink(-1001000000000, 0)
	if !strings.Contains(link, "/0") {
		t.Errorf("expected message_id 0 in link, got %q", link)
	}
}

// --- escapeMarkdown ---

func TestEscapeMarkdown_NoSpecialChars(t *testing.T) {
	got := escapeMarkdown("Hello World")
	if got != "Hello World" {
		t.Errorf("plain text should be unchanged, got %q", got)
	}
}

func TestEscapeMarkdown_Underscores(t *testing.T) {
	got := escapeMarkdown("squash_friends")
	if got != `squash\_friends` {
		t.Errorf("underscores should be escaped, got %q", got)
	}
}

func TestEscapeMarkdown_Stars(t *testing.T) {
	got := escapeMarkdown("**bold**")
	if got != `\*\*bold\*\*` {
		t.Errorf("stars should be escaped, got %q", got)
	}
}

func TestEscapeMarkdown_Mixed(t *testing.T) {
	got := escapeMarkdown("my_group [best]")
	if !strings.Contains(got, `my\_group`) {
		t.Errorf("underscore not escaped in %q", got)
	}
	// escapeMarkdown escapes '[' but not ']' (only the opener creates a link in Markdown v1).
	if !strings.Contains(got, `\[best]`) {
		t.Errorf("opening bracket not escaped in %q", got)
	}
}

// --- parseAdminCommand (retained for testing; no longer called from production handlers) ---

// TestParseAdminCommand_NewGamePrefix exercises the core parsing logic with a
// sample input that resembles the old /new_game command format.
func TestParseAdminCommand_NewGamePrefix(t *testing.T) {
	loc := time.UTC
	raw := "/new_game\n2026-06-01 18:00\ncourts: 2,3,4"
	lines := strings.SplitN(strings.TrimSpace(raw), "\n", 2)
	if len(lines) < 2 {
		t.Fatal("expected 2 lines after split")
	}
	body := strings.TrimSpace(lines[1])

	gameDate, courts, err := parseAdminCommand(body, loc)
	if err != nil {
		t.Fatalf("parseAdminCommand: %v", err)
	}
	if courts != "2,3,4" {
		t.Errorf("courts: got %q, want %q", courts, "2,3,4")
	}
	if gameDate.Year() != 2026 || gameDate.Month() != 6 || gameDate.Day() != 1 {
		t.Errorf("gameDate: got %v, want 2026-06-01", gameDate)
	}
}

func TestParseAdminCommand_NewGamePrefixOnly_Error(t *testing.T) {
	// Only "/new_game" with no body should not reach parseAdminCommand.
	// This replicates the guard in handleCommandNewGame.
	raw := "/new_game"
	lines := strings.SplitN(strings.TrimSpace(raw), "\n", 2)
	hasBody := len(lines) >= 2 && strings.TrimSpace(lines[1]) != ""
	if hasBody {
		t.Error("single-line /new_game should have no body")
	}
}

// --- courts validation (used by processCourtsEdit) ---

// TestCourtsValidation_Valid checks the validation logic applied in processCourtsEdit.
func TestCourtsValidation_Valid(t *testing.T) {
	cases := []string{"1", "2,3,4", "10,11"}
	for _, c := range cases {
		parts := strings.Split(c, ",")
		for _, p := range parts {
			if strings.TrimSpace(p) == "" {
				t.Errorf("valid courts %q failed validation", c)
			}
		}
	}
}

func TestCourtsValidation_Invalid(t *testing.T) {
	cases := []string{"", ",", "1,,2"}
	for _, c := range cases {
		parts := strings.Split(c, ",")
		anyEmpty := false
		for _, p := range parts {
			if strings.TrimSpace(p) == "" {
				anyEmpty = true
			}
		}
		if !anyEmpty {
			t.Errorf("invalid courts %q passed validation, should have failed", c)
		}
	}
}

// --- group command routing (regression: commands in groups were falling to parseAdminCommand) ---

// TestIsBotMentioned_AddressedBotCommand verifies that isBotMentioned returns true
// for a "/command@botname" bot_command entity so that isKnownGroupMention lets the
// message through and the slash-command check in handleMessage routes it correctly.
func TestIsBotMentioned_AddressedBotCommand(t *testing.T) {
	bot := newMinimalBot("testbot")
	// "/help@testbot" is 13 UTF-16 code units (all ASCII).
	text := "/help@testbot"
	msg := &tgbotapi.Message{
		Text: text,
		Entities: []tgbotapi.MessageEntity{
			{Type: "bot_command", Offset: 0, Length: 13},
		},
	}
	if !bot.isBotMentioned(msg) {
		t.Error("isBotMentioned: expected true for /command@botname bot_command entity")
	}
}

// TestIsBotMentioned_BotCommandAddressedToOtherBot verifies that a bot_command
// addressed to a different bot is not treated as a mention of this bot.
func TestIsBotMentioned_BotCommandAddressedToOtherBot(t *testing.T) {
	bot := newMinimalBot("testbot")
	text := "/help@otherbot"
	msg := &tgbotapi.Message{
		Text: text,
		Entities: []tgbotapi.MessageEntity{
			{Type: "bot_command", Offset: 0, Length: 14},
		},
	}
	if bot.isBotMentioned(msg) {
		t.Error("isBotMentioned: expected false for /command@otherbot entity")
	}
}

// TestIsBotMentioned_BotCommandWithoutBotName verifies that a bare "/help" (no @name)
// is NOT treated as an addressed mention — bare commands in groups are ignored unless
// the bot is explicitly addressed.
func TestIsBotMentioned_BotCommandWithoutBotName(t *testing.T) {
	bot := newMinimalBot("testbot")
	text := "/help"
	msg := &tgbotapi.Message{
		Text: text,
		Entities: []tgbotapi.MessageEntity{
			{Type: "bot_command", Offset: 0, Length: 5},
		},
	}
	if bot.isBotMentioned(msg) {
		t.Error("isBotMentioned: expected false for bare /command without @botname")
	}
}

// TestGroupMentionFirst_CommandStrippedAndRecognised is the regression test for the
// "@bot /help" pattern (mention entity before the slash-command text).
// isBotMentioned must detect the @mention so isKnownGroupMention lets the message
// through; after stripBotMention the remaining text starts with "/" and must be
// routed to handleCommand rather than parseAdminCommand.
func TestGroupMentionFirst_CommandStrippedAndRecognised(t *testing.T) {
	bot := newMinimalBot("testbot")
	// "@testbot" is at offset 0, length 8 (all ASCII).
	text := "@testbot /help"
	entities := []tgbotapi.MessageEntity{{Type: "mention", Offset: 0, Length: 8}}
	msg := &tgbotapi.Message{Text: text, Entities: entities}

	if !bot.isBotMentioned(msg) {
		t.Fatal("isBotMentioned: expected true — @mention enables isKnownGroupMention")
	}

	stripped := strings.TrimSpace(stripBotMention(text, bot.api.Self.UserName, entities))
	if !strings.HasPrefix(stripped, "/") {
		t.Errorf("after stripping @mention, text %q should start with '/' to be "+
			"routed as a command (got %q)", text, stripped)
	}
}

// --- parseAdminCommand security validations ---

// TestParseAdminCommand_PastDate ensures a date in the past is rejected with an
// error mentioning "future", which matches the message shown to admins.
func TestParseAdminCommand_PastDate(t *testing.T) {
	loc := time.UTC
	raw := "2020-01-01 18:00\ncourts: 1,2"
	_, _, err := parseAdminCommand(raw, loc)
	if err == nil {
		t.Fatal("expected error for past date, got nil")
	}
	if !strings.Contains(err.Error(), "future") {
		t.Errorf("error message should mention 'future', got %q", err.Error())
	}
}

// TestParseAdminCommand_CourtsLengthLimit ensures courts strings longer than
// maxCourtsLen are rejected before reaching the database.
func TestParseAdminCommand_CourtsLengthLimit(t *testing.T) {
	loc := time.UTC
	longCourts := strings.Repeat("1,", 60) // 120 chars — well above the 100-char cap
	raw := "2030-01-01 18:00\ncourts: " + longCourts
	_, _, err := parseAdminCommand(raw, loc)
	if err == nil {
		t.Fatal("expected error for courts string exceeding maxCourtsLen, got nil")
	}
}

// TestParseAdminCommand_MissingCourtsPrefix is a regression test for the bug where
// `strings.TrimPrefix` was used unconditionally, so bare court numbers like "2,3,4"
// (without the "courts:" prefix) were silently accepted and stored.
func TestParseAdminCommand_MissingCourtsPrefix(t *testing.T) {
	loc := time.UTC
	cases := []string{
		"2030-01-01 18:00\n2,3,4",         // bare numbers
		"2030-01-01 18:00\n court: 2,3,4", // wrong keyword
		"2030-01-01 18:00\nCourts: 2,3,4", // wrong case (prefix match is case-sensitive)
	}
	for _, raw := range cases {
		_, _, err := parseAdminCommand(raw, loc)
		if err == nil {
			t.Errorf("parseAdminCommand(%q): expected error for missing 'courts:' prefix, got nil", raw)
		}
	}
}

// TestParseAdminCommand_TrailingComma is a regression test for the inconsistency
// where processCourtsEdit rejects "2," but parseAdminCommand used to accept it,
// producing a spurious empty court entry and an inflated courts_count.
func TestParseAdminCommand_TrailingComma(t *testing.T) {
	loc := time.UTC
	cases := []string{
		"2030-01-01 18:00\ncourts: 2,",
		"2030-01-01 18:00\ncourts: ,3",
		"2030-01-01 18:00\ncourts: 1,,2",
		"2030-01-01 18:00\ncourts: ,",
	}
	for _, raw := range cases {
		_, _, err := parseAdminCommand(raw, loc)
		if err == nil {
			t.Errorf("parseAdminCommand(%q): expected error for empty court part, got nil", raw)
		}
	}
}

// TestParseAdminCommand_ValidCourts ensures well-formed inputs still pass.
func TestParseAdminCommand_ValidCourts(t *testing.T) {
	loc := time.UTC
	cases := []struct {
		input  string
		courts string
	}{
		{"2030-01-01 18:00\ncourts: 2,3,4", "2,3,4"},
		{"2030-01-01 18:00\ncourts: 5", "5"},
		{"2030-01-01 18:00\ncourts: 10,11", "10,11"},
	}
	for _, tc := range cases {
		_, courts, err := parseAdminCommand(tc.input, loc)
		if err != nil {
			t.Errorf("parseAdminCommand(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if courts != tc.courts {
			t.Errorf("parseAdminCommand(%q): got courts %q, want %q", tc.input, courts, tc.courts)
		}
	}
}

// --- formatGamesListMessage ---

// TestFormatGamesListMessage_PerGroupTimezone is a regression test for the bug
// where all games were displayed using b.loc (a single bot-wide timezone) instead
// of each group's own timezone. Both games are at the same UTC instant but belong
// to groups with different timezones, so the displayed times must differ.
func TestFormatGamesListMessage_PerGroupTimezone(t *testing.T) {
	// 17:00 UTC → 17:00 in UTC+0, 20:00 in UTC+3
	utcTime := time.Date(2026, 6, 1, 17, 0, 0, 0, time.UTC)

	games := []*models.Game{
		{ID: 1, ChatID: -1001, Courts: "1", CourtsCount: 1, GameDate: utcTime},
		{ID: 2, ChatID: -1002, Courts: "2", CourtsCount: 1, GameDate: utcTime},
	}
	groups := []models.Group{
		{ChatID: -1001, Title: "Group UTC", Timezone: "UTC"},
		{ChatID: -1002, Title: "Group UTC+3", Timezone: "Etc/GMT-3"},
	}

	lz := i18n.New(i18n.En)
	text, _ := formatGamesListMessage(games, groups, lz)

	if !strings.Contains(text, "17:00") {
		t.Errorf("UTC group should display 17:00, got:\n%s", text)
	}
	if !strings.Contains(text, "20:00") {
		t.Errorf("UTC+3 group should display 20:00, got:\n%s", text)
	}
}

// TestFormatGamesListMessage_InvalidTimezone verifies that a game whose group has
// an invalid or missing timezone falls back to UTC without panicking.
func TestFormatGamesListMessage_InvalidTimezone(t *testing.T) {
	utcTime := time.Date(2026, 6, 1, 17, 0, 0, 0, time.UTC)
	games := []*models.Game{
		{ID: 1, ChatID: -1001, Courts: "1", CourtsCount: 1, GameDate: utcTime},
	}
	groups := []models.Group{
		{ChatID: -1001, Title: "Group Bad TZ", Timezone: "Not/AValidZone"},
	}

	lz := i18n.New(i18n.En)
	// Must not panic; falls back to UTC and shows 17:00.
	text, _ := formatGamesListMessage(games, groups, lz)

	if !strings.Contains(text, "17:00") {
		t.Errorf("invalid timezone should fall back to UTC (17:00), got:\n%s", text)
	}
}

// --- isValidTriggerEvent ---

func TestIsValidTriggerEvent(t *testing.T) {
	valid := []string{
		"cancellation_reminder",
		"day_after_cleanup",
		"booking_reminder",
		"auto_booking",
	}
	for _, e := range valid {
		if !isValidTriggerEvent(e) {
			t.Errorf("isValidTriggerEvent(%q): expected true", e)
		}
	}

	invalid := []string{"", "unknown", "auto_Booking", "AUTO_BOOKING", "trigger"}
	for _, e := range invalid {
		if isValidTriggerEvent(e) {
			t.Errorf("isValidTriggerEvent(%q): expected false", e)
		}
	}
}

// --- isBotMentioned UTF-16 offset correctness ---

// newMinimalBot returns a Bot with only the api.Self.UserName field set, which is
// all isBotMentioned needs.
func newMinimalBot(username string) *Bot {
	return &Bot{
		api: &tgbotapi.BotAPI{Self: tgbotapi.User{UserName: username}},
	}
}

// TestIsBotMentioned_EmojiBeforeMention is the key regression test for the UTF-16
// offset fix. 👋 (U+1F44B) occupies 2 UTF-16 code units (surrogate pair) but 4
// UTF-8 bytes. Telegram reports the @mention offset in UTF-16 units, so the old
// byte-index approach would extract the wrong slice and miss the mention.
func TestIsBotMentioned_EmojiBeforeMention(t *testing.T) {
	bot := newMinimalBot("testbot")
	// UTF-16 layout: [0xD83D, 0xDC4B] [0x20] [0x40 't' 'e' 's' 't' 'b' 'o' 't'] ...
	// emoji = 2 units, space = 1 unit → @testbot starts at UTF-16 offset 3, length 8.
	text := "👋 @testbot are you here?"
	msg := &tgbotapi.Message{
		Text: text,
		Entities: []tgbotapi.MessageEntity{
			{Type: "mention", Offset: 3, Length: 8},
		},
	}
	if !bot.isBotMentioned(msg) {
		t.Error("isBotMentioned: expected true for @mention after emoji, got false")
	}
}

func TestIsBotMentioned_NoMention(t *testing.T) {
	bot := newMinimalBot("testbot")
	msg := &tgbotapi.Message{
		Text:     "hello there, no mention at all",
		Entities: []tgbotapi.MessageEntity{},
	}
	if bot.isBotMentioned(msg) {
		t.Error("isBotMentioned: expected false for message without any mention entity")
	}
}

func TestIsBotMentioned_DifferentBot(t *testing.T) {
	bot := newMinimalBot("testbot")
	// @otherbot is at offset 0, length 9.
	text := "@otherbot hello"
	msg := &tgbotapi.Message{
		Text: text,
		Entities: []tgbotapi.MessageEntity{
			{Type: "mention", Offset: 0, Length: 9},
		},
	}
	if bot.isBotMentioned(msg) {
		t.Error("isBotMentioned: expected false for mention of a different bot")
	}
}

func TestIsBotMentioned_AsciiNoEmoji(t *testing.T) {
	bot := newMinimalBot("testbot")
	// Plain ASCII: UTF-16 offsets == rune indices == byte indices, so the baseline
	// case must also work correctly.
	text := "hey @testbot come play"
	msg := &tgbotapi.Message{
		Text: text,
		Entities: []tgbotapi.MessageEntity{
			{Type: "mention", Offset: 4, Length: 8},
		},
	}
	if !bot.isBotMentioned(msg) {
		t.Error("isBotMentioned: expected true for plain-ASCII @mention")
	}
}
