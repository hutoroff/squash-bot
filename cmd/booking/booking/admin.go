package booking

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/hutoroff/squash-bot/cmd/booking/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/discovery"
	"github.com/hutoroff/squash-bot/internal/observability"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Readiness struct {
	Ready       bool `json:"ready"`
	Database    bool `json:"database"`
	Singleton   bool `json:"singleton"`
	Coordinator bool `json:"coordinator"`
}

type AdminConfig struct {
	Addr          string
	Secret        string
	Version       string
	ServiceKey    string
	InstanceID    string
	APIEndpoint   string
	AdminEndpoint string
	Scope         bookingapi.Scope
	Readiness     func(context.Context) Readiness
	Running       func() int
	LedgerHealth  func(context.Context) (storage.LedgerHealth, error)
	Metrics       *Metrics
}

type Metrics struct {
	httpRequests       *prometheus.CounterVec
	httpDuration       *prometheus.HistogramVec
	externalExecutions *prometheus.CounterVec
	externalDuration   *prometheus.HistogramVec
	operationOutcomes  *prometheus.CounterVec
}

func NewAdminServer(cfg AdminConfig) (*http.Server, *Metrics, error) {
	if cfg.Addr == "" || cfg.Secret == "" || cfg.Readiness == nil || cfg.Running == nil || cfg.LedgerHealth == nil {
		return nil, nil, fmt.Errorf("booking admin address, secret, and health sources are required")
	}
	if _, err := uuid.Parse(cfg.InstanceID); err != nil {
		return nil, nil, fmt.Errorf("booking admin instance ID must be a UUID: %w", err)
	}
	descriptor := discovery.Descriptor{
		SchemaVersion: discovery.SchemaVersion, InstanceID: cfg.InstanceID, ServiceKey: cfg.ServiceKey,
		ServiceKind: "booking", Implementation: cfg.Scope.Provider, ReleaseVersion: cfg.Version,
		Scope:     map[string]string{"facility_id": cfg.Scope.FacilityID, "facility_uuid": cfg.Scope.FacilityUUID, "facility_slug": cfg.Scope.FacilitySlug, "sport": cfg.Scope.Sport},
		Endpoints: discovery.Endpoints{API: cfg.APIEndpoint, Admin: cfg.AdminEndpoint},
		Contracts: []discovery.Contract{{Name: bookingapi.ContractName, Major: bookingapi.ContractMajor, Minor: 0}},
		Capabilities: []discovery.Capability{
			{Name: "booking.courts.list", Contract: bookingapi.ContractName, Major: bookingapi.ContractMajor},
			{Name: "booking.slots.list", Contract: bookingapi.ContractName, Major: bookingapi.ContractMajor},
			{Name: "booking.create", Contract: bookingapi.ContractName, Major: bookingapi.ContractMajor},
			{Name: "booking.cancel", Contract: bookingapi.ContractName, Major: bookingapi.ContractMajor},
			{Name: "booking.operations.get", Contract: bookingapi.ContractName, Major: bookingapi.ContractMajor},
			{Name: "booking.operations.reconcile", Contract: bookingapi.ContractName, Major: bookingapi.ContractMajor},
			{Name: "booking.operations.resolve", Contract: bookingapi.ContractName, Major: bookingapi.ContractMajor},
			{Name: "booking.import", Contract: bookingapi.ContractName, Major: bookingapi.ContractMajor},
		},
		State: discovery.StateReady,
	}
	if err := descriptor.Validate(); err != nil {
		return nil, nil, fmt.Errorf("invalid booking admin descriptor: %w", err)
	}
	metrics := cfg.Metrics
	if metrics == nil {
		metrics = NewMetrics()
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	registry.MustRegister(metrics.httpRequests, metrics.httpDuration, metrics.externalExecutions, metrics.externalDuration, metrics.operationOutcomes)
	registry.MustRegister(
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: observability.MetricBuildInfo, Help: "Static service build identity.", ConstLabels: prometheus.Labels{"service_key": cfg.ServiceKey, "service_kind": "booking", "version": cfg.Version}}, func() float64 { return 1 }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: observability.MetricServiceReady, Help: "Whether locally owned service readiness gates pass.", ConstLabels: prometheus.Labels{"service_key": cfg.ServiceKey, "service_kind": "booking"}}, func() float64 {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if cfg.Readiness(ctx).Ready {
				return 1
			}
			return 0
		}),
	)
	registry.MustRegister(
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "booking_ready", Help: "Whether all booking readiness gates pass."}, func() float64 {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if cfg.Readiness(ctx).Ready {
				return 1
			}
			return 0
		}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "booking_database_healthy", Help: "Whether the booking database readiness check passes."}, func() float64 {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if cfg.Readiness(ctx).Database {
				return 1
			}
			return 0
		}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "booking_singleton_healthy", Help: "Whether this process still owns singleton authority."}, func() float64 {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if cfg.Readiness(ctx).Singleton {
				return 1
			}
			return 0
		}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "booking_admission_open", Help: "Whether the coordinator is accepting operation admission."}, func() float64 {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if cfg.Readiness(ctx).Coordinator {
				return 1
			}
			return 0
		}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "booking_operations_running", Help: "Current externally executing booking operations."}, func() float64 {
			return float64(cfg.Running())
		}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "booking_operations_unknown", Help: "Durable operations with unknown provider outcome."}, func() float64 {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			health, err := cfg.LedgerHealth(ctx)
			if err != nil {
				return 0
			}
			return float64(health.UnknownOperations)
		}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "booking_risk_replacements", Help: "Durable create operations admitted with explicit duplicate-risk replacement authorization."}, func() float64 {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			health, err := cfg.LedgerHealth(ctx)
			if err != nil {
				return 0
			}
			return float64(health.RiskReplacements)
		}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "booking_oldest_unknown_age_seconds", Help: "Age of the oldest unresolved operation, or zero when none exists."}, func() float64 {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			health, err := cfg.LedgerHealth(ctx)
			if err != nil || health.OldestUnknownAt == nil {
				return 0
			}
			age := time.Since(*health.OldestUnknownAt).Seconds()
			if age < 0 {
				return 0
			}
			return age
		}),
	)

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
		writeJSON(w, http.StatusOK, descriptor)
	})
	mux.Handle("GET /metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))

	return &http.Server{
		Addr: cfg.Addr, Handler: requireAdminBearer(cfg.Secret, mux),
		ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
	}, metrics, nil
}

func NewMetrics() *Metrics {
	return &Metrics{
		httpRequests:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "booking_http_requests_total", Help: "Business API requests by bounded route and status."}, []string{"method", "route", "status"}),
		httpDuration:       prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "booking_http_request_duration_seconds", Help: "Business API request latency.", Buckets: prometheus.DefBuckets}, []string{"method", "route"}),
		externalExecutions: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "booking_external_executions_total", Help: "Provider execution flows by operation kind and sanitized outcome."}, []string{"kind", "outcome"}),
		externalDuration:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "booking_external_execution_duration_seconds", Help: "Provider execution flow latency.", Buckets: []float64{0.25, 0.5, 1, 2, 5, 10, 20, 45, 90}}, []string{"kind"}),
		operationOutcomes:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "booking_operation_outcomes_total", Help: "Durably finalized operation outcomes."}, []string{"kind", "state", "outcome"}),
	}
}

func (m *Metrics) ObserveHTTPRequest(method, route, status string, duration time.Duration) {
	m.httpRequests.WithLabelValues(method, route, status).Inc()
	m.httpDuration.WithLabelValues(method, route).Observe(duration.Seconds())
}

func (m *Metrics) ObserveExternalExecution(kind bookingapi.OperationKind, outcome bookingapi.OutcomeCategory, duration time.Duration) {
	m.externalExecutions.WithLabelValues(string(kind), string(outcome)).Inc()
	m.externalDuration.WithLabelValues(string(kind)).Observe(duration.Seconds())
}

func (m *Metrics) ObserveOperationOutcome(kind bookingapi.OperationKind, state bookingapi.OperationState, outcome bookingapi.OutcomeCategory) {
	m.operationOutcomes.WithLabelValues(string(kind), string(state), string(outcome)).Inc()
}

func requireAdminBearer(secret string, next http.Handler) http.Handler {
	expected := []byte("Bearer " + secret)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), expected) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}
