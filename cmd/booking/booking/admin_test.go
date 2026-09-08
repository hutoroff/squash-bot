package booking

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hutoroff/squash-bot/cmd/booking/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
)

func TestAdminServerRequiresSeparateBearerAndReportsReadiness(t *testing.T) {
	ready := false
	srv, _, err := NewAdminServer(AdminConfig{
		Addr: ":9091", Secret: "monitoring-secret", Version: "1.2.3", ServiceKey: "booking.primary", InstanceID: "0195f8db-7a6f-7c7b-8f71-0f6e0712042e",
		APIEndpoint: "http://booking:8081", AdminEndpoint: "http://booking:9091",
		Scope: bookingapi.Scope{Provider: "eversports", FacilityID: "facility", FacilityUUID: "uuid", FacilitySlug: "slug", Sport: "squash"},
		Readiness: func(context.Context) Readiness {
			return Readiness{Ready: ready, Database: true, Singleton: ready, Coordinator: ready}
		},
		Running: func() int { return 1 },
		LedgerHealth: func(context.Context) (storage.LedgerHealth, error) {
			return storage.LedgerHealth{UnknownOperations: 2}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewAdminServer: %v", err)
	}

	request := func(path string, authenticated bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if authenticated {
			req.Header.Set("Authorization", "Bearer monitoring-secret")
		}
		response := httptest.NewRecorder()
		srv.Handler.ServeHTTP(response, req)
		return response
	}
	if got := request("/health/live", false).Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthenticated liveness status = %d, want 401", got)
	}
	if got := request("/health/ready", true).Code; got != http.StatusServiceUnavailable {
		t.Fatalf("unready status = %d, want 503", got)
	}
	ready = true
	if got := request("/health/ready", true).Code; got != http.StatusOK {
		t.Fatalf("ready status = %d, want 200", got)
	}
	descriptor := request("/descriptor", true)
	if descriptor.Code != http.StatusOK || strings.Contains(descriptor.Body.String(), "secret") || !strings.Contains(descriptor.Body.String(), `"contracts":[{"name":"eversports-booking","major":2`) {
		t.Fatalf("descriptor = %d %s", descriptor.Code, descriptor.Body.String())
	}
	metrics := request("/metrics", true)
	if metrics.Code != http.StatusOK || !strings.Contains(metrics.Body.String(), "booking_operations_unknown 2") || !strings.Contains(metrics.Body.String(), "go_goroutines") {
		t.Fatalf("metrics = %d %s", metrics.Code, metrics.Body.String())
	}
}
