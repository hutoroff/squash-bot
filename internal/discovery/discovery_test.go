package discovery

import (
	"context"
	"errors"
	"testing"
)

func testDescriptor() Descriptor {
	return Descriptor{
		SchemaVersion: SchemaVersion, InstanceID: "0195f8db-7a6f-7c7b-8f71-0f6e0712042e",
		ServiceKey: "booking.eversports.primary", ServiceKind: "booking", Implementation: "eversports",
		ReleaseVersion: "2.0.0", Scope: map[string]string{"facility_uuid": "facility-1", "sport": "squash"},
		Endpoints:    Endpoints{API: "http://10.0.0.2:8081", Admin: "http://10.0.0.2:9090"},
		Contracts:    []Contract{{Name: "eversports-booking", Major: 2, Minor: 0}},
		Capabilities: []Capability{{Name: "booking.create", Contract: "eversports-booking", Major: 2}, {Name: "booking.operations.get", Contract: "eversports-booking", Major: 2}},
		State:        StateReady,
	}
}

func TestDescriptorValidationRejectsUnsafeOrUnboundDeclarations(t *testing.T) {
	valid := testDescriptor()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid descriptor: %v", err)
	}
	tests := []Descriptor{valid, valid, valid, valid}
	tests[0].InstanceID = "hostname"
	tests[1].Endpoints.API = "https://user:password@example.test/path"
	tests[2].Capabilities = append(tests[2].Capabilities, Capability{Name: "booking.cancel", Contract: "unknown", Major: 1})
	tests[3].State = "unknown"
	for index, descriptor := range tests {
		if err := descriptor.Validate(); err == nil {
			t.Fatalf("invalid descriptor %d accepted", index)
		}
	}
}

func TestStaticResolverRequiresExactScopeContractAndCapabilities(t *testing.T) {
	resolver, err := NewStaticResolver([]Descriptor{testDescriptor()})
	if err != nil {
		t.Fatal(err)
	}
	requirement := Requirement{
		ServiceKey: "booking.eversports.primary", Contract: "eversports-booking", Major: 2,
		RequiredCapabilities: []string{"booking.create", "booking.operations.get"},
		Scope:                map[string]string{"facility_uuid": "facility-1", "sport": "squash"},
	}
	endpoints, err := resolver.Resolve(context.Background(), requirement)
	if err != nil || len(endpoints) != 1 || endpoints[0].API != "http://10.0.0.2:8081" {
		t.Fatalf("resolved endpoints = %+v err = %v", endpoints, err)
	}
	mismatch := requirement
	mismatch.Scope = map[string]string{"facility_uuid": "facility-2", "sport": "squash"}
	if _, err := resolver.Resolve(context.Background(), mismatch); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("scope mismatch error = %v", err)
	}
	missing := requirement
	missing.ServiceKey = "missing"
	if _, err := resolver.Resolve(context.Background(), missing); !errors.Is(err, ErrNoEndpoint) {
		t.Fatalf("missing error = %v", err)
	}
	notReady := testDescriptor()
	notReady.State = StateNotReady
	notReadyResolver, err := NewStaticResolver([]Descriptor{notReady})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := notReadyResolver.Resolve(context.Background(), requirement); !errors.Is(err, ErrNoEndpoint) {
		t.Fatalf("not-ready error = %v", err)
	}
}
