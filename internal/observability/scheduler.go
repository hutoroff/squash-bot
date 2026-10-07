package observability

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// SchedulerMetrics records bounded job results. A timestamp advances only
// after the job returns, never at dispatch or while blocked.
type SchedulerMetrics struct {
	runs      *prometheus.CounterVec
	completed *prometheus.GaugeVec
	interval  prometheus.Gauge
	mu        sync.Mutex
}

func NewSchedulerMetrics(pollInterval time.Duration) *SchedulerMetrics {
	m := &SchedulerMetrics{
		runs:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: MetricSchedulerRunsTotal, Help: "Completed scheduler jobs by bounded name and outcome."}, []string{"job", "outcome"}),
		completed: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: MetricSchedulerLastCompletedTimestamp, Help: "Unix timestamp of last returned job, regardless of outcome."}, []string{"job"}),
		interval:  prometheus.NewGauge(prometheus.GaugeOpts{Name: "squash_scheduler_poll_interval_seconds", Help: "Configured cron poll interval in seconds."}),
	}
	m.interval.Set(pollInterval.Seconds())
	return m
}
func (m *SchedulerMetrics) SetPollInterval(interval time.Duration) {
	m.interval.Set(interval.Seconds())
}
func (m *SchedulerMetrics) Collectors() []prometheus.Collector {
	return []prometheus.Collector{m.runs, m.completed, m.interval}
}
func (m *SchedulerMetrics) Observe(job, outcome string, at time.Time) {
	switch job {
	case "cancellation_reminder", "halfway_court_check", "final_court_check", "booking_reminder", "day_after_cleanup", "auto_booking", "auto_approve_results", "post_leaderboard":
	default:
		job = "other"
	}
	switch outcome {
	case "success", "failure", "skipped_dependency", "skipped", "unclassified":
	default:
		outcome = "unclassified"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs.WithLabelValues(job, outcome).Inc()
	m.completed.WithLabelValues(job).Set(float64(at.Unix()))
}
