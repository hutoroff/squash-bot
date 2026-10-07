package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/hutoroff/squash-bot/internal/observability"
	"github.com/prometheus/client_golang/prometheus"
)

func TestUpdateOutcomesAndWebhookMetricsAreBounded(t *testing.T) {
	updates := observability.NewUpdateMetrics()
	bot := &Bot{logger: telegramNoopLogger(), updateMetrics: updates}
	bot.processUpdate(context.Background(), tgbotapi.Update{UpdateID: 1})                                                                     // not handled
	bot.processUpdate(context.Background(), tgbotapi.Update{CallbackQuery: &tgbotapi.CallbackQuery{ID: "panic"}})                             // nil API; recovered
	bot.processUpdate(context.Background(), tgbotapi.Update{MyChatMember: &tgbotapi.ChatMemberUpdated{Chat: tgbotapi.Chat{Type: "private"}}}) // handled no-op

	httpMetrics := observability.NewHTTPMetrics()
	ch := make(chan tgbotapi.Update, 1)
	handler := httpMetrics.WrapNamed("POST /telegram/webhook", webhookHandler("secret", func() bool { return false }, ch))
	req := httptest.NewRequest(http.MethodPost, "/private-webhook-path?token=secret", nil)
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable || len(ch) != 0 {
		t.Fatalf("webhook status=%d queued=%d", rr.Code, len(ch))
	}
	reg := prometheus.NewRegistry()
	for _, c := range append(updates.Collectors(), httpMetrics.Collectors()...) {
		reg.MustRegister(c)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	foundPanic, foundIgnored, foundWebhook, foundProcessed := false, false, false, false
	for _, family := range families {
		for _, metric := range family.Metric {
			labels := map[string]string{}
			for _, l := range metric.Label {
				labels[l.GetName()] = l.GetValue()
				if l.GetValue() == "/private-webhook-path" || l.GetValue() == "secret" {
					t.Fatal("secret path leaked into metric labels")
				}
			}
			if family.GetName() == observability.MetricTelegramUpdatesTotal {
				if labels["kind"] == "callback" && labels["outcome"] == "panic" {
					foundPanic = true
				}
				if labels["kind"] == "other" && labels["outcome"] == "ignored" {
					foundIgnored = true
				}
				if labels["kind"] == "membership" && labels["outcome"] == "processed" {
					foundProcessed = true
				}
			}
			if family.GetName() == observability.MetricHTTPRequestsTotal && labels["route"] == "POST /telegram/webhook" && labels["status"] == "503" {
				foundWebhook = true
			}
		}
	}
	if !foundPanic || !foundIgnored || !foundWebhook || !foundProcessed {
		t.Fatalf("panic=%t ignored=%t webhook=%t processed=%t", foundPanic, foundIgnored, foundWebhook, foundProcessed)
	}
}
