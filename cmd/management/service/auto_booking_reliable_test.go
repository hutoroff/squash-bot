package service

import (
	"context"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/i18n"
	"github.com/hutoroff/squash-bot/internal/models"
)

type countingNotifier struct{ runs int }

func (n *countingNotifier) RunOnce(context.Context) error {
	n.runs++
	return nil
}

func reliableSlotTexts(t *testing.T, api *mockTelegramAPI) []string {
	t.Helper()
	var texts []string
	for _, call := range api.sendCalls {
		msg, ok := call.(tgbotapi.MessageConfig)
		if !ok {
			t.Fatalf("expected MessageConfig, got %T", call)
		}
		texts = append(texts, msg.Text)
	}
	return texts
}

func TestFinishReliableSlotWithoutMatchIDsSendsOnlySuccessSummary(t *testing.T) {
	api := &mockTelegramAPI{admins: []tgbotapi.ChatMember{makeChatMember(101, false)}}
	notifier := &countingNotifier{}
	job := &AutoBookingJob{api: api, logger: noopLogger(), reliableBooking: &reliableBookingDeps{}, reliableNotifier: notifier}
	result := &BookFreeCourtsResult{
		BookedLabels: []string{"5", "6", "7", "8"},
		Failures: []BookingFailure{
			{Reason: "booking succeeded without a cancellable match ID"},
			{Reason: "booking succeeded without a cancellable match ID"},
			{Reason: "booking succeeded without a cancellable match ID"},
			{Reason: "booking succeeded without a cancellable match ID"},
		},
		Requested: 4,
	}

	booked := job.finishReliableSlot(context.Background(), -1001, &models.Venue{ID: 1, Name: "Venue"}, "2026-11-23", "20:45", 4, result, i18n.New(i18n.En))

	texts := reliableSlotTexts(t, api)
	if !booked || len(texts) != 1 || !strings.Contains(texts[0], "Auto-booked 4 courts") {
		t.Fatalf("booked = %v, DMs = %q; want one success DM", booked, texts)
	}
	if notifier.runs != 1 {
		t.Fatalf("operator notifier runs = %d, want 1", notifier.runs)
	}
}

func TestFinishReliableSlotShortfallSendsOneFailureSummary(t *testing.T) {
	api := &mockTelegramAPI{admins: []tgbotapi.ChatMember{makeChatMember(101, false)}}
	job := &AutoBookingJob{api: api, logger: noopLogger(), reliableBooking: &reliableBookingDeps{}}
	result := &BookFreeCourtsResult{
		BookedLabels: []string{"5"},
		Failures: []BookingFailure{
			{Reason: "booking outcome is unresolved"},
			{Reason: "booking was rejected with no provider effect"},
			{Reason: "credential unavailable for durable operation"},
		},
		Requested: 4,
	}

	job.finishReliableSlot(context.Background(), -1001, &models.Venue{ID: 1, Name: "Venue"}, "2026-11-23", "20:45", 4, result, i18n.New(i18n.En))

	texts := reliableSlotTexts(t, api)
	if len(texts) != 2 || !strings.Contains(texts[0], "Auto-booked 1 courts") || !strings.Contains(texts[1], "booked 1 of 4 courts") {
		t.Fatalf("DMs = %q; want one success and one shortfall DM", texts)
	}
}
