package observability

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/hutoroff/squash-bot/internal/discovery"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Readiness struct {
	Ready  bool            `json:"ready"`
	Checks map[string]bool `json:"checks,omitempty"`
}

type AdminConfig struct {
	Addr       string
	Token      string
	Version    string
	Descriptor discovery.Descriptor
	Readiness  func(context.Context) Readiness
	Collectors []prometheus.Collector
}

func NewAdminServer(cfg AdminConfig) (*http.Server, error) {
	if cfg.Addr == "" || len(cfg.Token) < 32 || cfg.Readiness == nil {
		return nil, fmt.Errorf("admin address, monitoring token, and readiness source are required")
	}
	if err := cfg.Descriptor.Validate(); err != nil {
		return nil, fmt.Errorf("invalid admin service descriptor: %w", err)
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: MetricBuildInfo, Help: "Static service build identity.",
		ConstLabels: prometheus.Labels{"service_key": cfg.Descriptor.ServiceKey, "service_kind": cfg.Descriptor.ServiceKind, "version": cfg.Version},
	}, func() float64 { return 1 }))
	registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: MetricServiceReady, Help: "Whether locally owned service readiness gates pass.",
		ConstLabels: prometheus.Labels{"service_key": cfg.Descriptor.ServiceKey, "service_kind": cfg.Descriptor.ServiceKind},
	}, func() float64 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if cfg.Readiness(ctx).Ready {
			return 1
		}
		return 0
	}))
	for _, collector := range cfg.Collectors {
		if collector == nil {
			return nil, fmt.Errorf("admin collector cannot be nil")
		}
		if err := registry.Register(collector); err != nil {
			return nil, fmt.Errorf("register admin collector: %w", err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"live": true})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		readyCtx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		readiness := cfg.Readiness(readyCtx)
		status := http.StatusOK
		if !readiness.Ready {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, readiness)
	})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"version": cfg.Version})
	})
	mux.HandleFunc("GET /descriptor", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, cfg.Descriptor)
	})
	mux.Handle("GET /metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))

	return &http.Server{
		Addr: cfg.Addr, Handler: requireBearer(cfg.Token, mux), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
	}, nil
}

func Run(ctx context.Context, server *http.Server, logger *slog.Logger) error {
	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

func requireBearer(token string, next http.Handler) http.Handler {
	expected := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), expected) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("write admin response", "err", err)
	}
}
