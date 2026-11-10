package telegram

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// webhookRequestLog makes webhook delivery visible without per-update noise:
// the first accepted update after start is logged once, and rejected requests
// are logged at most once per reason per interval.
type webhookRequestLog struct {
	logger      *slog.Logger
	interval    time.Duration
	now         func() time.Time
	firstUpdate atomic.Bool

	mu         sync.Mutex
	lastLogged map[string]time.Time
	suppressed map[string]int
}

func newWebhookRequestLog(logger *slog.Logger) *webhookRequestLog {
	return &webhookRequestLog{
		logger: logger, interval: time.Minute, now: time.Now,
		lastLogged: make(map[string]time.Time), suppressed: make(map[string]int),
	}
}

func (l *webhookRequestLog) accepted() {
	if l != nil && l.firstUpdate.CompareAndSwap(false, true) {
		l.logger.Info("webhook first update received")
	}
}

// rejected never logs the request path: the public webhook path may carry a
// secret and unknown paths are untrusted input.
func (l *webhookRequestLog) rejected(reason string, status int) {
	if l == nil {
		return
	}
	l.mu.Lock()
	now := l.now()
	if last, ok := l.lastLogged[reason]; ok && now.Sub(last) < l.interval {
		l.suppressed[reason]++
		l.mu.Unlock()
		return
	}
	suppressed := l.suppressed[reason]
	l.lastLogged[reason] = now
	l.suppressed[reason] = 0
	l.mu.Unlock()
	l.logger.Warn("webhook request rejected", "reason", reason, "status", status, "suppressed_since_last", suppressed)
}

// webhookHealth tracks getWebhookInfo results between checks so only new
// delivery errors and persistent backlogs are reported.
type webhookHealth struct {
	expectedURL   string
	lastErrorDate int
	pendingChecks int
	urlMismatch   bool
}

// evaluate returns a warning message and attributes, or "" when healthy.
func (h *webhookHealth) evaluate(info tgbotapi.WebhookInfo) (string, []any) {
	if info.URL != h.expectedURL {
		h.pendingChecks = 0
		if h.urlMismatch {
			return "", nil
		}
		h.urlMismatch = true
		return "telegram webhook URL changed; updates are not delivered to this instance", []any{"registered_url_set", info.URL != ""}
	}
	h.urlMismatch = false
	if info.LastErrorDate > h.lastErrorDate {
		h.lastErrorDate = info.LastErrorDate
		return "telegram reports webhook delivery error", []any{
			"last_error", info.LastErrorMessage, "last_error_at", time.Unix(int64(info.LastErrorDate), 0).UTC(),
			"pending_updates", info.PendingUpdateCount,
		}
	}
	if info.PendingUpdateCount == 0 {
		h.pendingChecks = 0
		return "", nil
	}
	h.pendingChecks++
	if h.pendingChecks == 2 {
		return "telegram webhook updates are pending", []any{"pending_updates", info.PendingUpdateCount}
	}
	return "", nil
}

// monitorWebhookHealth polls getWebhookInfo until ctx is done. The error
// baseline is taken from registration so an old error is not re-reported.
func (b *Bot) monitorWebhookHealth(ctx context.Context, health *webhookHealth, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		info, err := b.api.GetWebhookInfo()
		if err != nil {
			b.logger.Warn("telegram webhook health check failed", "err", err)
			continue
		}
		if msg, attrs := health.evaluate(info); msg != "" {
			b.logger.Warn(msg, attrs...)
		}
	}
}
