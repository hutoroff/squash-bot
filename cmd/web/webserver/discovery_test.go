package webserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/discovery"
)

func TestDiscoveredManagementTargetRewritesBusinessRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/version" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	descriptor := discovery.Descriptor{SchemaVersion: discovery.SchemaVersion, InstanceID: "0195f8db-7a6f-7c7b-8f71-0f6e07120437", ServiceKey: "management", ServiceKind: "management", Implementation: "squash-management", ReleaseVersion: "1", Endpoints: discovery.Endpoints{API: server.URL, Admin: server.URL}, Contracts: []discovery.Contract{{Name: "management-api", Major: 1}}, Capabilities: []discovery.Capability{{Name: "management.api", Contract: "management-api", Major: 1}}, State: discovery.StateReady}
	resolver, err := discovery.NewStaticResolver([]discovery.Descriptor{descriptor})
	if err != nil {
		t.Fatal(err)
	}
	target := NewDiscoveredManagementTarget(resolver, discovery.Requirement{ServiceKey: "management", Contract: "management-api", Major: 1, RequiredCapabilities: []string{"management.api"}})
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target.requestBaseURL()+"/api/v1/version", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := newTargetHTTPClient(target, time.Second).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", response.StatusCode)
	}
}
