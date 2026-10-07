package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/discovery"
	"github.com/hutoroff/squash-bot/internal/observability"
	"github.com/prometheus/client_golang/prometheus"
)

func TestTelegramManagementDependencyCoversDirectAndPreDispatchRequests(t *testing.T) {
	metrics := observability.NewDependencyMetrics()
	resolver, err := discovery.NewStaticResolver(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := NewDiscoveredObserved(resolver, discovery.Requirement{ServiceKey: "management", Contract: "management-api", Major: 1}, "test", metrics)
	if _, err := client.GetVersion(context.Background()); !errors.Is(err, discovery.ErrNoEndpoint) {
		t.Fatalf("pre-dispatch error = %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	client = NewObserved(server.URL, "test", metrics)
	if _, err := client.GetNextGameForUser(context.Background(), 123); err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewRegistry()
	for _, collector := range metrics.Collectors() {
		registry.MustRegister(collector)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	foundError, found204 := false, false
	for _, family := range families {
		if family.GetName() != observability.MetricDependencyRequestsTotal {
			continue
		}
		for _, metric := range family.Metric {
			if metric.GetCounter().GetValue() != 1 {
				t.Fatal("request counted more than once")
			}
			for _, label := range metric.Label {
				if label.GetName() == "outcome" {
					foundError = foundError || label.GetValue() == "resolution_error"
					found204 = found204 || label.GetValue() == "204"
				}
			}
		}
	}
	if !foundError || !found204 {
		t.Fatalf("predispatch=%t direct HTTP=%t", foundError, found204)
	}
}

func TestDiscoveredClientResolvesManagementForEachRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertRequest(t, r, http.MethodGet, "/version")
		writeJSONResponse(t, w, http.StatusOK, map[string]string{"version": "1.0.0"})
	}))
	defer server.Close()
	descriptor := discovery.Descriptor{SchemaVersion: discovery.SchemaVersion, InstanceID: "0195f8db-7a6f-7c7b-8f71-0f6e07120436", ServiceKey: "management", ServiceKind: "management", Implementation: "squash-management", ReleaseVersion: "1", Endpoints: discovery.Endpoints{API: server.URL, Admin: server.URL}, Contracts: []discovery.Contract{{Name: "management-api", Major: 1}}, Capabilities: []discovery.Capability{{Name: "management.api", Contract: "management-api", Major: 1}}, State: discovery.StateReady}
	resolver, err := discovery.NewStaticResolver([]discovery.Descriptor{descriptor})
	if err != nil {
		t.Fatal(err)
	}
	client := NewDiscovered(resolver, discovery.Requirement{ServiceKey: "management", Contract: "management-api", Major: 1, RequiredCapabilities: []string{"management.api"}}, "test-api-secret")
	if version, err := client.GetVersion(context.Background()); err != nil || version != "1.0.0" {
		t.Fatalf("version=%q err=%v", version, err)
	}
}

func TestResolveUser_HTTPContract(t *testing.T) {
	var got map[string]any
	c := newHTTPTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assertRequest(t, r, http.MethodPost, "/api/v1/identities/resolve")
		decodeJSONBody(t, r, &got)
		writeJSONResponse(t, w, http.StatusOK, map[string]any{
			"user_id": 7001, "player_id": 901, "display_name": "@alice", "is_server_owner": true,
		})
	})

	resolved, err := c.ResolveUser(context.Background(), 123456, "alice", "Alice", "Example")
	if err != nil {
		t.Fatalf("ResolveUser: %v", err)
	}
	wantBody := map[string]any{
		"provider": "telegram", "external_id": "123456", "username": "alice",
		"first_name": "Alice", "last_name": "Example", "photo_url": "",
	}
	if !reflect.DeepEqual(got, wantBody) {
		t.Errorf("request body = %#v, want %#v", got, wantBody)
	}
	if resolved.UserID != 7001 || resolved.PlayerID == nil || *resolved.PlayerID != 901 ||
		resolved.DisplayName != "@alice" || !resolved.IsServerOwner {
		t.Errorf("resolved user = %+v, want canonical user/player/profile response", resolved)
	}
}

func TestCanonicalUserAndActorPropagation(t *testing.T) {
	t.Run("participation uses canonical user ID", func(t *testing.T) {
		var got map[string]any
		c := newHTTPTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			assertRequest(t, r, http.MethodPost, "/api/v1/games/44/join")
			decodeJSONBody(t, r, &got)
			writeJSONResponse(t, w, http.StatusOK, []any{})
		})

		if _, err := c.Join(context.Background(), 44, -10044, 7001); err != nil {
			t.Fatalf("Join: %v", err)
		}
		want := map[string]any{"user_id": float64(7001), "group_id": float64(-10044)}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Join body = %#v, want canonical identity body %#v", got, want)
		}
	})

	t.Run("game creation includes canonical actor", func(t *testing.T) {
		var got map[string]any
		c := newHTTPTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			assertRequest(t, r, http.MethodPost, "/api/v1/games")
			decodeJSONBody(t, r, &got)
			writeJSONResponse(t, w, http.StatusOK, map[string]any{"id": 44, "chat_id": -10044})
		})

		gameDate := time.Date(2026, 9, 12, 18, 30, 0, 0, time.FixedZone("UTC+2", 2*60*60))
		venueID := int64(81)
		game, err := c.CreateGame(context.Background(), -10044, gameDate, "1,2", "squash", &venueID, 7001, "@alice")
		if err != nil {
			t.Fatalf("CreateGame: %v", err)
		}
		if game.ID != 44 || game.ChatID != -10044 {
			t.Errorf("decoded game = %+v", game)
		}
		for key, want := range map[string]any{
			"chat_id": float64(-10044), "courts": "1,2", "sport": "squash",
			"venue_id": float64(81), "actor_user_id": float64(7001), "actor_display": "@alice",
		} {
			if got[key] != want {
				t.Errorf("CreateGame body[%q] = %#v, want %#v", key, got[key], want)
			}
		}
		if got["game_date"] != "2026-09-12T18:30:00+02:00" {
			t.Errorf("game_date = %#v, want local-offset timestamp", got["game_date"])
		}
	})
}

func TestPartialAndErrorStatusMappings(t *testing.T) {
	t.Run("partial court cancellation remains a successful response", func(t *testing.T) {
		var got map[string]any
		c := newHTTPTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			assertRequest(t, r, http.MethodPatch, "/api/v1/games/44/courts")
			decodeJSONBody(t, r, &got)
			writeJSONResponse(t, w, http.StatusOK, map[string]any{
				"canceled": []string{"1"},
				"failed":   []map[string]string{{"court": "2", "reason": "upstream timeout"}},
			})
		})

		canceled, failed, err := c.UpdateCourtsAndCancelBookings(context.Background(), 44, -10044, "3", "@alice", 7001, "telegram:callback-1")
		if err != nil {
			t.Fatalf("UpdateCourtsAndCancelBookings: %v", err)
		}
		if !reflect.DeepEqual(canceled, []string{"1"}) || !reflect.DeepEqual(failed, []CancelFailure{{Court: "2", Reason: "upstream timeout"}}) {
			t.Errorf("partial response = canceled %v failed %+v", canceled, failed)
		}
		if got["cancel_bookings"] != true || got["actor_user_id"] != float64(7001) || got["request_key"] != "telegram:callback-1" {
			t.Errorf("partial request body lost cancellation/actor/request-key fields: %#v", got)
		}
	})

	t.Run("publish conflict maps to sentinel", func(t *testing.T) {
		c := newHTTPTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			assertRequest(t, r, http.MethodPost, "/api/v1/games/44/publish")
			writeJSONResponse(t, w, http.StatusConflict, map[string]string{"error": "already published"})
		})
		if _, err := c.PublishGame(context.Background(), 44, 7001, "@alice"); !errors.Is(err, ErrAlreadyPublished) {
			t.Fatalf("PublishGame error = %v, want ErrAlreadyPublished", err)
		}
	})

	t.Run("generic status preserves typed code and message", func(t *testing.T) {
		c := newHTTPTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			assertRequest(t, r, http.MethodGet, "/api/v1/games/44")
			writeJSONResponse(t, w, http.StatusForbidden, map[string]string{"error": "group access denied"})
		})
		_, err := c.GetGameByID(context.Background(), 44)
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) {
			t.Fatalf("GetGameByID error = %v, want *HTTPError", err)
		}
		if httpErr.StatusCode != http.StatusForbidden || httpErr.Message != "group access denied" {
			t.Errorf("HTTPError = %+v", httpErr)
		}
	})
}

func newHTTPTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New(srv.URL, "test-api-secret")
}

func assertRequest(t *testing.T, r *http.Request, method, requestPath string) {
	t.Helper()
	if r.Method != method || r.URL.RequestURI() != requestPath {
		t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.RequestURI(), method, requestPath)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer test-api-secret" {
		t.Errorf("Authorization = %q, want bearer secret", got)
	}
	if method != http.MethodGet && r.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
	}
}

func decodeJSONBody(t *testing.T, r *http.Request, dst any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
}

func writeJSONResponse(t *testing.T, w http.ResponseWriter, status int, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
