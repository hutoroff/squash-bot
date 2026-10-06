package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegistryResolverCachesOnlyWithinLeaseAndReplacesWithEmptySnapshot(t *testing.T) {
	baseTime := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	var mode atomic.Int32
	var calls atomic.Int32
	descriptor := testDescriptor()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("s", 32) {
			t.Errorf("missing auth")
		}
		if mode.Load() == 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if mode.Load() == 1 {
			_ = json.NewEncoder(w).Encode([]ResolvedEndpoint{})
			return
		}
		_ = json.NewEncoder(w).Encode([]ResolvedEndpoint{{InstanceID: descriptor.InstanceID, API: descriptor.Endpoints.API, Admin: descriptor.Endpoints.Admin, Descriptor: descriptor, RegistryTime: baseTime, LeaseExpiresAt: baseTime.Add(35 * time.Second)}})
	}))
	defer server.Close()
	resolver, err := NewRegistryResolver(server.URL, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	now := baseTime
	resolver.now = func() time.Time { return now }
	requirement := Requirement{ServiceKey: descriptor.ServiceKey, Contract: "eversports-booking", Major: 2, RequiredCapabilities: []string{"booking.create"}, Scope: descriptor.Scope}
	if endpoints, err := resolver.Resolve(context.Background(), requirement); err != nil || len(endpoints) != 1 {
		t.Fatalf("first resolve = %+v %v", endpoints, err)
	}
	now = now.Add(2 * time.Second)
	if _, err := resolver.Resolve(context.Background(), requirement); err != nil || calls.Load() != 1 {
		t.Fatalf("cached resolve calls=%d err=%v", calls.Load(), err)
	}
	resolver.Invalidate(requirement)
	if _, err := resolver.Resolve(context.Background(), requirement); err != nil || calls.Load() != 2 {
		t.Fatalf("invalidated resolve calls=%d err=%v", calls.Load(), err)
	}
	now = now.Add(5 * time.Second)
	mode.Store(2)
	if endpoints, err := resolver.Resolve(context.Background(), requirement); err != nil || len(endpoints) != 1 {
		t.Fatalf("failed refresh cache = %+v %v", endpoints, err)
	}
	now = now.Add(40 * time.Second)
	if _, err := resolver.Resolve(context.Background(), requirement); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expired cache error = %v", err)
	}

	mode.Store(0)
	now = baseTime
	resolver.cache = make(map[string]resolverSnapshot)
	if _, err := resolver.Resolve(context.Background(), requirement); err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Second)
	mode.Store(1)
	if endpoints, err := resolver.Resolve(context.Background(), requirement); err != nil || len(endpoints) != 0 {
		t.Fatalf("empty replacement = %+v %v", endpoints, err)
	}
}

func TestRegistryResolverCoalescesConcurrentRefreshForOneSelector(t *testing.T) {
	descriptor := testDescriptor()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		now := time.Now()
		_ = json.NewEncoder(w).Encode([]ResolvedEndpoint{{
			InstanceID: descriptor.InstanceID, API: descriptor.Endpoints.API, Admin: descriptor.Endpoints.Admin,
			Descriptor: descriptor, RegistryTime: now, LeaseExpiresAt: now.Add(35 * time.Second),
		}})
	}))
	defer server.Close()
	resolver, err := NewRegistryResolver(server.URL, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	requirement := Requirement{ServiceKey: descriptor.ServiceKey, Contract: "eversports-booking", Major: 2, RequiredCapabilities: []string{"booking.create"}, Scope: descriptor.Scope}
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, resolveErr := resolver.Resolve(context.Background(), requirement)
			results <- resolveErr
		}()
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	// The second caller must wait on the first refresh rather than issuing a
	// second request while the registry response is still unavailable.
	time.Sleep(25 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("concurrent refresh calls = %d, want 1", got)
	}
	close(release)
	for range 2 {
		select {
		case resolveErr := <-results:
			if resolveErr != nil {
				t.Fatalf("Resolve() error = %v", resolveErr)
			}
		case <-time.After(time.Second):
			t.Fatal("coalesced resolve did not finish")
		}
	}
}

func TestRegistryResolverRefreshesIndependentSelectorsConcurrently(t *testing.T) {
	first := testDescriptor()
	second := testDescriptor()
	second.ServiceKey = "booking.secondary"
	firstStarted := make(chan struct{})
	secondRequested := make(chan struct{})
	releaseFirst := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		descriptor := first
		if r.URL.Query().Get("service_key") == second.ServiceKey {
			descriptor = second
			close(secondRequested)
		} else {
			close(firstStarted)
			<-releaseFirst
		}
		now := time.Now()
		_ = json.NewEncoder(w).Encode([]ResolvedEndpoint{{
			InstanceID: descriptor.InstanceID, API: descriptor.Endpoints.API, Admin: descriptor.Endpoints.Admin,
			Descriptor: descriptor, RegistryTime: now, LeaseExpiresAt: now.Add(35 * time.Second),
		}})
	}))
	defer server.Close()
	resolver, err := NewRegistryResolver(server.URL, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	firstRequirement := Requirement{ServiceKey: first.ServiceKey, Contract: "eversports-booking", Major: 2, RequiredCapabilities: []string{"booking.create"}, Scope: first.Scope}
	secondRequirement := firstRequirement
	secondRequirement.ServiceKey = second.ServiceKey

	firstDone := make(chan error, 1)
	go func() {
		_, resolveErr := resolver.Resolve(context.Background(), firstRequirement)
		firstDone <- resolveErr
	}()
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first refresh did not start")
	}
	secondDone := make(chan error, 1)
	go func() {
		_, resolveErr := resolver.Resolve(context.Background(), secondRequirement)
		secondDone <- resolveErr
	}()
	select {
	case <-secondRequested:
	case <-time.After(time.Second):
		t.Fatal("independent selector was blocked by the first refresh")
	}
	close(releaseFirst)
	for name, done := range map[string]chan error{"first": firstDone, "second": secondDone} {
		select {
		case resolveErr := <-done:
			if resolveErr != nil {
				t.Fatalf("%s resolve error = %v", name, resolveErr)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s resolve did not finish", name)
		}
	}
}

func TestRegistryResolverReturnsTypedIncompatibleError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusConflict) }))
	defer server.Close()
	resolver, err := NewRegistryResolver(server.URL, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.Resolve(context.Background(), Requirement{ServiceKey: "management", Contract: "management-api", Major: 2})
	if !errors.Is(err, ErrIncompatible) {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveOneRejectsMultipleActiveInstances(t *testing.T) {
	descriptor := testDescriptor()
	second := descriptor
	second.InstanceID = "0195f8db-7a6f-7c7b-8f71-0f6e07120430"
	resolver, err := NewStaticResolver([]Descriptor{descriptor, second})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ResolveOne(context.Background(), resolver, Requirement{ServiceKey: descriptor.ServiceKey, Contract: "eversports-booking", Major: 2, Scope: descriptor.Scope})
	if !errors.Is(err, ErrMultipleActive) {
		t.Fatalf("error = %v", err)
	}
}
