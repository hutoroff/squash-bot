package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// capturedRequest holds the headers the httpBookingClient sent to the server.
type capturedRequest struct {
	email    string
	password string
}

// capturingServer returns a test server that records the credential headers from each
// request and responds with an empty JSON array. The caller reads from the returned channel.
func capturingServer(t *testing.T) (*httptest.Server, *capturedRequest) {
	t.Helper()
	got := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.email = r.Header.Get("X-Eversports-Email")
		got.password = r.Header.Get("X-Eversports-Password")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]struct{}{}) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// ── ListCourts ────────────────────────────────────────────────────────────────

func TestHTTPBookingClient_ListCourts_ForwardsCredentialHeaders(t *testing.T) {
	srv, got := capturingServer(t)
	client := NewHTTPBookingClient(srv.URL, "internal-secret")

	if _, err := client.ListCourts(context.Background(), "2026-05-01", "user@example.com", "pass123"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.email != "user@example.com" {
		t.Errorf("X-Eversports-Email: want %q, got %q", "user@example.com", got.email)
	}
	if got.password != "pass123" {
		t.Errorf("X-Eversports-Password: want %q, got %q", "pass123", got.password)
	}
}

func TestHTTPBookingClient_ListCourts_NoCredentialHeaders_WhenLoginEmpty(t *testing.T) {
	srv, got := capturingServer(t)
	client := NewHTTPBookingClient(srv.URL, "internal-secret")

	if _, err := client.ListCourts(context.Background(), "2026-05-01", "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.email != "" {
		t.Errorf("X-Eversports-Email must be absent for empty login, got %q", got.email)
	}
	if got.password != "" {
		t.Errorf("X-Eversports-Password must be absent for empty login, got %q", got.password)
	}
}

// ── ListMatches ───────────────────────────────────────────────────────────────

func TestHTTPBookingClient_ListMatches_ForwardsCredentialHeaders(t *testing.T) {
	srv, got := capturingServer(t)
	client := NewHTTPBookingClient(srv.URL, "internal-secret")

	if _, err := client.ListMatches(context.Background(), "2026-05-01", "1830", "1830", false, "user@example.com", "pass123"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.email != "user@example.com" {
		t.Errorf("X-Eversports-Email: want %q, got %q", "user@example.com", got.email)
	}
	if got.password != "pass123" {
		t.Errorf("X-Eversports-Password: want %q, got %q", "pass123", got.password)
	}
}

func TestHTTPBookingClient_ListMatches_NoCredentialHeaders_WhenLoginEmpty(t *testing.T) {
	srv, got := capturingServer(t)
	client := NewHTTPBookingClient(srv.URL, "internal-secret")

	if _, err := client.ListMatches(context.Background(), "2026-05-01", "1830", "1830", false, "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.email != "" {
		t.Errorf("X-Eversports-Email must be absent for empty login, got %q", got.email)
	}
	if got.password != "" {
		t.Errorf("X-Eversports-Password must be absent for empty login, got %q", got.password)
	}
}
