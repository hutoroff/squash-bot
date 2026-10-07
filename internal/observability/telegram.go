package observability

import "github.com/prometheus/client_golang/prometheus"

// UpdateMetrics counts transport-accepted updates after local processing.
// A processed outcome means the handler returned, not that downstream side
// effects succeeded; most bot handlers handle their own errors.
type UpdateMetrics struct{ total *prometheus.CounterVec }

func NewUpdateMetrics() *UpdateMetrics {
	return &UpdateMetrics{total: prometheus.NewCounterVec(prometheus.CounterOpts{Name: MetricTelegramUpdatesTotal, Help: "Telegram update handler returns by bounded kind and outcome."}, []string{"kind", "outcome"})}
}
func (m *UpdateMetrics) Collectors() []prometheus.Collector { return []prometheus.Collector{m.total} }
func (m *UpdateMetrics) Observe(kind, outcome string) {
	switch kind {
	case "message", "callback", "membership":
	default:
		kind = "other"
	}
	switch outcome {
	case "processed", "ignored", "panic":
	default:
		outcome = "ignored"
	}
	m.total.WithLabelValues(kind, outcome).Inc()
}
