package service

import (
	"context"
	"errors"
	"testing"

	"github.com/hutoroff/squash-bot/internal/discovery"
)

type registryServiceRepositoryFake struct {
	registered bool
	catalog    []discovery.CatalogEntry
	err        error
}

func (r *registryServiceRepositoryFake) Register(context.Context, discovery.Descriptor) (*discovery.Lease, error) {
	r.registered = true
	return &discovery.Lease{}, nil
}
func (*registryServiceRepositoryFake) Heartbeat(context.Context, string, discovery.InstanceState) (*discovery.Lease, error) {
	return &discovery.Lease{}, nil
}
func (*registryServiceRepositoryFake) Drain(context.Context, string) error      { return nil }
func (*registryServiceRepositoryFake) Deregister(context.Context, string) error { return nil }
func (r *registryServiceRepositoryFake) Resolve(context.Context, discovery.Requirement) ([]discovery.ResolvedEndpoint, error) {
	return nil, r.err
}
func (r *registryServiceRepositoryFake) Catalog(context.Context) ([]discovery.CatalogEntry, error) {
	return r.catalog, r.err
}

func TestServiceRegistryClassifiesIncompatibleLogicalBinding(t *testing.T) {
	repository := &registryServiceRepositoryFake{catalog: []discovery.CatalogEntry{{ServiceKey: "booking.default", Scope: map[string]string{"facility_id": "one"}, Contracts: []discovery.Contract{{Name: "eversports-booking", Major: 1}}}}}
	service, err := NewServiceRegistryService(repository, "10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Resolve(context.Background(), discovery.Requirement{ServiceKey: "booking.default", Contract: "eversports-booking", Major: 2, Scope: map[string]string{"facility_id": "one"}})
	if !errors.Is(err, discovery.ErrIncompatible) {
		t.Fatalf("error = %v", err)
	}
}

func TestServiceRegistryPropagatesStorageFailureInsteadOfEmptyResolution(t *testing.T) {
	repository := &registryServiceRepositoryFake{err: errors.New("database unavailable")}
	service, err := NewServiceRegistryService(repository, "10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Resolve(context.Background(), discovery.Requirement{ServiceKey: "booking.default", Contract: "eversports-booking", Major: 2, Scope: map[string]string{"facility_id": "one"}})
	if err == nil || errors.Is(err, discovery.ErrIncompatible) {
		t.Fatalf("storage failure was converted to resolution result: %v", err)
	}
}

func TestServiceRegistryRejectsUnapprovedAddressesBeforePersistence(t *testing.T) {
	repository := &registryServiceRepositoryFake{}
	service, err := NewServiceRegistryService(repository, "10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	descriptor := discovery.Descriptor{
		SchemaVersion: 1, InstanceID: "0195f8db-7a6f-7c7b-8f71-0f6e0712042e", ServiceKey: "web", ServiceKind: "web",
		Implementation: "squash-web", ReleaseVersion: "1", Endpoints: discovery.Endpoints{API: "http://203.0.113.10:8082", Admin: "http://203.0.113.10:9090"},
		Contracts: []discovery.Contract{{Name: "web-api", Major: 1}}, Capabilities: []discovery.Capability{}, State: discovery.StateReady,
	}
	if _, err := service.Register(context.Background(), descriptor); err == nil || repository.registered {
		t.Fatalf("public endpoint accepted: %v", err)
	}
	descriptor.Endpoints = discovery.Endpoints{API: "http://10.1.2.3:8081", Admin: "http://10.1.2.3:9090"}
	if _, err := service.Register(context.Background(), descriptor); err == nil || repository.registered {
		t.Fatalf("wrong web port accepted: %v", err)
	}
	descriptor.Endpoints.API = "http://10.1.2.3:8082"
	if _, err := service.Register(context.Background(), descriptor); err != nil || !repository.registered {
		t.Fatalf("valid endpoint rejected: %v", err)
	}
}
