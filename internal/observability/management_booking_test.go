package observability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/prometheus/client_golang/prometheus"
)

func TestDurableBookingCollectorBoundsStorageRead(t *testing.T) {
	collector := NewManagementBookingCollector(func(ctx context.Context) (*models.ManagementBookingMetrics, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)
	started := time.Now()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("storage read exceeded bounded collection budget")
	}
	if len(families) != 1 || families[0].GetName() != MetricManagementBookingStorageHealthy || families[0].Metric[0].GetGauge().GetValue() != 0 {
		t.Fatalf("bounded failure metrics = %+v", families)
	}
}

func TestDurableBookingCollectorReplayIsNotExecution(t *testing.T) {
	collector := NewManagementBookingCollector(func(context.Context) (*models.ManagementBookingMetrics, error) {
		return &models.ManagementBookingMetrics{OutcomeUnknown: 1}, nil
	})
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)
	for i := 0; i < 2; i++ {
		families, err := registry.Gather()
		if err != nil {
			t.Fatal(err)
		}
		for _, family := range families {
			if family.GetName() == MetricBookingOperationsTotal {
				t.Fatal("poll incremented execution counter")
			}
			if family.GetName() != MetricManagementBookingOperations {
				continue
			}
			for _, m := range family.Metric {
				if m.Label[0].GetValue() == "outcome_unknown" && m.GetGauge().GetValue() != 1 {
					t.Fatal("unchanged durable state was double counted")
				}
			}
		}
	}
}

func TestDurableBookingCollectorCountsAgeAndStorageFailure(t *testing.T) {
	oldest := time.Now().Add(-10 * time.Minute)
	calls := 0
	collector := NewManagementBookingCollector(func(context.Context) (*models.ManagementBookingMetrics, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("DB down")
		}
		return &models.ManagementBookingMetrics{Planned: 1, OutcomeUnknown: 2, RiskAuthorized: 1, PendingNotices: 3, OldestPendingAt: &oldest}, nil
	})
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]float64{}
	foundAge, foundRisk, foundNotices, healthy := false, false, false, false
	for _, family := range families {
		switch family.GetName() {
		case MetricManagementBookingStorageHealthy:
			healthy = family.Metric[0].GetGauge().GetValue() == 1
		case MetricManagementBookingOperations:
			for _, m := range family.Metric {
				states[m.Label[0].GetValue()] = m.GetGauge().GetValue()
			}
		case MetricManagementBookingPendingNotices:
			foundNotices = family.Metric[0].GetGauge().GetValue() == 3
		case MetricManagementBookingRiskAuthorized:
			foundRisk = family.Metric[0].GetGauge().GetValue() == 1
		case MetricManagementBookingOldestPendingAge:
			age := family.Metric[0].GetGauge().GetValue()
			foundAge = age >= 600 && age < 630
		}
	}
	if !healthy || !foundAge || !foundRisk || !foundNotices || states["outcome_unknown"] != 2 || states["planned"] != 1 || len(states) != 6 {
		t.Fatalf("healthy=%t age=%t risk=%t notices=%t states=%v", healthy, foundAge, foundRisk, foundNotices, states)
	}
	families, err = registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 1 || families[0].GetName() != MetricManagementBookingStorageHealthy || families[0].Metric[0].GetGauge().GetValue() != 0 {
		t.Fatalf("stale booking aggregates on DB failure: %+v", families)
	}
}
