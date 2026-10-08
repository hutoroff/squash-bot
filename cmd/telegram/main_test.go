package main

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/hutoroff/squash-bot/cmd/telegram/telegram"
	"github.com/hutoroff/squash-bot/internal/config"
	"github.com/hutoroff/squash-bot/internal/observability"
)

type syntheticTransport struct {
	webhookErr                 error
	webhookReady               bool
	pollingReady               bool
	webhookCalls, pollingCalls int
	options                    telegram.WebhookOptions
}

func (s *syntheticTransport) StartWebhook(_ context.Context, opts telegram.WebhookOptions) error {
	s.webhookCalls++
	s.options = opts
	if s.webhookReady && opts.OnReady != nil {
		opts.OnReady()
	}
	return s.webhookErr
}
func (s *syntheticTransport) StartWithReady(_ context.Context, ready func()) {
	s.pollingCalls++
	if s.pollingReady {
		ready()
	}
}

func TestTransportFallbackRechecksReadinessAndPreservesDependencyAdmission(t *testing.T) {
	for _, registry := range []bool{false, true} {
		name := "static"
		if registry {
			name = "registry"
		}
		t.Run(name, func(t *testing.T) {
			var managementAvailable atomic.Bool
			transport := &syntheticTransport{webhookErr: errors.New("synthetic listener failure after binding"), webhookReady: true, pollingReady: false}
			cfg := config.TelegramConfig{WebhookURL: "https://synthetic.test/hook", WebhookSecret: "synthetic-secret", ServerPort: "8083"}
			if registry {
				cfg.DiscoveryMode = config.DiscoveryModeRegistry
			}
			states := []bool{}
			metrics := observability.NewHTTPMetrics()
			runTelegramTransport(context.Background(), transport, cfg, metrics, managementAvailable.Load, func(ready bool) { states = append(states, ready) })
			if transport.webhookCalls != 1 || transport.pollingCalls != 1 || !reflect.DeepEqual(states, []bool{true, false}) {
				t.Fatalf("webhook fallback readiness=%v calls=%d/%d", states, transport.webhookCalls, transport.pollingCalls)
			}
			if transport.options.HTTPMetrics != metrics || transport.options.PublicURL != cfg.WebhookURL || transport.options.ListenPort != cfg.ServerPort || transport.options.Secret != cfg.WebhookSecret {
				t.Fatal("webhook settings lost during transport wiring")
			}
			if registry {
				if transport.options.AdmitUpdate == nil || transport.options.AdmitUpdate() {
					t.Fatal("unavailable management accepted a webhook")
				}
				managementAvailable.Store(true)
				if !transport.options.AdmitUpdate() {
					t.Fatal("webhook gate did not recover without restart")
				}
			} else if transport.options.AdmitUpdate != nil {
				t.Fatal("static mode acquired a registry admission gate")
			}
		})
	}
}

func TestTransportPollingOnlyAndCancelledWebhookDoNotRegisterUnexpectedTransport(t *testing.T) {
	polling := &syntheticTransport{pollingReady: true}
	var ready []bool
	runTelegramTransport(context.Background(), polling, config.TelegramConfig{}, nil, nil, func(value bool) { ready = append(ready, value) })
	if polling.webhookCalls != 0 || polling.pollingCalls != 1 || !reflect.DeepEqual(ready, []bool{true}) {
		t.Fatalf("polling only ready=%v webhook=%d poll=%d", ready, polling.webhookCalls, polling.pollingCalls)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	shuttingDown := &syntheticTransport{webhookErr: errors.New("synthetic shutdown")}
	runTelegramTransport(ctx, shuttingDown, config.TelegramConfig{WebhookURL: "https://synthetic.test/hook"}, nil, nil, func(bool) {})
	if shuttingDown.pollingCalls != 0 {
		t.Fatal("canceled webhook fell back to polling")
	}
}
