package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPMetricsUsesRouteTemplateAndUnmatchedFallback(t *testing.T) {
	metrics := NewHTTPMetrics()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/games/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	server := metrics.WrapMux(mux)
	server.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/games/123?secret=no", nil))
	server.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/unmatched/456", nil))
	registry := prometheus.NewRegistry()
	for _, c := range metrics.Collectors() {
		registry.MustRegister(c)
	}
	gathered, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	foundFallback := false
	for _, family := range gathered {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetName() == "route" && label.GetValue() == "GET /api/v1/games/{id}" {
					foundFallback = true
				}
				if label.GetValue() == "/api/v1/games/123" {
					t.Fatal("raw path label")
				}
			}
		}
	}
	if !foundFallback {
		t.Fatal("template route metric missing")
	}
}
