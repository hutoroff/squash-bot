package service

import (
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/observability"
	"github.com/prometheus/client_golang/prometheus"
)

type observedJobFixture struct {
	key, outcome string
	entered      chan struct{}
	release      chan struct{}
}

func (j *observedJobFixture) name() string   { return j.key }
func (j *observedJobFixture) run(force bool) { j.runWithOutcome(force) }
func (j *observedJobFixture) runWithOutcome(bool) string {
	if j.entered != nil {
		close(j.entered)
		<-j.release
	}
	return j.outcome
}

func TestSchedulerExplicitResultsAndHungJobCompletion(t *testing.T) {
	metrics := observability.NewSchedulerMetrics(5 * time.Minute)
	hung := &observedJobFixture{key: "auto_booking", outcome: "skipped_dependency", entered: make(chan struct{}), release: make(chan struct{})}
	scheduler := NewScheduler(nil, hung)
	scheduler.SetObserver(metrics)
	done := make(chan struct{})
	go func() { defer close(done); scheduler.RunScheduledTasks() }()
	<-hung.entered
	registry := prometheus.NewRegistry()
	for _, collector := range metrics.Collectors() {
		registry.MustRegister(collector)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() == observability.MetricSchedulerLastCompletedTimestamp && len(f.Metric) != 0 {
			t.Fatal("hung job advanced completion timestamp")
		}
	}
	close(hung.release)
	<-done
	scheduler = NewScheduler(nil, &observedJobFixture{key: "auto_approve_results", outcome: "success"}, &observedJobFixture{key: "final_court_check", outcome: "failure"})
	scheduler.SetObserver(metrics)
	scheduler.RunScheduledTasks()
	families, err = registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	interval := false
	completed := 0
	for _, f := range families {
		switch f.GetName() {
		case observability.MetricSchedulerRunsTotal:
			for _, m := range f.Metric {
				labels := map[string]string{}
				for _, l := range m.Label {
					labels[l.GetName()] = l.GetValue()
				}
				found[labels["outcome"]] = true
				if m.GetCounter().GetValue() != 1 {
					t.Fatal("run double counted")
				}
			}
		case observability.MetricSchedulerLastCompletedTimestamp:
			completed = len(f.Metric)
		case "squash_scheduler_poll_interval_seconds":
			interval = len(f.Metric) == 1 && f.Metric[0].GetGauge().GetValue() == 300
		}
	}
	if !found["success"] || !found["failure"] || !found["skipped_dependency"] || !interval || completed != 3 {
		t.Fatalf("results=%v interval=%t completed=%d", found, interval, completed)
	}
}
