package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
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

// ObservePreDispatch counts a failed logical lookup without implying a
// business request was sent or replayed.
func (m *DependencyMetrics) ObservePreDispatch(target string, started time.Time) {
	m.observe(target, "resolution_error", started)
}

// RoundTripper observes a single transport dispatch, including transport
// failures. Resolution failures before dispatch must be recorded by the caller.
func (m *DependencyMetrics) RoundTripper(target string, base http.RoundTripper) http.RoundTripper {
	return observedTransport{metrics: m, target: target, base: base}
}

type observedTransport struct {
	metrics *DependencyMetrics
	target  string
	base    http.RoundTripper
}

func (t observedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	started := time.Now()
	response, err := t.base.RoundTrip(request)
	status := 0
	if response != nil {
		status = response.StatusCode
	}
	t.metrics.Observe(t.target, status, err, started)
	return response, err
}

func (m *DependencyMetrics) Observe(target string, status int, err error, started time.Time) {
	outcome := "transport_error"
	if err == nil && status >= 100 && status <= 599 {
		outcome = strconv.Itoa(status)
	}
	m.observe(target, outcome, started)
}
func (m *DependencyMetrics) observe(target, outcome string, started time.Time) {
	if target != "management" && target != "booking" {
		target = "other"
	}
	m.requests.WithLabelValues(target, outcome).Inc()
	m.duration.WithLabelValues(target).Observe(time.Since(started).Seconds())
}
