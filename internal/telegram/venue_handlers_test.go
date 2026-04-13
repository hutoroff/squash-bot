package telegram

// White-box unit tests for pure venue-handler helpers.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/vkhutorov/squash_bot/internal/i18n"
)

// ── test helpers ──────────────────────────────────────────────────────────────

// newFakeBotAPI returns a *tgbotapi.BotAPI backed by an httptest server that
// responds with a minimal valid Telegram API payload for any request.
func newFakeBotAPI(t *testing.T) *tgbotapi.BotAPI {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Combined payload: satisfies both the User decoder (getMe) and the
		// Message decoder (sendMessage, editMessageText, etc.).
		_, _ = io.WriteString(w, `{"ok":true,"result":{`+
			`"id":1,"is_bot":true,"first_name":"TestBot","username":"testbot",`+
			`"message_id":1,"date":1000000,"chat":{"id":42,"type":"private"}}}`)
	}))
	t.Cleanup(srv.Close)
	api, err := tgbotapi.NewBotAPIWithClient("faketoken", srv.URL+"/bot%s/%s", srv.Client())
	if err != nil {
		t.Fatalf("newFakeBotAPI: %v", err)
	}
	return api
}

func telegramNoopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ── renderAutoBookingEnabledKeyboard ──────────────────────────────────────────

func TestRenderAutoBookingEnabledKeyboard_HasTwoButtons(t *testing.T) {
	lz := i18n.New(i18n.En)
	kb := renderAutoBookingEnabledKeyboard(lz)
	if len(kb.InlineKeyboard) != 1 {
		t.Fatalf("expected 1 row, got %d", len(kb.InlineKeyboard))
	}
	if len(kb.InlineKeyboard[0]) != 2 {
		t.Fatalf("expected 2 buttons in row, got %d", len(kb.InlineKeyboard[0]))
	}
}

func TestRenderAutoBookingEnabledKeyboard_CallbackData(t *testing.T) {
	lz := i18n.New(i18n.En)
	kb := renderAutoBookingEnabledKeyboard(lz)
	row := kb.InlineKeyboard[0]
	if row[0].CallbackData == nil || *row[0].CallbackData != "venue_wiz_autobooking:enable" {
		t.Errorf("first button callback: got %v, want venue_wiz_autobooking:enable", row[0].CallbackData)
	}
	if row[1].CallbackData == nil || *row[1].CallbackData != "venue_wiz_autobooking:disable" {
		t.Errorf("second button callback: got %v, want venue_wiz_autobooking:disable", row[1].CallbackData)
	}
}

func TestRenderAutoBookingEnabledKeyboard_ButtonLabels_EN(t *testing.T) {
	lz := i18n.New(i18n.En)
	kb := renderAutoBookingEnabledKeyboard(lz)
	row := kb.InlineKeyboard[0]
	if row[0].Text != "✅ Enable" {
		t.Errorf("enable button EN: got %q, want %q", row[0].Text, "✅ Enable")
	}
	if row[1].Text != "❌ Disable" {
		t.Errorf("disable button EN: got %q, want %q", row[1].Text, "❌ Disable")
	}
}

func TestRenderAutoBookingEnabledKeyboard_ButtonLabels_DE(t *testing.T) {
	lz := i18n.New(i18n.De)
	kb := renderAutoBookingEnabledKeyboard(lz)
	row := kb.InlineKeyboard[0]
	if row[0].Text != "✅ Aktivieren" {
		t.Errorf("enable button DE: got %q, want %q", row[0].Text, "✅ Aktivieren")
	}
	if row[1].Text != "❌ Deaktivieren" {
		t.Errorf("disable button DE: got %q, want %q", row[1].Text, "❌ Deaktivieren")
	}
}

func TestRenderAutoBookingEnabledKeyboard_ButtonLabels_RU(t *testing.T) {
	lz := i18n.New(i18n.Ru)
	kb := renderAutoBookingEnabledKeyboard(lz)
	row := kb.InlineKeyboard[0]
	if row[0].Text != "✅ Включить" {
		t.Errorf("enable button RU: got %q, want %q", row[0].Text, "✅ Включить")
	}
	if row[1].Text != "❌ Отключить" {
		t.Errorf("disable button RU: got %q, want %q", row[1].Text, "❌ Отключить")
	}
}

// ── venueWizard struct defaults ───────────────────────────────────────────────

func TestVenueWizard_AutoBookingEnabled_DefaultsFalse(t *testing.T) {
	wiz := &venueWizard{}
	if wiz.autoBookingEnabled {
		t.Error("autoBookingEnabled should default to false")
	}
}

// ── wizard step ordering ──────────────────────────────────────────────────────

func TestVenueWizardStep_AutoBookingEnabled_IsAfterGracePeriod(t *testing.T) {
	if venueStepAutoBookingEnabled <= venueStepGracePeriod {
		t.Errorf("venueStepAutoBookingEnabled (%d) must come after venueStepGracePeriod (%d)",
			venueStepAutoBookingEnabled, venueStepGracePeriod)
	}
}

func TestVenueWizardStep_AutoBookingCourts_IsAfterAutoBookingEnabled(t *testing.T) {
	if venueStepAutoBookingCourts <= venueStepAutoBookingEnabled {
		t.Errorf("venueStepAutoBookingCourts (%d) must come after venueStepAutoBookingEnabled (%d)",
			venueStepAutoBookingCourts, venueStepAutoBookingEnabled)
	}
}

func TestVenueWizardStep_BookingOpensDays_IsAfterAutoBookingCourts(t *testing.T) {
	if venueStepBookingOpensDays <= venueStepAutoBookingCourts {
		t.Errorf("venueStepBookingOpensDays (%d) must come after venueStepAutoBookingCourts (%d)",
			venueStepBookingOpensDays, venueStepAutoBookingCourts)
	}
}

// ── processVenueWizard text input at venueStepAutoBookingEnabled ──────────────

// TestProcessVenueWizard_TextAtAutoBookingEnabledStep verifies that typing text
// (instead of clicking the keyboard button) at the venueStepAutoBookingEnabled
// step does not advance the wizard — the step must remain unchanged so the
// admin can still click the button and proceed.
func TestProcessVenueWizard_TextAtAutoBookingEnabledStep_StepUnchanged(t *testing.T) {
	api := newFakeBotAPI(t)
	b := &Bot{
		api:    api,
		logger: telegramNoopLogger(),
	}

	wiz := &venueWizard{step: venueStepAutoBookingEnabled, groupID: 1}
	msg := &tgbotapi.Message{
		Chat: &tgbotapi.Chat{ID: 42, Type: "private"},
		From: &tgbotapi.User{LanguageCode: "en"},
		Text: "yes please",
	}

	b.processVenueWizard(context.Background(), msg, wiz)

	if wiz.step != venueStepAutoBookingEnabled {
		t.Errorf("step advanced to %v, want venueStepAutoBookingEnabled", wiz.step)
	}
}
