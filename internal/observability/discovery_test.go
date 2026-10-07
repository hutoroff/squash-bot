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
func TestRegistryCollectorStorageFailureOmitsLive(t *testing.T) {
	c := NewRegistryCollector([]discovery.MonitoredService{{ServiceKey: "web", Contract: "web", Major: 1}}, func(context.Context) ([]discovery.MonitoringInstance, error) { return nil, errors.New("down") })
	r := prometheus.NewRegistry()
	r.MustRegister(c)
	m, err := r.Gather()
	if err != nil || len(m) != 2 {
		t.Fatalf("metrics=%d err=%v", len(m), err)
	}
}
