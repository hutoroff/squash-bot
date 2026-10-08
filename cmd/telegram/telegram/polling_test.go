package telegram

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// This is an in-process fake Telegram Bot API, not a bot start. It pins the
// transport-specific readiness boundary and subscription without deleting an
// actual webhook or receiving updates from Telegram.
func TestPollingFakeTelegramAPIWaitsForWebhookDeletionBeforeReady(t *testing.T) {
	for _, deletionSucceeds := range []bool{false, true} {
		name := "failed deletion"
		if deletionSucceeds {
			name = "successful deletion"
		}
		t.Run(name, func(t *testing.T) {
			var seenUpdates atomic.Int32
			started := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasPrefix(r.URL.Path, "/botfake-token/") {
					t.Errorf("unexpected test endpoint: %q", r.URL.Path)
				}
				switch strings.TrimPrefix(r.URL.Path, "/botfake-token/") {
				case "getMe":
					_, _ = w.Write([]byte(`{"ok":true,"result":{"id":42,"is_bot":true,"first_name":"fake","username":"fake"}}`))
				case "deleteWebhook":
					if !deletionSucceeds {
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"synthetic deletion failure"}`))
						return
					}
					_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
				case "getUpdates":
					seenUpdates.Add(1)
					if err := r.ParseForm(); err != nil {
						t.Errorf("parse polling request: %v", err)
					}
					var subscribed []string
					if err := json.Unmarshal([]byte(r.Form.Get("allowed_updates")), &subscribed); err != nil {
						t.Errorf("decode allowed_updates: %v", err)
					}
					if strings.Join(subscribed, ",") != strings.Join(allowedUpdates, ",") {
						t.Errorf("polling allowed updates=%v want %v", subscribed, allowedUpdates)
					}
					once.Do(func() { close(started) })
					<-release
					_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
				default:
					t.Errorf("unexpected fake Telegram method %q", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			api, err := tgbotapi.NewBotAPIWithClient("fake-token", server.URL+"/bot%s/%s", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			bot := &Bot{api: api, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), handlerSem: make(chan struct{}, 1)}
			ctx, cancel := context.WithCancel(context.Background())
			var ready atomic.Int32
			done := make(chan struct{})
			go func() { bot.StartWithReady(ctx, func() { ready.Add(1) }); close(done) }()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				cancel()
				close(release)
				t.Fatal("fake polling transport did not start")
			}
			want := int32(0)
			if deletionSucceeds {
				want = 1
			}
			if got := ready.Load(); got != want {
				t.Errorf("ready callbacks=%d want=%d", got, want)
			}
			cancel()
			close(release)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("polling did not stop after cancellation")
			}
			if seenUpdates.Load() == 0 {
				t.Fatal("fake polling did not request updates")
			}
		})
	}
}
