package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// HTTPMetrics records bounded incoming HTTP dimensions. Route must be a
// registered template, never a raw path.
type HTTPMetrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func NewHTTPMetrics() *HTTPMetrics {
	return &HTTPMetrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: MetricHTTPRequestsTotal, Help: "HTTP requests by bounded route and status."}, []string{"method", "route", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: MetricHTTPRequestDurationSeconds, Help: "HTTP request duration by bounded route."}, []string{"method", "route"}),
	}
}
func (m *HTTPMetrics) Collectors() []prometheus.Collector {
	return []prometheus.Collector{m.requests, m.duration}
}
func (m *HTTPMetrics) Observe(method, route string, status int, duration time.Duration) {
	if route == "" {
		route = "unmatched"
	}
	m.requests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	m.duration.WithLabelValues(method, route).Observe(duration.Seconds())
}
func (m *HTTPMetrics) WrapMux(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &responseRecorder{ResponseWriter: w}
		handler, pattern := mux.Handler(r)
		handler.ServeHTTP(recorder, r)
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}
		m.Observe(r.Method, pattern, recorder.status, time.Since(started))
	})
}
func (m *HTTPMetrics) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}
		m.Observe(r.Method, r.Pattern, recorder.status, time.Since(started))
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (w *responseRecorder) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseRecorder) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}
