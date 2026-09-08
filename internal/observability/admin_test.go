package observability

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hutoroff/squash-bot/internal/discovery"
)

func TestAdminServerProtectsEveryRouteAndReportsReadiness(t *testing.T) {
	ready := false
	server, err := NewAdminServer(AdminConfig{
		Addr: ":9090", Token: strings.Repeat("m", 32), Version: "1.2.3",
		Descriptor: discovery.Descriptor{
			SchemaVersion: discovery.SchemaVersion, InstanceID: "0195f8db-7a6f-7c7b-8f71-0f6e0712042e",
			ServiceKey: "web", ServiceKind: "web", Implementation: "squash-web", ReleaseVersion: "1.2.3",
			Endpoints: discovery.Endpoints{API: "http://web:8082", Admin: "http://web:9090"},
			Contracts: []discovery.Contract{{Name: "web-api", Major: 1}}, State: discovery.StateReady,
		},
		Readiness: func(context.Context) Readiness {
			return Readiness{Ready: ready, Checks: map[string]bool{"local": ready}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/health/live", "/health/ready", "/version", "/descriptor", "/metrics"} {
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s status = %d", path, response.Code)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("m", 32))
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready status = %d", response.Code)
	}
	ready = true
	request = httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("m", 32))
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	var health Readiness
	if response.Code != http.StatusOK || json.NewDecoder(response.Body).Decode(&health) != nil || !health.Ready {
		t.Fatalf("ready response = %d %+v", response.Code, health)
	}
	request = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("m", 32))
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "squash_service_ready") {
		t.Fatalf("metrics status/body = %d %q", response.Code, response.Body.String())
	}
}
