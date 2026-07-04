package telegram

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// WebhookOptions configures the webhook transport.
type WebhookOptions struct {
	// PublicURL is the full public HTTPS URL Telegram should POST updates to.
	// Must use https scheme; the path is derived from the URL.
	PublicURL string
	// ListenPort is the local plain-HTTP port to bind (e.g. "8083").
	// A TLS-terminating reverse proxy should forward to this port.
	ListenPort string
	// Secret is sent as X-Telegram-Bot-Api-Secret-Token and validated per request.
	// Leave empty to skip validation (not recommended in production).
	Secret string
}

// StartWebhook registers the Telegram webhook and runs the HTTP listener until ctx
// is cancelled. Updates are fed into the shared runUpdateLoop so all handlers run
// identically regardless of transport.
//
// Returns a non-nil error if webhook registration or listener setup fails, so the
// caller can fall back to long-polling.
func (b *Bot) StartWebhook(ctx context.Context, opts WebhookOptions) error {
	parsed, err := url.Parse(opts.PublicURL)
	if err != nil {
		return fmt.Errorf("parse webhook URL: %w", err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("webhook URL must use https scheme, got %q", parsed.Scheme)
	}

	// Bind the port before touching Telegram so any failure degrades to polling
	// without leaving a registered but unserviceable webhook.
	ln, err := net.Listen("tcp", ":"+opts.ListenPort)
	if err != nil {
		return fmt.Errorf("bind webhook port %s: %w", opts.ListenPort, err)
	}

	if err := b.registerWebhook(opts.PublicURL, opts.Secret); err != nil {
		ln.Close()
		return fmt.Errorf("register webhook: %w", err)
	}

	// Channel sized to absorb a burst while processUpdate goroutines spin up.
	ch := make(chan tgbotapi.Update, 100)

	mux := http.NewServeMux()
	mux.HandleFunc("POST "+parsed.Path, webhookHandler(opts.Secret, ch))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	})
	srv := &http.Server{
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		b.logger.Info("webhook listener started", "port", opts.ListenPort, "path", parsed.Path)
		if serveErr := srv.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			b.logger.Error("webhook server error", "err", serveErr)
		}
	}()

	b.runUpdateLoop(ctx, ch)

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b.logger.Info("webhook server shutting down")
	if shutErr := srv.Shutdown(shutCtx); shutErr != nil {
		b.logger.Warn("webhook server shutdown", "err", shutErr)
	}
	close(ch)
	return nil
}

// registerWebhook calls setWebhook manually via MakeRequest to support the
// secret_token parameter, which is absent from the typed tgbotapi.WebhookConfig
// in v5.5.1.
func (b *Bot) registerWebhook(publicURL, secret string) error {
	params := tgbotapi.Params{
		"url":                  publicURL,
		"drop_pending_updates": "false",
	}
	allowedJSON, err := json.Marshal(allowedUpdates)
	if err == nil {
		params["allowed_updates"] = string(allowedJSON)
	}
	if secret != "" {
		params["secret_token"] = secret
	}

	resp, err := b.api.MakeRequest("setWebhook", params)
	if err != nil {
		return fmt.Errorf("setWebhook API call: %w", err)
	}
	if !resp.Ok {
		return fmt.Errorf("setWebhook returned ok=false: %s", resp.Description)
	}

	info, err := b.api.GetWebhookInfo()
	if err != nil {
		return fmt.Errorf("getWebhookInfo: %w", err)
	}
	if !info.IsSet() {
		return fmt.Errorf("webhook not set after registration (getWebhookInfo reports unset)")
	}
	if info.LastErrorMessage != "" {
		slog.Warn("webhook registered but has a previous error", "last_error", info.LastErrorMessage)
	}
	b.logger.Info("webhook registered", "url", publicURL)
	return nil
}

// maxWebhookBodyBytes bounds the Update body to guard against memory/CPU
// exhaustion from oversized payloads. 1 MiB is far larger than any real
// Telegram Update.
const maxWebhookBodyBytes = 1 << 20

// webhookHandler returns an http.HandlerFunc that validates the Telegram secret
// header, decodes the Update body, and enqueues it on ch.
// Factored out of StartWebhook so it can be tested without a live BotAPI.
func webhookHandler(secret string, ch chan<- tgbotapi.Update) http.HandlerFunc {
	secretBytes := []byte(secret)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if secret != "" {
			got := []byte(r.Header.Get("X-Telegram-Bot-Api-Secret-Token"))
			if subtle.ConstantTimeCompare(got, secretBytes) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxWebhookBodyBytes)
		var update tgbotapi.Update
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		select {
		case ch <- update:
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	}
}
