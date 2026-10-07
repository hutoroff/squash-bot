//go:build integration

package storage_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/discovery"
)

func registryDescriptor(instanceID string) discovery.Descriptor {
	return discovery.Descriptor{
		SchemaVersion: discovery.SchemaVersion, InstanceID: instanceID,
		ServiceKey: "booking.eversports.test", ServiceKind: "booking", Implementation: "eversports", ReleaseVersion: "2.0.0",
		Scope:        map[string]string{"facility_uuid": "facility-1", "sport": "squash"},
		Endpoints:    discovery.Endpoints{API: "http://10.0.0.10:8081", Admin: "http://10.0.0.10:9090"},
		Contracts:    []discovery.Contract{{Name: "eversports-booking", Major: 2}},
		Capabilities: []discovery.Capability{{Name: "booking.create", Contract: "eversports-booking", Major: 2}},
		State:        discovery.StateStarting,
	}
}

func TestServiceRegistryConcurrentMembershipLifecycleAndConflicts(t *testing.T) {
	mustTruncate(t)
	ctx := context.Background()
	repository := storage.NewServiceRegistryRepo(testPool)
	first := registryDescriptor("0195f8db-7a6f-7c7b-8f71-0f6e0712042e")
	second := registryDescriptor("0195f8db-7a6f-7c7b-8f71-0f6e0712042f")
	var wait sync.WaitGroup
	errorsCh := make(chan error, 2)
	for _, descriptor := range []discovery.Descriptor{first, second} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := repository.Register(ctx, descriptor)
			errorsCh <- err
		}()
	}
	wait.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent registration: %v", err)
		}
	}
	for _, descriptor := range []discovery.Descriptor{first, second} {
		if _, err := repository.Heartbeat(ctx, descriptor.InstanceID, discovery.StateReady); err != nil {
			t.Fatalf("heartbeat %s: %v", descriptor.InstanceID, err)
		}
	}
	requirement := discovery.Requirement{ServiceKey: first.ServiceKey, Contract: "eversports-booking", Major: 2, RequiredCapabilities: []string{"booking.create"}, Scope: first.Scope}
	resolved, err := repository.Resolve(ctx, requirement)
	if err != nil || len(resolved) != 2 {
		t.Fatalf("resolved = %+v err = %v", resolved, err)
	}

	conflictingScope := registryDescriptor("0195f8db-7a6f-7c7b-8f71-0f6e07120430")
	conflictingScope.Scope = map[string]string{"facility_uuid": "other", "sport": "squash"}
	if _, err := repository.Register(ctx, conflictingScope); !errors.Is(err, storage.ErrRegistryConflict) {
		t.Fatalf("scope conflict = %v", err)
	}
	changedIncarnation := first
	changedIncarnation.Endpoints.API = "http://10.0.0.11:8081"
	if _, err := repository.Register(ctx, changedIncarnation); !errors.Is(err, storage.ErrRegistryConflict) {
		t.Fatalf("incarnation conflict = %v", err)
	}
	if err := repository.Drain(ctx, first.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Heartbeat(ctx, first.InstanceID, discovery.StateReady); !errors.Is(err, storage.ErrRegistryGone) {
		t.Fatalf("late heartbeat = %v", err)
	}
	if _, err := repository.Register(ctx, first); !errors.Is(err, storage.ErrRegistryGone) {
		t.Fatalf("drained registration = %v", err)
	}
	resolved, err = repository.Resolve(ctx, requirement)
	if err != nil || len(resolved) != 1 || resolved[0].InstanceID != second.InstanceID {
		t.Fatalf("resolved after drain = %+v err = %v", resolved, err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE service_instances SET lease_expires_at = NOW() - interval '1 second' WHERE instance_id = $1`, second.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Heartbeat(ctx, second.InstanceID, discovery.StateReady); !errors.Is(err, storage.ErrRegistryGone) {
		t.Fatalf("expired heartbeat = %v", err)
	}
	if _, err := repository.Register(ctx, second); err != nil {
		t.Fatalf("expired full registration: %v", err)
	}
	if _, err := repository.Heartbeat(ctx, second.InstanceID, discovery.StateReady); err != nil {
		t.Fatalf("replacement heartbeat: %v", err)
	}
	if err := repository.Deregister(ctx, second.InstanceID); err != nil {
		t.Fatalf("deregister replacement: %v", err)
	}
	if err := repository.Deregister(ctx, second.InstanceID); err != nil {
		t.Fatalf("idempotent deregistration: %v", err)
	}
	resolved, err = repository.Resolve(ctx, requirement)
	if err != nil || len(resolved) != 0 {
		t.Fatalf("resolved terminal instance = %+v err = %v", resolved, err)
	}
	if _, err := repository.Heartbeat(ctx, second.InstanceID, discovery.StateReady); !errors.Is(err, storage.ErrRegistryGone) {
		t.Fatalf("terminal heartbeat = %v", err)
	}
	management := discovery.Descriptor{
		SchemaVersion: 1, InstanceID: "0195f8db-7a6f-7c7b-8f71-0f6e07120431", ServiceKey: "management",
		ServiceKind: "management", Implementation: "squash-management", ReleaseVersion: "1",
		Endpoints: discovery.Endpoints{API: "http://10.0.0.20:8080", Admin: "http://10.0.0.20:9090"},
		Contracts: []discovery.Contract{{Name: "management-api", Major: 1}}, State: discovery.StateReady,
	}
	if _, err := repository.Register(ctx, management); err != nil {
		t.Fatalf("register empty-scope descriptor: %v", err)
	}
	catalog, err := repository.Catalog(ctx)
	if err != nil || len(catalog) != 2 || len(catalog[0].Instances)+len(catalog[1].Instances) != 3 {
		t.Fatalf("catalog = %+v err = %v", catalog, err)
	}
}
