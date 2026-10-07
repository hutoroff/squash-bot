package discovery

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
)

// MonitoredService is an operator-owned expected singleton binding.
type MonitoredService struct {
	ServiceKey   string            `json:"service_key"`
	Contract     string            `json:"contract"`
	Major        int               `json:"major"`
	Capabilities []string          `json:"capabilities,omitempty"`
	Scope        map[string]string `json:"scope,omitempty"`
}

func ParseMonitoredServices(raw string) ([]MonitoredService, error) {
	var services []MonitoredService
	if err := json.Unmarshal([]byte(raw), &services); err != nil || len(services) == 0 || len(services) > 32 {
		return nil, fmt.Errorf("MONITORED_SERVICE_KEYS must be a bounded JSON service inventory")
	}
	seen := map[string]bool{}
	for i := range services {
		sort.Strings(services[i].Capabilities)
		services[i].Capabilities = slices.Compact(services[i].Capabilities)
		requirement := Requirement{ServiceKey: services[i].ServiceKey, Contract: services[i].Contract, Major: services[i].Major, RequiredCapabilities: services[i].Capabilities, Scope: services[i].Scope}
		if err := ValidateRequirement(requirement); err != nil {
			return nil, fmt.Errorf("invalid monitored service %q: %w", services[i].ServiceKey, err)
		}
		if seen[services[i].ServiceKey] {
			return nil, fmt.Errorf("duplicate monitored service key %q", services[i].ServiceKey)
		}
		seen[services[i].ServiceKey] = true
	}
	return services, nil
}
