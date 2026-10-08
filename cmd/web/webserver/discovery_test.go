package webserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/discovery"
	"github.com/hutoroff/squash-bot/internal/observability"
	"github.com/prometheus/client_golang/prometheus"
)

func TestWebManagementDependencyCountsPreDispatchFailure(t *testing.T) {
	resolver, err := discovery.NewStaticResolver(nil)
	if err != nil {
		t.Fatal(err)
	}
	metrics := observability.NewDependencyMetrics()
	target := NewDiscoveredManagementTarget(resolver, discovery.Requirement{ServiceKey: "management", Contract: "management-api", Major: 1})
	target.SetDependencyMetrics(metrics)
	request, err := http.NewRequest(http.MethodPost, target.requestBaseURL()+"/api/v1/games/123?private=secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newTargetHTTPClient(target, time.Second).Do(request); err == nil {
		t.Fatal("expected no endpoint")
	}
	registry := prometheus.NewRegistry()
	for _, collector := range metrics.Collectors() {
		registry.MustRegister(collector)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, family := range families {
		if family.GetName() != observability.MetricDependencyRequestsTotal {
			continue
		}
		for _, metric := range family.Metric {
			if metric.GetCounter().GetValue() != 1 {
				t.Fatalf("pre-dispatch count=%v", metric.GetCounter().GetValue())
			}
			for _, label := range metric.Label {
				if label.GetName() == "outcome" && label.GetValue() == "resolution_error" {
					found = true
				}
				if label.GetValue() == "/api/v1/games/123" {
					t.Fatal("raw path label")
				}
			}
		}
	}
	if !found {
		t.Fatal("pre-dispatch failure not recorded")
	}
}

type switchableManagementResolver struct{ endpoint atomic.Value }

func (s *switchableManagementResolver) Resolve(_ context.Context, _ discovery.Requirement) ([]discovery.ResolvedEndpoint, error) {
	address := s.endpoint.Load().(string)
	if address == "" {
		return []discovery.ResolvedEndpoint{}, nil
	}
	return []discovery.ResolvedEndpoint{{API: address}}, nil
}

func TestWebSessionSurvivesManagementTargetReplacementAndOutage(t *testing.T) {
	secret := strings.Repeat("j", 32)
	token, err := issueJWT(secret, JWTClaims{UserID: 42, Exp: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	endpoint := func(label string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			if r.URL.Path != "/api/v1/users" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("m", 32) || r.Header.Get("X-Caller-User-Id") != "42" || r.Header.Get("Cookie") != "" {
				t.Errorf("unsafe redirected request: path=%q actor=%q cookie=%q", r.URL.Path, r.Header.Get("X-Caller-User-Id"), r.Header.Get("Cookie"))
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(label))
		}))
	}
	first, second := endpoint("first"), endpoint("second")
	defer first.Close()
	defer second.Close()
	resolver := &switchableManagementResolver{}
	resolver.endpoint.Store(first.URL)
	target := NewDiscoveredManagementTarget(resolver, discovery.Requirement{ServiceKey: "management", Contract: "management-api", Major: 1})
	auth := NewAuthHandlerWithTarget("bot", "bot", secret, target, strings.Repeat("m", 32), slog.New(slog.NewTextHandler(io.Discard, nil)))
	handler := NewUsersHandlerWithTarget(auth, target, strings.Repeat("m", 32))
	call := func() (int, string, string) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/users", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		w := httptest.NewRecorder()
		handler.handleListUsers(w, r)
		return w.Code, w.Body.String(), w.Header().Get("Set-Cookie")
	}
	if status, body, _ := call(); status != 200 || body != "first" {
		t.Fatalf("first %d %q", status, body)
	}
	resolver.endpoint.Store("")
	if status, _, cookie := call(); status != http.StatusBadGateway || cookie != "" {
		t.Fatalf("outage status=%d cookie=%q", status, cookie)
	}
	resolver.endpoint.Store(second.URL)
	if status, body, cookie := call(); status != 200 || body != "second" || cookie != "" || requests.Load() != 2 {
		t.Fatalf("replacement status=%d body=%q cookie=%q calls=%d", status, body, cookie, requests.Load())
	}
}

func TestDiscoveredManagementTargetRewritesBusinessRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/version" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	descriptor := discovery.Descriptor{SchemaVersion: discovery.SchemaVersion, InstanceID: "0195f8db-7a6f-7c7b-8f71-0f6e07120437", ServiceKey: "management", ServiceKind: "management", Implementation: "squash-management", ReleaseVersion: "1", Endpoints: discovery.Endpoints{API: server.URL, Admin: server.URL}, Contracts: []discovery.Contract{{Name: "management-api", Major: 1}}, Capabilities: []discovery.Capability{{Name: "management.api", Contract: "management-api", Major: 1}}, State: discovery.StateReady}
	resolver, err := discovery.NewStaticResolver([]discovery.Descriptor{descriptor})
	if err != nil {
		t.Fatal(err)
	}
	target := NewDiscoveredManagementTarget(resolver, discovery.Requirement{ServiceKey: "management", Contract: "management-api", Major: 1, RequiredCapabilities: []string{"management.api"}})
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target.requestBaseURL()+"/api/v1/version", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := newTargetHTTPClient(target, time.Second).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", response.StatusCode)
	}
}
