package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/hutoroff/squash-bot/internal/discovery"
)

var ErrRegistryDescriptorRejected = errors.New("service registry descriptor rejected")

type ServiceRegistryRepository interface {
	Register(context.Context, discovery.Descriptor) (*discovery.Lease, error)
	Heartbeat(context.Context, string, discovery.InstanceState) (*discovery.Lease, error)
	Drain(context.Context, string) error
	Deregister(context.Context, string) error
	Resolve(context.Context, discovery.Requirement) ([]discovery.ResolvedEndpoint, error)
	Catalog(context.Context) ([]discovery.CatalogEntry, error)
}

type serviceRegistryMonitoringRepository interface {
	MonitoringInstances(context.Context) ([]discovery.MonitoringInstance, error)
}

type ServiceRegistryService struct {
	repository ServiceRegistryRepository
	allowed    []*net.IPNet
}

func NewServiceRegistryService(repository ServiceRegistryRepository, allowedCIDRs string) (*ServiceRegistryService, error) {
	if repository == nil {
		return nil, fmt.Errorf("service registry repository is required")
	}
	service := &ServiceRegistryService{repository: repository}
	for _, raw := range strings.Split(allowedCIDRs, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid discovery allowed CIDR %q", raw)
		}
		service.allowed = append(service.allowed, network)
	}
	if len(service.allowed) == 0 {
		return nil, fmt.Errorf("at least one discovery allowed CIDR is required")
	}
	return service, nil
}

func (s *ServiceRegistryService) Register(ctx context.Context, descriptor discovery.Descriptor) (*discovery.Lease, error) {
	if err := descriptor.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRegistryDescriptorRejected, err)
	}
	if err := s.validateEndpoints(descriptor); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRegistryDescriptorRejected, err)
	}
	return s.repository.Register(ctx, descriptor)
}
func (s *ServiceRegistryService) Heartbeat(ctx context.Context, instanceID string, state discovery.InstanceState) (*discovery.Lease, error) {
	return s.repository.Heartbeat(ctx, instanceID, state)
}
func (s *ServiceRegistryService) Drain(ctx context.Context, instanceID string) error {
	return s.repository.Drain(ctx, instanceID)
}
func (s *ServiceRegistryService) Deregister(ctx context.Context, instanceID string) error {
	return s.repository.Deregister(ctx, instanceID)
}
func (s *ServiceRegistryService) Resolve(ctx context.Context, requirement discovery.Requirement) ([]discovery.ResolvedEndpoint, error) {
	endpoints, err := s.repository.Resolve(ctx, requirement)
	if err != nil || len(endpoints) > 0 {
		return endpoints, err
	}
	catalog, err := s.repository.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	matchingBinding := false
	for _, entry := range catalog {
		descriptor := discovery.Descriptor{ServiceKey: entry.ServiceKey, Scope: entry.Scope, Contracts: entry.Contracts, Capabilities: entry.Capabilities}
		if descriptor.ServiceKey != requirement.ServiceKey || !sameDiscoveryScope(descriptor.Scope, requirement.Scope) {
			continue
		}
		matchingBinding = true
		if descriptor.Compatible(requirement) {
			return endpoints, nil
		}
	}
	if matchingBinding {
		return nil, discovery.ErrIncompatible
	}
	return endpoints, nil
}
func (s *ServiceRegistryService) Catalog(ctx context.Context) ([]discovery.CatalogEntry, error) {
	return s.repository.Catalog(ctx)
}

// PrometheusTargets projects active registry memberships to Prometheus HTTP SD.
// Targets use the registered admin address only; descriptor-controlled paths,
// scope, and release values are intentionally not exported as labels.
func (s *ServiceRegistryService) PrometheusTargets(ctx context.Context) ([]discovery.PrometheusTarget, error) {
	repository, ok := s.repository.(serviceRegistryMonitoringRepository)
	if !ok {
		return nil, fmt.Errorf("service registry monitoring snapshot unavailable")
	}
	instances, err := repository.MonitoringInstances(ctx)
	if err != nil {
		return nil, err
	}
	targets := make([]discovery.PrometheusTarget, 0, len(instances))
	for _, instance := range instances {
		parsed, err := url.Parse(instance.Descriptor.Endpoints.Admin)
		if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("invalid monitoring admin endpoint for %s", instance.Descriptor.ServiceKey)
		}
		if _, _, err := net.SplitHostPort(parsed.Host); err != nil {
			return nil, fmt.Errorf("invalid monitoring admin endpoint for %s", instance.Descriptor.ServiceKey)
		}
		targets = append(targets, discovery.PrometheusTarget{
			Targets: []string{parsed.Host},
			Labels: map[string]string{
				"service_key":  instance.Descriptor.ServiceKey,
				"service_kind": instance.Descriptor.ServiceKind,
				"state":        string(instance.State),
			},
		})
	}
	return targets, nil
}

func sameDiscoveryScope(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func (s *ServiceRegistryService) validateEndpoints(descriptor discovery.Descriptor) error {
	allowedAPIPorts := map[string]map[int]bool{
		"management": {8080: true}, "booking": {8081: true}, "web": {8082: true}, "telegram": {8083: true},
	}
	allowedAdminPorts := map[string]map[int]bool{
		"management": {9090: true}, "booking": {9091: true}, "web": {9090: true}, "telegram": {9090: true},
	}
	if descriptor.Endpoints.API == "" && descriptor.ServiceKind != "telegram" {
		return fmt.Errorf("API endpoint is required for service kind")
	}
	if descriptor.Endpoints.API != "" {
		if err := s.validateEndpoint(descriptor.Endpoints.API, allowedAPIPorts[descriptor.ServiceKind]); err != nil {
			return fmt.Errorf("invalid advertised API endpoint: %w", err)
		}
	}
	if err := s.validateEndpoint(descriptor.Endpoints.Admin, allowedAdminPorts[descriptor.ServiceKind]); err != nil {
		return fmt.Errorf("invalid advertised admin endpoint: %w", err)
	}
	return nil
}

func (s *ServiceRegistryService) validateEndpoint(raw string, ports map[int]bool) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return fmt.Errorf("host must be a non-loopback IP")
	}
	allowed := false
	for _, network := range s.allowed {
		allowed = allowed || network.Contains(ip)
	}
	if !allowed {
		return fmt.Errorf("host is outside allowed networks")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || !ports[port] {
		return fmt.Errorf("port is not allowed for service kind")
	}
	return nil
}
