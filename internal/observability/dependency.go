package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"strconv"
	"time"
)

// DependencyMetrics uses only fixed logical dependency names; callers must not
// pass URLs, paths, identifiers, or provider error text as labels.
type DependencyMetrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func NewDependencyMetrics() *DependencyMetrics {
	return &DependencyMetrics{prometheus.NewCounterVec(prometheus.CounterOpts{Name: MetricDependencyRequestsTotal, Help: "Logical dependency requests by bounded target and outcome."}, []string{"target", "outcome"}), prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: MetricDependencyRequestDurationSeconds, Help: "Logical dependency request duration."}, []string{"target"})}
}
func (m *DependencyMetrics) Collectors() []prometheus.Collector {
	return []prometheus.Collector{m.requests, m.duration}
}
func (m *DependencyMetrics) Observe(target string, status int, err error, started time.Time) {
	outcome := "error"
	if err == nil {
		outcome = strconv.Itoa(status)
	}
	m.requests.WithLabelValues(target, outcome).Inc()
	m.duration.WithLabelValues(target).Observe(time.Since(started).Seconds())
}
