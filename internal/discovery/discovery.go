package discovery

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
)

const SchemaVersion = 1

const (
	StateStarting InstanceState = "starting"
	StateReady    InstanceState = "ready"
	StateNotReady InstanceState = "not_ready"
	StateDraining InstanceState = "draining"
)

var (
	ErrUnavailable  = errors.New("service discovery unavailable")
	ErrNoEndpoint   = errors.New("no eligible service endpoint")
	ErrIncompatible = errors.New("service contract is incompatible")
	identifierRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
)

type InstanceState string

type Contract struct {
	Name  string `json:"name"`
	Major int    `json:"major"`
	Minor int    `json:"minor"`
}

type Capability struct {
	Name     string `json:"name"`
	Contract string `json:"contract"`
	Major    int    `json:"major"`
}

type Endpoints struct {
	API   string `json:"api"`
	Admin string `json:"admin"`
}

type Descriptor struct {
	SchemaVersion  int               `json:"schema_version"`
	InstanceID     string            `json:"instance_id"`
	ServiceKey     string            `json:"service_key"`
	ServiceKind    string            `json:"service_kind"`
	Implementation string            `json:"implementation"`
	ReleaseVersion string            `json:"release_version"`
	Scope          map[string]string `json:"scope,omitempty"`
	Endpoints      Endpoints         `json:"endpoints"`
	Contracts      []Contract        `json:"contracts"`
	Capabilities   []Capability      `json:"capabilities"`
	State          InstanceState     `json:"state"`
}

type Requirement struct {
	ServiceKey           string
	Contract             string
	Major                int
	MinimumMinor         int
	RequiredCapabilities []string
	Scope                map[string]string
}

type ResolvedEndpoint struct {
	InstanceID string
	API        string
	Admin      string
	Descriptor Descriptor
}

type Resolver interface {
	Resolve(context.Context, Requirement) ([]ResolvedEndpoint, error)
}

type Registrar interface {
	Register(context.Context, Descriptor) error
	Heartbeat(context.Context, string, InstanceState) error
	Drain(context.Context, string) error
	Deregister(context.Context, string) error
}

func (d Descriptor) Validate() error {
	if d.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported discovery descriptor schema %d", d.SchemaVersion)
	}
	if _, err := uuid.Parse(d.InstanceID); err != nil {
		return fmt.Errorf("instance_id must be a UUID: %w", err)
	}
	for name, value := range map[string]string{"service_key": d.ServiceKey, "service_kind": d.ServiceKind, "implementation": d.Implementation} {
		if !identifierRE.MatchString(value) {
			return fmt.Errorf("%s is invalid", name)
		}
	}
	if d.ReleaseVersion == "" || len(d.ReleaseVersion) > 64 || strings.ContainsAny(d.ReleaseVersion, "\r\n\x00") {
		return fmt.Errorf("release_version is invalid")
	}
	if d.State != StateStarting && d.State != StateReady && d.State != StateNotReady && d.State != StateDraining {
		return fmt.Errorf("instance state is invalid")
	}
	if len(d.Scope) > 16 || len(d.Contracts) == 0 || len(d.Contracts) > 16 || len(d.Capabilities) > 64 {
		return fmt.Errorf("descriptor collections exceed bounds")
	}
	for key, value := range d.Scope {
		if !identifierRE.MatchString(key) || value == "" || len(value) > 256 || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("descriptor scope is invalid")
		}
	}
	if err := validateEndpoint(d.Endpoints.API, false); err != nil {
		return fmt.Errorf("api endpoint: %w", err)
	}
	if err := validateEndpoint(d.Endpoints.Admin, true); err != nil {
		return fmt.Errorf("admin endpoint: %w", err)
	}
	seenContracts := make(map[string]bool)
	for _, contract := range d.Contracts {
		if !identifierRE.MatchString(contract.Name) || contract.Major < 1 || contract.Minor < 0 {
			return fmt.Errorf("contract declaration is invalid")
		}
		key := fmt.Sprintf("%s/%d", contract.Name, contract.Major)
		if seenContracts[key] {
			return fmt.Errorf("contract declaration is duplicated")
		}
		seenContracts[key] = true
	}
	seenCapabilities := make(map[string]bool)
	for _, capability := range d.Capabilities {
		if !identifierRE.MatchString(capability.Name) || !identifierRE.MatchString(capability.Contract) || capability.Major < 1 || !seenContracts[fmt.Sprintf("%s/%d", capability.Contract, capability.Major)] {
			return fmt.Errorf("capability declaration is invalid")
		}
		key := fmt.Sprintf("%s/%s/%d", capability.Name, capability.Contract, capability.Major)
		if seenCapabilities[key] {
			return fmt.Errorf("capability declaration is duplicated")
		}
		seenCapabilities[key] = true
	}
	return nil
}

func validateEndpoint(raw string, required bool) error {
	if raw == "" && !required {
		return nil
	}
	if raw == "" || len(raw) > 512 {
		return fmt.Errorf("endpoint is required and bounded")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("endpoint must be an HTTP origin")
	}
	return nil
}

func satisfies(d Descriptor, requirement Requirement) bool {
	return d.State == StateReady && compatible(d, requirement)
}

func compatible(d Descriptor, requirement Requirement) bool {
	if d.ServiceKey != requirement.ServiceKey || !equalScope(d.Scope, requirement.Scope) {
		return false
	}
	contractFound := false
	for _, contract := range d.Contracts {
		if contract.Name == requirement.Contract && contract.Major == requirement.Major && contract.Minor >= requirement.MinimumMinor {
			contractFound = true
			break
		}
	}
	if !contractFound {
		return false
	}
	for _, required := range requirement.RequiredCapabilities {
		if !slices.ContainsFunc(d.Capabilities, func(capability Capability) bool {
			return capability.Name == required && capability.Contract == requirement.Contract && capability.Major == requirement.Major
		}) {
			return false
		}
	}
	return true
}

func validateRequirement(requirement Requirement) error {
	if !identifierRE.MatchString(requirement.ServiceKey) || !identifierRE.MatchString(requirement.Contract) || requirement.Major < 1 || requirement.MinimumMinor < 0 || len(requirement.RequiredCapabilities) > 64 || len(requirement.Scope) > 16 {
		return fmt.Errorf("invalid discovery requirement")
	}
	for _, capability := range requirement.RequiredCapabilities {
		if !identifierRE.MatchString(capability) {
			return fmt.Errorf("invalid discovery requirement capability")
		}
	}
	for key, value := range requirement.Scope {
		if !identifierRE.MatchString(key) || value == "" || len(value) > 256 || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("invalid discovery requirement scope")
		}
	}
	return nil
}

func equalScope(left, right map[string]string) bool {
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
