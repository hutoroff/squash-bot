package discovery

import (
	"context"
	"fmt"
)

type StaticResolver struct {
	descriptors map[string][]Descriptor
}

func NewStaticResolver(descriptors []Descriptor) (*StaticResolver, error) {
	resolver := &StaticResolver{descriptors: make(map[string][]Descriptor)}
	for _, descriptor := range descriptors {
		if err := descriptor.Validate(); err != nil {
			return nil, fmt.Errorf("invalid static discovery descriptor: %w", err)
		}
		resolver.descriptors[descriptor.ServiceKey] = append(resolver.descriptors[descriptor.ServiceKey], descriptor)
	}
	return resolver, nil
}

func (r *StaticResolver) Resolve(_ context.Context, requirement Requirement) ([]ResolvedEndpoint, error) {
	if err := validateRequirement(requirement); err != nil {
		return nil, err
	}
	descriptors := r.descriptors[requirement.ServiceKey]
	if len(descriptors) == 0 {
		return nil, ErrNoEndpoint
	}
	var endpoints []ResolvedEndpoint
	compatibleFound := false
	for _, descriptor := range descriptors {
		compatibleFound = compatibleFound || compatible(descriptor, requirement)
		if satisfies(descriptor, requirement) {
			endpoints = append(endpoints, ResolvedEndpoint{InstanceID: descriptor.InstanceID, API: descriptor.Endpoints.API, Admin: descriptor.Endpoints.Admin, Descriptor: descriptor})
		}
	}
	if len(endpoints) == 0 {
		if compatibleFound {
			return nil, ErrNoEndpoint
		}
		return nil, ErrIncompatible
	}
	return endpoints, nil
}
