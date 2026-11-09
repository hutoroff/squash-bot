package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/discovery"
	"github.com/hutoroff/squash-bot/internal/observability"
	"github.com/prometheus/client_golang/prometheus"
)

func TestBookingDependencyCountsBeforeDispatchAndHTTPOnce(t *testing.T) {
	metrics := observability.NewDependencyMetrics()
	resolver := bookingResolverFunc(func(context.Context, discovery.Requirement) ([]discovery.ResolvedEndpoint, error) {
		return nil, discovery.ErrNoEndpoint
	})
	client := NewDiscoveredReliableHTTPBookingClient(resolver, discovery.Requirement{ServiceKey: "booking", Contract: "eversports-booking", Major: 2}, "test")
	observed := client.(interface {
		SetDependencyMetrics(*observability.DependencyMetrics)
	})
	observed.SetDependencyMetrics(metrics)
	if err := client.VerifyBinding(context.Background(), bookingapi.Scope{}); !errors.Is(err, discovery.ErrNoEndpoint) {
		t.Fatalf("predispatch error=%v", err)
	}
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits++; w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	static := newHTTPBookingClient(server.URL, "test")
	static.SetDependencyMetrics(metrics)
	if _, err := static.ListMatches(context.Background(), "2026-01-01", "", "", false, "", ""); err == nil {
		t.Fatal("HTTP 503 expected")
	}
	if hits != 1 {
		t.Fatalf("HTTP dispatches=%d", hits)
	}
	registry := prometheus.NewRegistry()
	for _, collector := range metrics.Collectors() {
		registry.MustRegister(collector)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	foundError, found503 := false, false
	for _, family := range families {
		if family.GetName() != observability.MetricDependencyRequestsTotal {
			continue
		}
		for _, metric := range family.Metric {
			if metric.GetCounter().GetValue() != 1 {
				t.Fatal("request double counted")
			}
			for _, label := range metric.Label {
				if label.GetName() == "outcome" {
					foundError = foundError || label.GetValue() == "resolution_error"
					found503 = found503 || label.GetValue() == "503"
				}
			}
		}
	}
	if !foundError || !found503 {
		t.Fatalf("pre-dispatch=%t HTTP 503=%t", foundError, found503)
	}
}
