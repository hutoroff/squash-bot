package i18n_test

import (
	"strings"
	"testing"

	"github.com/vkhutorov/squash_bot/internal/i18n"
)

// TestSchedReminderEvenNoCancel_Wording is a regression test for the bug where an even
// player count under capacity produced an "all good" (✅) notification instead of a
// warning asking the group to cancel unused courts.
// It verifies all three languages use warning wording and include a call-to-cancel.
func TestSchedReminderEvenNoCancel_Wording(t *testing.T) {
	// Required: each language must contain a word prompting manual court cancellation.
	callToCancel := map[i18n.Lang]string{
		i18n.En: "cancel",
		i18n.De: "stornieren",
		i18n.Ru: "отмените",
	}

	for _, lang := range []i18n.Lang{i18n.En, i18n.De, i18n.Ru} {
		lz := i18n.New(lang)
		// 2 players, 6-person capacity (3 courts) — even count, under capacity, nothing canceled.
		msg := lz.Tf(i18n.SchedReminderEvenNoCancel, "15.04 18:00", 2, 6, 3)

		// Must be a warning, not a success confirmation.
		if strings.Contains(msg, "✅") {
			t.Errorf("[%s] even_no_cancel message must not contain ✅ (all-good emoji): %s", lang, msg)
		}
		// Must prompt manual cancellation.
		word := callToCancel[lang]
		if !strings.Contains(strings.ToLower(msg), word) {
			t.Errorf("[%s] message missing cancel call-to-action %q: %s", lang, word, msg)
		}
		// Must contain player count and capacity so the group sees the problem.
		if !strings.Contains(msg, "2") || !strings.Contains(msg, "6") {
			t.Errorf("[%s] message missing player count 2/6: %s", lang, msg)
		}
		// Must contain court count.
		if !strings.Contains(msg, "3") {
			t.Errorf("[%s] message missing court count 3: %s", lang, msg)
		}
		// Must contain the game date.
		if !strings.Contains(msg, "15.04 18:00") {
			t.Errorf("[%s] message missing game date: %s", lang, msg)
		}
	}
}

// TestBookingReminderMessage_Wording is a regression test for the incorrect booking
// reminder message that said "booking opens in N days" when in fact the reminder
// fires on the day booking opens and the game is N days away.
// It verifies all three languages use the corrected phrasing.
func TestBookingReminderMessage_Wording(t *testing.T) {
	banned := map[i18n.Lang][]string{
		i18n.En: {"opens in", "opens in %d"},
		i18n.De: {"öffnet in", "öffnet in %d"},
		i18n.Ru: {"откроется через"},
	}
	required := map[i18n.Lang][]string{
		i18n.En: {"days", "now"},
		i18n.De: {"Tagen", "jetzt"},
		i18n.Ru: {"дней", "сейчас"},
	}

	for _, lang := range []i18n.Lang{i18n.En, i18n.De, i18n.Ru} {
		lz := i18n.New(lang)
		msg := lz.Tf(i18n.SchedBookingReminder, "Test Venue", 14)

		for _, bad := range banned[lang] {
			if strings.Contains(msg, bad) {
				t.Errorf("[%s] message still contains old wording %q: %s", lang, bad, msg)
			}
		}
		for _, want := range required[lang] {
			if !strings.Contains(msg, want) {
				t.Errorf("[%s] message missing expected word %q: %s", lang, want, msg)
			}
		}
		if !strings.Contains(msg, "14") {
			t.Errorf("[%s] message missing day count: %s", lang, msg)
		}
		if !strings.Contains(msg, "Test Venue") {
			t.Errorf("[%s] message missing venue name: %s", lang, msg)
		}
	}
}
