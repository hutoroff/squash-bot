package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// sentinel is a trivial handler that always returns 200 with a known body,
// used to verify that requireBearer correctly forwards authorised requests.
var sentinel = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok")) //nolint:errcheck
})

func TestRequireBearer_HealthExempt(t *testing.T) {
	h := requireBearer("secret", sentinel)
	// /health must respond 200 with no Authorization header.
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("GET /health: want 200, got %d", w.Code)
	}
}

func TestRequireBearer_MissingHeader(t *testing.T) {
	h := requireBearer("secret", sentinel)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/games", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("missing header: want 401, got %d", w.Code)
	}
}

func TestRequireBearer_WrongSecret(t *testing.T) {
	h := requireBearer("secret", sentinel)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/games", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong secret: want 401, got %d", w.Code)
	}
}

func TestRequireBearer_WrongScheme(t *testing.T) {
	// Token is correct but scheme is not "Bearer".
	h := requireBearer("secret", sentinel)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/games", nil)
	req.Header.Set("Authorization", "Basic secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong scheme: want 401, got %d", w.Code)
	}
}

func TestRequireBearer_CorrectSecret(t *testing.T) {
	h := requireBearer("secret", sentinel)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/games", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("correct secret: want 200, got %d", w.Code)
	}
}
