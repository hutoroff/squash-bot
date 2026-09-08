package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"
)

const (
	resolverRefreshInterval = 5 * time.Second
	resolverFailureInterval = time.Second
	maxResolverSnapshots    = 64
	maxResolvedEndpoints    = 256
)

type cachedEndpoint struct {
	endpoint ResolvedEndpoint
	expires  time.Time
}

type resolverSnapshot struct {
	endpoints   []cachedEndpoint
	nextRefresh time.Time
	lastErr     error
}

type RegistryResolver struct {
	baseURL string
	secret  string
	client  *http.Client
	mu      sync.Mutex
	cache   map[string]resolverSnapshot
	now     func() time.Time
}

func NewRegistryResolver(baseURL, secret string) (*RegistryResolver, error) {
	registrar, err := NewHTTPRegistrar(baseURL, secret)
	if err != nil {
		return nil, err
	}
	return &RegistryResolver{baseURL: registrar.baseURL, secret: secret, client: registrar.client, cache: make(map[string]resolverSnapshot), now: time.Now}, nil
}

func (r *RegistryResolver) Resolve(ctx context.Context, requirement Requirement) ([]ResolvedEndpoint, error) {
	if err := ValidateRequirement(requirement); err != nil {
		return nil, err
	}
	key := requirementKey(requirement)
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	cached := liveCached(r.cache[key], now)
	if snapshot, ok := r.cache[key]; ok && now.Before(snapshot.nextRefresh) {
		if len(cached) == 0 && snapshot.lastErr != nil {
			return nil, snapshot.lastErr
		}
		return cached, nil
	}
	started := now
	resolved, err := r.fetch(ctx, requirement)
	finished := r.now()
	if err != nil {
		snapshot := r.cache[key]
		snapshot.nextRefresh = finished.Add(resolverFailureInterval)
		snapshot.lastErr = err
		r.store(key, snapshot)
		if stillValid := liveCached(snapshot, finished); len(stillValid) > 0 {
			return stillValid, nil
		}
		return nil, err
	}
	snapshot := resolverSnapshot{nextRefresh: finished.Add(resolverRefreshInterval)}
	for _, endpoint := range resolved {
		if !endpoint.Descriptor.Satisfies(requirement) || endpoint.RegistryTime.IsZero() || !endpoint.LeaseExpiresAt.After(endpoint.RegistryTime) {
			return nil, fmt.Errorf("%w: registry returned invalid endpoint", ErrUnavailable)
		}
		remaining := endpoint.LeaseExpiresAt.Sub(endpoint.RegistryTime) - finished.Sub(started)
		if remaining <= 0 {
			continue
		}
		snapshot.endpoints = append(snapshot.endpoints, cachedEndpoint{endpoint: endpoint, expires: finished.Add(remaining)})
	}
	r.store(key, snapshot)
	return liveCached(snapshot, finished), nil
}

func (r *RegistryResolver) Invalidate(requirement Requirement) {
	key := requirementKey(requirement)
	r.mu.Lock()
	snapshot, ok := r.cache[key]
	if ok {
		snapshot.nextRefresh = time.Time{}
		r.cache[key] = snapshot
	}
	r.mu.Unlock()
}

func (r *RegistryResolver) fetch(ctx context.Context, requirement Requirement) ([]ResolvedEndpoint, error) {
	query := url.Values{}
	query.Set("service_key", requirement.ServiceKey)
	query.Set("contract", requirement.Contract)
	query.Set("major", strconv.Itoa(requirement.Major))
	if requirement.MinimumMinor > 0 {
		query.Set("minimum_minor", strconv.Itoa(requirement.MinimumMinor))
	}
	for _, capability := range requirement.RequiredCapabilities {
		query.Add("capability", capability)
	}
	keys := make([]string, 0, len(requirement.Scope))
	for key := range requirement.Scope {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		query.Set("scope."+key, requirement.Scope[key])
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/internal/discovery/v1/resolve?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+r.secret)
	request.Header.Set("Accept", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict {
		return nil, ErrIncompatible
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: registry status %d", ErrUnavailable, response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxControlResponseBytes+1))
	if err != nil || len(raw) > maxControlResponseBytes {
		return nil, fmt.Errorf("%w: invalid bounded response", ErrUnavailable)
	}
	var endpoints []ResolvedEndpoint
	if err := json.Unmarshal(raw, &endpoints); err != nil {
		return nil, fmt.Errorf("%w: malformed response", ErrUnavailable)
	}
	if endpoints == nil {
		endpoints = []ResolvedEndpoint{}
	}
	if len(endpoints) > maxResolvedEndpoints {
		return nil, fmt.Errorf("%w: too many endpoints", ErrUnavailable)
	}
	return endpoints, nil
}

func (r *RegistryResolver) store(key string, snapshot resolverSnapshot) {
	if _, exists := r.cache[key]; !exists && len(r.cache) >= maxResolverSnapshots {
		var oldestKey string
		var oldest time.Time
		for candidate, current := range r.cache {
			if oldestKey == "" || current.nextRefresh.Before(oldest) {
				oldestKey, oldest = candidate, current.nextRefresh
			}
		}
		delete(r.cache, oldestKey)
	}
	r.cache[key] = snapshot
}

func liveCached(snapshot resolverSnapshot, now time.Time) []ResolvedEndpoint {
	result := make([]ResolvedEndpoint, 0, len(snapshot.endpoints))
	for _, cached := range snapshot.endpoints {
		if now.Before(cached.expires) {
			result = append(result, cached.endpoint)
		}
	}
	return result
}

func requirementKey(requirement Requirement) string {
	raw, _ := json.Marshal(requirement)
	return string(raw)
}
