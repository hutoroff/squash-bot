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

// BoundedMethod prevents arbitrary method tokens from creating metric series.
func BoundedMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions:
		return method
	default:
		return "OTHER"
	}
}

func (m *HTTPMetrics) Observe(method, route string, status int, duration time.Duration) {
	if route == "" {
		route = "unmatched"
	}
	m.requests.WithLabelValues(BoundedMethod(method), route, strconv.Itoa(status)).Inc()
	m.duration.WithLabelValues(BoundedMethod(method), route).Observe(duration.Seconds())
}
func (m *HTTPMetrics) WrapMux(mux *http.ServeMux) http.Handler {
	return m.WrapMuxHandler(mux, mux)
}

// WrapMuxHandler pre-matches bounded patterns while preserving the mux's normal
// dispatch (including path-value population and redirects) and outer middleware.
func (m *HTTPMetrics) WrapMuxHandler(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &responseRecorder{ResponseWriter: w}
		_, pattern := mux.Handler(r)
		next.ServeHTTP(recorder, r)
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}
		m.Observe(r.Method, pattern, recorder.status, time.Since(started))
	})
}

// WrapNamed records a fixed route for handlers whose registered path is
// operator-configured (for example Telegram's webhook URL).
func (m *HTTPMetrics) WrapNamed(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}
		m.Observe(r.Method, route, recorder.status, time.Since(started))
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
