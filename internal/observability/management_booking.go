package observability

import (
	"context"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/prometheus/client_golang/prometheus"
)

type BookingMetricsSource func(context.Context) (*models.ManagementBookingMetrics, error)

// ManagementBookingCollector reads durable, bounded database aggregates on each
// scrape. HTTP status reads, repeated keys and restarts cannot increment an
// execution counter. When unavailable, state gauges are omitted, not stale.
type ManagementBookingCollector struct{ read BookingMetricsSource }

func NewManagementBookingCollector(read BookingMetricsSource) *ManagementBookingCollector {
	return &ManagementBookingCollector{read: read}
}
func (c *ManagementBookingCollector) Describe(chan<- *prometheus.Desc) {} // dynamic, fixed names and labels
func (c *ManagementBookingCollector) Collect(ch chan<- prometheus.Metric) {
	health := prometheus.NewDesc(MetricManagementBookingStorageHealthy, "Whether durable booking metrics were read.", nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var metrics *models.ManagementBookingMetrics
	var err error
	if c.read != nil {
		metrics, err = c.read(ctx)
	}
	if err != nil || metrics == nil {
		ch <- prometheus.MustNewConstMetric(health, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(health, prometheus.GaugeValue, 1)
	states := prometheus.NewDesc(MetricManagementBookingOperations, "Durable management booking operation counts.", []string{"state"}, nil)
	for _, entry := range []struct {
		name  string
		count int64
	}{{"planned", metrics.Planned}, {"accepted", metrics.Accepted}, {"running", metrics.Running}, {"outcome_unknown", metrics.OutcomeUnknown}, {"succeeded", metrics.Succeeded}, {"rejected_no_effect", metrics.RejectedNoEffect}} {
		ch <- prometheus.MustNewConstMetric(states, prometheus.GaugeValue, float64(entry.count), entry.name)
	}
	ch <- prometheus.MustNewConstMetric(prometheus.NewDesc(MetricManagementBookingPendingNotices, "Unclaimed operator notifications.", nil, nil), prometheus.GaugeValue, float64(metrics.PendingNotices))
	ch <- prometheus.MustNewConstMetric(prometheus.NewDesc(MetricManagementBookingRiskAuthorized, "Durable linked duplicate-risk operation count.", nil, nil), prometheus.GaugeValue, float64(metrics.RiskAuthorized))
	age := float64(0)
	if metrics.OldestPendingAt != nil {
		age = time.Since(*metrics.OldestPendingAt).Seconds()
		if age < 0 {
			age = 0
		}
	}
	ch <- prometheus.MustNewConstMetric(prometheus.NewDesc(MetricManagementBookingOldestPendingAge, "Age of oldest unresolved operation, zero when none.", nil, nil), prometheus.GaugeValue, age)
}
