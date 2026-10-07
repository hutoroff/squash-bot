package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestHTTPMetricsUsesRouteTemplateAndUnmatchedFallback(t *testing.T) {
	metrics := NewHTTPMetrics()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/games/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "123" {
			t.Errorf("path value = %q", r.PathValue("id"))
		}
		w.WriteHeader(http.StatusNoContent)
	})
	server := metrics.WrapMux(mux)
	server.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/games/123?secret=no", nil))
	for i := 0; i < 100; i++ {
		server.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("ARBITRARY"+strings.Repeat("Z", i), "/unmatched/456", nil))
	}
	registry := prometheus.NewRegistry()
	for _, c := range metrics.Collectors() {
		registry.MustRegister(c)
	}
	gathered, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	foundTemplate, foundFallback := false, false
	for _, family := range gathered {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetName() == "route" && label.GetValue() == "GET /api/v1/games/{id}" {
					foundTemplate = true
				}
				if label.GetName() == "route" && label.GetValue() == "unmatched" {
					foundFallback = true
				}
				if label.GetName() == "method" && strings.HasPrefix(label.GetValue(), "ARBITRARY") {
					t.Fatal("unbounded method label")
				}
				if label.GetValue() == "/api/v1/games/123" {
					t.Fatal("raw path label")
				}
			}
		}
	}
	if !foundTemplate || !foundFallback {
		t.Fatalf("template=%t fallback=%t", foundTemplate, foundFallback)
	}
	if len(gathered) != 2 {
		t.Fatalf("metric families = %d", len(gathered))
	}
	if len(gathered[0].Metric) > 2 || len(gathered[1].Metric) > 2 {
		t.Fatal("unbounded route or method series")
	}
}
