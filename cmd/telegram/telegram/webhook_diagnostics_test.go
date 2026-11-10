package telegram

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestWebhookRequestLogRateLimitsRejectionsAndLogsFirstUpdateOnce(t *testing.T) {
	var buf bytes.Buffer
	requestLog := newWebhookRequestLog(slog.New(slog.NewTextHandler(&buf, nil)))
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	requestLog.now = func() time.Time { return now }
	ch := make(chan tgbotapi.Update, 10)
	handler := newWebhookHandler("correct-secret", nil, ch, requestLog)

	send := func(secret string) {
		req := httptest.NewRequest(http.MethodPost, "/telegram/webhook", strings.NewReader(`{"update_id":1}`))
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
		handler(httptest.NewRecorder(), req)
	}
	send("wrong")
	send("wrong")
	send("wrong")
	send("correct-secret")
	send("correct-secret")
	now = now.Add(2 * time.Minute)
	send("wrong")

	logs := buf.String()
	if got := strings.Count(logs, "webhook request rejected"); got != 2 {
		t.Fatalf("rejection log lines = %d, want 2:\n%s", got, logs)
	}
	if !strings.Contains(logs, "suppressed_since_last=2") {
		t.Fatalf("expected suppressed count in logs:\n%s", logs)
	}
	if got := strings.Count(logs, "webhook first update received"); got != 1 {
		t.Fatalf("first update log lines = %d, want 1:\n%s", got, logs)
	}
	if strings.Contains(logs, "/telegram/webhook") {
		t.Fatalf("logs must not contain the webhook path:\n%s", logs)
	}
}

func TestWebhookHealthReportsOnlyNewErrorsAndPersistentBacklog(t *testing.T) {
	const url = "https://bot.example/telegram/webhook"
	health := &webhookHealth{expectedURL: url, lastErrorDate: 100}

	if msg, _ := health.evaluate(tgbotapi.WebhookInfo{URL: url, LastErrorDate: 100, LastErrorMessage: "old"}); msg != "" {
		t.Fatalf("old error reported: %q", msg)
	}
	if msg, _ := health.evaluate(tgbotapi.WebhookInfo{URL: url, LastErrorDate: 200, LastErrorMessage: "Wrong response from the webhook: 502 Bad Gateway", PendingUpdateCount: 3}); !strings.Contains(msg, "delivery error") {
		t.Fatalf("new error message = %q", msg)
	}
	if msg, _ := health.evaluate(tgbotapi.WebhookInfo{URL: url, LastErrorDate: 200, PendingUpdateCount: 3}); msg != "" {
		t.Fatalf("first pending check reported: %q", msg)
	}
	if msg, _ := health.evaluate(tgbotapi.WebhookInfo{URL: url, LastErrorDate: 200, PendingUpdateCount: 3}); !strings.Contains(msg, "pending") {
		t.Fatalf("persistent backlog message = %q", msg)
	}
	if msg, _ := health.evaluate(tgbotapi.WebhookInfo{URL: url, LastErrorDate: 200, PendingUpdateCount: 3}); msg != "" {
		t.Fatalf("backlog reported repeatedly: %q", msg)
	}
	if msg, _ := health.evaluate(tgbotapi.WebhookInfo{URL: ""}); !strings.Contains(msg, "URL changed") {
		t.Fatalf("missing webhook message = %q", msg)
	}
	if msg, _ := health.evaluate(tgbotapi.WebhookInfo{URL: ""}); msg != "" {
		t.Fatalf("missing webhook reported repeatedly: %q", msg)
	}
}
