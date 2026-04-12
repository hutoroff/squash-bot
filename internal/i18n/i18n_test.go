package i18n_test

import (
	"strings"
	"testing"

	"github.com/vkhutorov/squash_bot/internal/i18n"
)

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
