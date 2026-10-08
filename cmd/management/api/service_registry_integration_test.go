//go:build integration

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/discovery"
	"github.com/hutoroff/squash-bot/internal/observability"
	"github.com/hutoroff/squash-bot/internal/testutil"
	"github.com/prometheus/client_golang/prometheus"
)

type faultRegistryRepository struct {
	service.ServiceRegistryRepository
	monitoring interface {
		MonitoringInstances(context.Context) ([]discovery.MonitoringInstance, error)
	}
	unavailable atomic.Bool
}

func (r *faultRegistryRepository) Resolve(ctx context.Context, req discovery.Requirement) ([]discovery.ResolvedEndpoint, error) {
	if r.unavailable.Load() {
		return nil, errors.New("synthetic registry storage failure")
	}
	return r.ServiceRegistryRepository.Resolve(ctx, req)
}
func (r *faultRegistryRepository) Catalog(ctx context.Context) ([]discovery.CatalogEntry, error) {
	if r.unavailable.Load() {
		return nil, errors.New("synthetic registry storage failure")
	}
	return r.ServiceRegistryRepository.Catalog(ctx)
}
func (r *faultRegistryRepository) MonitoringInstances(ctx context.Context) ([]discovery.MonitoringInstance, error) {
	if r.unavailable.Load() {
		return nil, errors.New("synthetic registry storage failure")
	}
	return r.monitoring.MonitoringInstances(ctx)
}

// Exercises real HTTP registry adapters and PostgreSQL membership, not a live
// service process or provider. Registry loss is injected before the DB adapter;
// no shared disposable pool is closed out from under other integration tests.
func TestRegistryHTTPAndPostgresReplacementLeaseAndScrape(t *testing.T) {
	ctx := context.Background()
	if err := testutil.Truncate(ctx, apiTestPool); err != nil {
		t.Fatal(err)
	}
	secret, monitoringSecret := strings.Repeat("r", 32), strings.Repeat("p", 32)
	store := storage.NewServiceRegistryRepo(apiTestPool)
	fault := &faultRegistryRepository{ServiceRegistryRepository: store, monitoring: store}
	registry, err := service.NewServiceRegistryService(fault, "10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServer(":0", &Handler{serviceRegistry: registry, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, secret, monitoringSecret).Handler)
	defer server.Close()
	registrar, err := discovery.NewHTTPRegistrar(server.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := discovery.NewRegistryResolver(server.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	first := discovery.Descriptor{SchemaVersion: discovery.SchemaVersion, InstanceID: "0195f8db-7a6f-7c7b-8f71-0f6e07120480", ServiceKey: "booking.eversports.integration", ServiceKind: "booking", Implementation: "eversports", ReleaseVersion: "2.0.0", Scope: map[string]string{"facility_uuid": "facility-integration", "sport": "squash"}, Endpoints: discovery.Endpoints{API: "http://10.24.0.1:8081", Admin: "http://10.24.0.1:9091"}, Contracts: []discovery.Contract{{Name: "eversports-booking", Major: 2}}, Capabilities: []discovery.Capability{{Name: "booking.create", Contract: "eversports-booking", Major: 2}}, State: discovery.StateStarting}
	req := discovery.Requirement{ServiceKey: first.ServiceKey, Contract: "eversports-booking", Major: 2, RequiredCapabilities: []string{"booking.create"}, Scope: first.Scope}
	if _, err := discovery.ResolveOne(ctx, resolver, req); !errors.Is(err, discovery.ErrNoEndpoint) {
		t.Fatalf("booking absent: %v", err)
	}
	// Same resolver and registrar survive consumer-first and dependency-first starts.
	if _, err := registrar.Register(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := registrar.Heartbeat(ctx, first.InstanceID, discovery.StateReady); err != nil {
		t.Fatal(err)
	}
	resolver.Invalidate(req)
	if got, err := discovery.ResolveOne(ctx, resolver, req); err != nil || got.InstanceID != first.InstanceID {
		t.Fatalf("late booking registration: %+v %v", got, err)
	}
	liveCount := func() float64 {
		t.Helper()
		metrics := prometheus.NewRegistry()
		metrics.MustRegister(observability.NewRegistryCollector([]discovery.MonitoredService{{ServiceKey: first.ServiceKey, Contract: req.Contract, Major: req.Major, Capabilities: req.RequiredCapabilities, Scope: req.Scope}}, registry.MonitoringInstances))
		families, err := metrics.Gather()
		if err != nil {
			t.Fatal(err)
		}
		for _, family := range families {
			if family.GetName() == observability.MetricServiceLiveInstances {
				return family.Metric[0].GetGauge().GetValue()
			}
		}
		t.Fatal("expected live count missing")
		return -1
	}
	if got := liveCount(); got != 1 {
		t.Fatalf("heartbeat-ready service has live count %v, want 1", got)
	}

	readTargets := func(bearer string) (int, []discovery.PrometheusTarget, string) {
		t.Helper()
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/internal/discovery/v1/prometheus-targets", nil)
		request.Header.Set("Authorization", "Bearer "+bearer)
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		var targets []discovery.PrometheusTarget
		if response.StatusCode == http.StatusOK && json.Unmarshal(body, &targets) != nil {
			t.Fatalf("invalid targets: %s", body)
		}
		return response.StatusCode, targets, string(body)
	}
	if code, _, _ := readTargets(secret); code != http.StatusUnauthorized {
		t.Fatalf("business bearer read SD: %d", code)
	}
	if code, targets, body := readTargets(monitoringSecret); code != http.StatusOK || len(targets) != 1 || targets[0].Targets[0] != "10.24.0.1:9091" || strings.Contains(body, "facility-integration") || strings.Contains(body, secret) {
		t.Fatalf("SD targets=%+v status=%d body=%s", targets, code, body)
	}
	if _, err := registrar.Heartbeat(ctx, first.InstanceID, discovery.StateNotReady); err != nil {
		t.Fatal(err)
	}
	resolver.Invalidate(req)
	if _, err := discovery.ResolveOne(ctx, resolver, req); !errors.Is(err, discovery.ErrNoEndpoint) {
		t.Fatalf("not-ready service was routed: %v", err)
	}
	if got := liveCount(); got != 0 {
		t.Fatalf("not-ready service live count=%v", got)
	}
	if code, targets, _ := readTargets(monitoringSecret); code != http.StatusOK || len(targets) != 1 || targets[0].Labels["state"] != string(discovery.StateNotReady) {
		t.Fatalf("not-ready instance should still be scraped: status=%d targets=%+v", code, targets)
	}
	if _, err := registrar.Heartbeat(ctx, first.InstanceID, discovery.StateReady); err != nil {
		t.Fatal(err)
	}
	resolver.Invalidate(req)
	if _, err := discovery.ResolveOne(ctx, resolver, req); err != nil || liveCount() != 1 {
		t.Fatalf("ready recovery without restart: %v", err)
	}
	var beforeLease, afterLease time.Time
	if err := apiTestPool.QueryRow(ctx, `SELECT lease_expires_at FROM service_instances WHERE instance_id = $1`, first.InstanceID).Scan(&beforeLease); err != nil {
		t.Fatal(err)
	}
	newRepo := storage.NewServiceRegistryRepo(apiTestPool)
	newRegistry, err := service.NewServiceRegistryService(newRepo, "10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	restarted := httptest.NewServer(NewServer(":0", &Handler{serviceRegistry: newRegistry, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, secret, monitoringSecret).Handler)
	defer restarted.Close()
	restartedResolver, err := discovery.NewRegistryResolver(restarted.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := discovery.ResolveOne(ctx, restartedResolver, req); err != nil || got.InstanceID != first.InstanceID {
		t.Fatalf("new registry handler did not reload existing lease: %+v %v", got, err)
	}
	if err := apiTestPool.QueryRow(ctx, `SELECT lease_expires_at FROM service_instances WHERE instance_id = $1`, first.InstanceID).Scan(&afterLease); err != nil {
		t.Fatal(err)
	}
	if !afterLease.Equal(beforeLease) {
		t.Fatalf("registry read extended lease: before=%s after=%s", beforeLease, afterLease)
	}
	multiple := first
	multiple.InstanceID = "0195f8db-7a6f-7c7b-8f71-0f6e07120482"
	multiple.Endpoints = discovery.Endpoints{API: "http://10.24.0.3:8081", Admin: "http://10.24.0.3:9091"}
	if _, err := registrar.Register(ctx, multiple); err != nil {
		t.Fatal(err)
	}
	if _, err := registrar.Heartbeat(ctx, multiple.InstanceID, discovery.StateReady); err != nil {
		t.Fatal(err)
	}
	resolver.Invalidate(req)
	if _, err := discovery.ResolveOne(ctx, resolver, req); !errors.Is(err, discovery.ErrMultipleActive) {
		t.Fatalf("singleton ambiguity: %v", err)
	}
	if got := liveCount(); got != 2 {
		t.Fatalf("duplicate ready instance live count=%v", got)
	}
	if err := registrar.Drain(ctx, multiple.InstanceID); err != nil {
		t.Fatal(err)
	}
	if err := registrar.Deregister(ctx, multiple.InstanceID); err != nil {
		t.Fatal(err)
	}

	second := first
	second.InstanceID = "0195f8db-7a6f-7c7b-8f71-0f6e07120481"
	second.Endpoints = discovery.Endpoints{API: "http://10.24.0.2:8081", Admin: "http://10.24.0.2:9091"}
	// Lease expiry removes the old target even though the catalog still holds
	// its historical incarnation; replacement requires a new instance ID.
	if _, err := apiTestPool.Exec(ctx, `UPDATE service_instances SET lease_expires_at = NOW() - interval '1 second' WHERE instance_id = $1`, first.InstanceID); err != nil {
		t.Fatal(err)
	}
	resolver.Invalidate(req)
	if _, err := discovery.ResolveOne(ctx, resolver, req); !errors.Is(err, discovery.ErrNoEndpoint) {
		t.Fatalf("expired lease: %v", err)
	}
	if code, targets, _ := readTargets(monitoringSecret); code != http.StatusOK || len(targets) != 0 {
		t.Fatalf("expired SD targets=%+v status=%d", targets, code)
	}
	if got := liveCount(); got != 0 {
		t.Fatalf("expired last instance live count=%v", got)
	}
	if _, err := registrar.Heartbeat(ctx, first.InstanceID, discovery.StateReady); err == nil {
		t.Fatal("expired process revived by heartbeat")
	}
	if _, err := registrar.Register(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, err := registrar.Heartbeat(ctx, second.InstanceID, discovery.StateReady); err != nil {
		t.Fatal(err)
	}
	resolver.Invalidate(req)
	if got, err := discovery.ResolveOne(ctx, resolver, req); err != nil || got.InstanceID != second.InstanceID || got.API != "http://10.24.0.2:8081" {
		t.Fatalf("different-IP replacement: %+v %v", got, err)
	}
	if code, targets, _ := readTargets(monitoringSecret); code != http.StatusOK || len(targets) != 1 || targets[0].Targets[0] != "10.24.0.2:9091" {
		t.Fatalf("replacement SD=%+v status=%d", targets, code)
	}
	if err := registrar.Drain(ctx, second.InstanceID); err != nil {
		t.Fatal(err)
	}
	resolver.Invalidate(req)
	if _, err := discovery.ResolveOne(ctx, resolver, req); !errors.Is(err, discovery.ErrNoEndpoint) {
		t.Fatalf("draining service: %v", err)
	}
	if code, targets, _ := readTargets(monitoringSecret); code != http.StatusOK || len(targets) != 0 {
		t.Fatalf("drained SD=%+v status=%d", targets, code)
	}
	if err := registrar.Deregister(ctx, second.InstanceID); err != nil {
		t.Fatal(err)
	}
	fault.unavailable.Store(true)
	fresh, err := discovery.NewRegistryResolver(server.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := discovery.ResolveOne(ctx, fresh, req); !errors.Is(err, discovery.ErrUnavailable) {
		t.Fatalf("DB failure misread as empty: %v", err)
	}
	if code, _, _ := readTargets(monitoringSecret); code != http.StatusServiceUnavailable {
		t.Fatalf("DB failure SD status=%d", code)
	}
}
