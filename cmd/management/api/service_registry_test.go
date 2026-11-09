package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/discovery"
)

type registryRepositoryFake struct {
	descriptor discovery.Descriptor
	resolve    []discovery.ResolvedEndpoint
	instances  []discovery.MonitoringInstance
	err        error
}

func (r *registryRepositoryFake) Register(_ context.Context, descriptor discovery.Descriptor) (*discovery.Lease, error) {
	r.descriptor = descriptor
	return &discovery.Lease{}, r.err
}
func (r *registryRepositoryFake) Heartbeat(context.Context, string, discovery.InstanceState) (*discovery.Lease, error) {
	return &discovery.Lease{}, r.err
}
func (r *registryRepositoryFake) Drain(context.Context, string) error      { return r.err }
func (r *registryRepositoryFake) Deregister(context.Context, string) error { return r.err }
func (r *registryRepositoryFake) Resolve(context.Context, discovery.Requirement) ([]discovery.ResolvedEndpoint, error) {
	return r.resolve, r.err
}
func (r *registryRepositoryFake) Catalog(context.Context) ([]discovery.CatalogEntry, error) {
	return []discovery.CatalogEntry{}, r.err
}
func (r *registryRepositoryFake) MonitoringInstances(context.Context) ([]discovery.MonitoringInstance, error) {
	return r.instances, r.err
}

func TestServiceRegistryRoutesAreAuthenticatedBoundedAndTyped(t *testing.T) {
	repository := &registryRepositoryFake{}
	registry, err := service.NewServiceRegistryService(repository, "10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{serviceRegistry: registry, logger: slog.Default()}
	server := NewServer(":0", handler, strings.Repeat("s", 32), strings.Repeat("m", 32))
	request := httptest.NewRequest(http.MethodGet, "/internal/discovery/v1/catalog", nil)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}

	instanceID := "0195f8db-7a6f-7c7b-8f71-0f6e0712042e"
	body := `{"schema_version":1,"instance_id":"` + instanceID + `","service_key":"web","service_kind":"web","implementation":"squash-web","release_version":"1","endpoints":{"api":"http://10.0.0.2:8082","admin":"http://10.0.0.2:9090"},"contracts":[{"name":"web-api","major":1,"minor":0}],"capabilities":[],"state":"ready"}`
	request = httptest.NewRequest(http.MethodPut, "/internal/discovery/v1/instances/"+instanceID, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("m", 32))
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("monitoring token registration status = %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPut, "/internal/discovery/v1/instances/"+instanceID, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 32))
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || repository.descriptor.InstanceID != instanceID {
		t.Fatalf("register = %d %+v", response.Code, repository.descriptor)
	}

	request = httptest.NewRequest(http.MethodGet, "/internal/discovery/v1/resolve?service_key=web&contract=web-api&major=1", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 32))
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != "[]" {
		t.Fatalf("empty resolve = %d %s", response.Code, response.Body.String())
	}

	repository.err = storage.ErrRegistryGone
	request = httptest.NewRequest(http.MethodPost, "/internal/discovery/v1/instances/"+instanceID+"/heartbeat", strings.NewReader(`{"state":"ready"}`))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 32))
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusGone {
		t.Fatalf("gone heartbeat status = %d", response.Code)
	}
	repository.err = storage.ErrRegistryCapacity
	request = httptest.NewRequest(http.MethodPut, "/internal/discovery/v1/instances/"+instanceID, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 32))
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("capacity registration status = %d", response.Code)
	}

	repository.err = nil
	repository.instances = []discovery.MonitoringInstance{{Descriptor: discovery.Descriptor{ServiceKey: "booking.default", ServiceKind: "booking", Endpoints: discovery.Endpoints{Admin: "http://10.0.0.3:9091"}}, State: discovery.StateReady}}
	request = httptest.NewRequest(http.MethodGet, "/internal/discovery/v1/prometheus-targets", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 32))
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("internal token scrape status = %d", response.Code)
	}
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("m", 32))
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "10.0.0.3:9091") {
		t.Fatalf("monitoring scrape = %d %s", response.Code, response.Body.String())
	}

	repository.err = errors.New("database unavailable")
	request = httptest.NewRequest(http.MethodGet, "/internal/discovery/v1/catalog", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("s", 32))
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable catalog status = %d", response.Code)
	}
}
