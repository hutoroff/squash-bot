package observability

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/hutoroff/squash-bot/internal/discovery"
	"github.com/prometheus/client_golang/prometheus"
)

type RegistrySnapshot func(context.Context) ([]discovery.MonitoringInstance, error)

type RegistryCollector struct {
	expected []discovery.MonitoredService
	snapshot RegistrySnapshot
}

func NewRegistryCollector(expected []discovery.MonitoredService, snapshot RegistrySnapshot) *RegistryCollector {
	return &RegistryCollector{expected: expected, snapshot: snapshot}
}
func (c *RegistryCollector) Describe(ch chan<- *prometheus.Desc) {}
func (c *RegistryCollector) Collect(ch chan<- prometheus.Metric) {
	storage := prometheus.NewDesc(MetricRegistryStorageHealthy, "Whether the authoritative registry snapshot was read.", nil, nil)
	expected := prometheus.NewDesc(MetricServiceExpectedInstances, "Configured expected singleton instances.", []string{"service_key"}, nil)
	live := prometheus.NewDesc(MetricServiceLiveInstances, "Eligible ready unexpired instances.", []string{"service_key"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	instances, err := c.snapshot(ctx)
	if err != nil {
		ch <- prometheus.MustNewConstMetric(storage, prometheus.GaugeValue, 0)
		for _, e := range c.expected {
			ch <- prometheus.MustNewConstMetric(expected, prometheus.GaugeValue, 1, e.ServiceKey)
		}
		return
	}
	ch <- prometheus.MustNewConstMetric(storage, prometheus.GaugeValue, 1)
	for _, e := range c.expected {
		req := discovery.Requirement{ServiceKey: e.ServiceKey, Contract: e.Contract, Major: e.Major, RequiredCapabilities: e.Capabilities, Scope: e.Scope}
		count := 0
		for _, instance := range instances {
			if instance.State == discovery.StateReady && instance.Descriptor.Satisfies(req) {
				count++
			}
		}
		ch <- prometheus.MustNewConstMetric(expected, prometheus.GaugeValue, 1, e.ServiceKey)
		ch <- prometheus.MustNewConstMetric(live, prometheus.GaugeValue, float64(count), e.ServiceKey)
	}
}

// RegistrationMetrics records only the current lease; a past success expires
// automatically rather than reporting a permanently healthy process.
type RegistrationMetrics struct {
	expires     atomic.Int64
	lastSuccess atomic.Int64
}

func NewRegistrationMetrics() *RegistrationMetrics { return &RegistrationMetrics{} }
func (m *RegistrationMetrics) Registered(lease *discovery.Lease) {
	if m != nil && lease != nil {
		m.expires.Store(lease.LeaseExpiresAt.Unix())
		m.lastSuccess.Store(lease.RegistryTime.Unix())
	}
}
func (m *RegistrationMetrics) Describe(ch chan<- *prometheus.Desc) {}
func (m *RegistrationMetrics) Collect(ch chan<- prometheus.Metric) {
	success := prometheus.NewDesc(MetricDiscoveryRegistrationSuccess, "Whether the latest registration lease remains valid.", nil, nil)
	last := prometheus.NewDesc(MetricDiscoveryLastSuccessTimestamp, "Registry time of the last successful registration.", nil, nil)
	v := float64(0)
	if m.expires.Load() > time.Now().Unix() {
		v = 1
	}
	ch <- prometheus.MustNewConstMetric(success, prometheus.GaugeValue, v)
	ch <- prometheus.MustNewConstMetric(last, prometheus.GaugeValue, float64(m.lastSuccess.Load()))
}
