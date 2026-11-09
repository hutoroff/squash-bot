package observability

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestDependencyMetricsBoundedTargetsOutcomesAndDispatch(t *testing.T) {
	m := NewDependencyMetrics()
	m.ObservePreDispatch("booking", time.Now())
	m.Observe("other-unique-user-target", 999, errors.New("transport failed: secret URL"), time.Now())
	baseCalls := 0
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		baseCalls++
		return &http.Response{StatusCode: http.StatusAccepted, Body: http.NoBody}, nil
	})
	request, err := http.NewRequest(http.MethodPost, "http://example.test/private/path?token=secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := m.RoundTripper("management", base).RoundTrip(request)
	if err != nil || response.StatusCode != http.StatusAccepted || baseCalls != 1 {
		t.Fatalf("response=%v err=%v calls=%d", response, err, baseCalls)
	}
	r := prometheus.NewRegistry()
	for _, collector := range m.Collectors() {
		r.MustRegister(collector)
	}
	families, err := r.Gather()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, family := range families {
		if family.GetName() != MetricDependencyRequestsTotal {
			continue
		}
		count = len(family.Metric)
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetValue() == "private" || label.GetValue() == "other-unique-user-target" || label.GetValue() == "999" {
					t.Fatalf("unbounded label: %q", label.GetValue())
				}
			}
			if metric.GetCounter().GetValue() != 1 {
				t.Fatalf("request counted more than once: %v", metric.GetCounter().GetValue())
			}
		}
	}
	if count != 3 {
		t.Fatalf("outcome series = %d, want 3", count)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
