package observability

import (
	"context"
	"errors"
	"testing"

	"github.com/hutoroff/squash-bot/internal/discovery"
	"github.com/prometheus/client_golang/prometheus"
)

func TestRegistryCollectorExpectedZeroAndEligibility(t *testing.T) {
	expected := []discovery.MonitoredService{{ServiceKey: "booking", Contract: "booking", Major: 1, Capabilities: []string{"create"}}}
	c := NewRegistryCollector(expected, func(context.Context) ([]discovery.MonitoringInstance, error) {
		return []discovery.MonitoringInstance{{Descriptor: discovery.Descriptor{ServiceKey: "booking", Contracts: []discovery.Contract{{Name: "booking", Major: 1}}, Capabilities: []discovery.Capability{{Name: "create", Contract: "booking", Major: 1}}}, State: discovery.StateReady}}, nil
	})
	r := prometheus.NewRegistry()
	r.MustRegister(c)
	m, err := r.Gather()
	if err != nil || len(m) != 3 {
		t.Fatalf("metrics=%d err=%v", len(m), err)
	}
}
func TestRegistryCollectorTracksZeroPresenceRecoveryAndUnknownStorage(t *testing.T) {
	expected := []discovery.MonitoredService{{ServiceKey: "booking.primary", Contract: "eversports-booking", Major: 2, Capabilities: []string{"booking.create"}}}
	instance := discovery.MonitoringInstance{Descriptor: discovery.Descriptor{ServiceKey: "booking.primary", State: discovery.StateReady, Contracts: []discovery.Contract{{Name: "eversports-booking", Major: 2}}, Capabilities: []discovery.Capability{{Name: "booking.create", Contract: "eversports-booking", Major: 2}}}, State: discovery.StateReady}
	var snapshot []discovery.MonitoringInstance
	var storageErr error
	r := prometheus.NewRegistry()
	r.MustRegister(NewRegistryCollector(expected, func(context.Context) ([]discovery.MonitoringInstance, error) { return snapshot, storageErr }))
	check := func(wantHealthy, wantLive float64, livePresent bool) {
		t.Helper()
		families, err := r.Gather()
		if err != nil {
			t.Fatal(err)
		}
		values := make(map[string]float64)
		for _, family := range families {
			for _, metric := range family.Metric {
				if family.GetName() == MetricServiceLiveInstances || family.GetName() == MetricServiceExpectedInstances {
					if len(metric.Label) != 1 || metric.Label[0].GetName() != "service_key" || metric.Label[0].GetValue() != "booking.primary" {
						t.Fatalf("unbounded or wrong labels: %+v", metric.Label)
					}
				}
				values[family.GetName()] = metric.GetGauge().GetValue()
			}
		}
		if values[MetricRegistryStorageHealthy] != wantHealthy || values[MetricServiceExpectedInstances] != 1 {
			t.Fatalf("snapshot health/expected = %+v", values)
		}
		got, ok := values[MetricServiceLiveInstances]
		if ok != livePresent || (ok && got != wantLive) {
			t.Fatalf("live=%v present=%t want=%v present=%t", got, ok, wantLive, livePresent)
		}
	}
	check(1, 0, true) // no registration, but expected inventory must still alert
	snapshot = []discovery.MonitoringInstance{instance}
	check(1, 1, true)
	snapshot = nil // lease expired / last scrape target removed
	check(1, 0, true)
	storageErr = errors.New("synthetic registry DB outage")
	check(0, 0, false) // unknown storage state must not masquerade as known zero
}

func TestRegistryCollectorStorageFailureOmitsLive(t *testing.T) {
	c := NewRegistryCollector([]discovery.MonitoredService{{ServiceKey: "web", Contract: "web", Major: 1}}, func(context.Context) ([]discovery.MonitoringInstance, error) { return nil, errors.New("down") })
	r := prometheus.NewRegistry()
	r.MustRegister(c)
	m, err := r.Gather()
	if err != nil || len(m) != 2 {
		t.Fatalf("metrics=%d err=%v", len(m), err)
	}
}
