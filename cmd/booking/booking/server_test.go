package booking

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// sentinel is a trivial handler that returns 200 with a fixed body,
// used to verify that requireBearer correctly forwards authorised requests.
var sentinel = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok")) //nolint:errcheck
})

type recordedBookingHTTP struct {
	method, route, status string
	calls                 int
}

func (o *recordedBookingHTTP) ObserveHTTPRequest(method, route, status string, _ time.Duration) {
	o.method, o.route, o.status = method, route, status
	o.calls++
}

func TestBookingHTTPMetricsPreserveMuxPathValuesAndBoundedLabels(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/matches/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "123" {
			t.Errorf("path value = %q", r.PathValue("id"))
		}
		w.WriteHeader(http.StatusNoContent)
	})
	observer := &recordedBookingHTTP{}
	server := observeHTTP(observer, mux, requireBearer("secret", mux))
	req := httptest.NewRequest(http.MethodGet, "/api/v2/matches/123", nil)
	req.Header.Set("Authorization", "Bearer secret")
	server.ServeHTTP(httptest.NewRecorder(), req)
	if observer.calls != 1 || observer.route != "GET /api/v2/matches/{id}" || observer.status != "204" {
		t.Fatalf("observed = %+v", observer)
	}
	server.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("ARBITRARY_METHOD", "/random/secret", nil))
	if observer.calls != 2 || observer.method != "OTHER" || observer.route != "unmatched" || observer.status != "401" {
		t.Fatalf("fallback = %+v", observer)
	}
}

func TestRequireBearer_HealthExempt(t *testing.T) {
	h := requireBearer("secret", sentinel)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("GET /health: want 200, got %d", w.Code)
	}
}

func TestRequireBearer_VersionRequiresBusinessAuthentication(t *testing.T) {
	h := requireBearer("secret", sentinel)
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("GET /version: want 401, got %d", w.Code)
	}
}

func TestRequireBearer_MissingHeader(t *testing.T) {
	h := requireBearer("secret", sentinel)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/eversports/bookings", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("missing header: want 401, got %d", w.Code)
	}
}

func TestRequireBearer_WrongSecret(t *testing.T) {
	h := requireBearer("secret", sentinel)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/eversports/bookings", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong secret: want 401, got %d", w.Code)
	}
}

func TestRequireBearer_WrongScheme(t *testing.T) {
	h := requireBearer("secret", sentinel)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/eversports/bookings", nil)
	req.Header.Set("Authorization", "Basic secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong scheme: want 401, got %d", w.Code)
	}
}

func TestRequireBearer_CorrectSecret(t *testing.T) {
	h := requireBearer("secret", sentinel)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/eversports/bookings", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("correct secret: want 200, got %d", w.Code)
	}
}

// echoBody is a handler that reads the full body and reports whether the
// read succeeded, used to observe limitRequestBody's effect on r.Body.
var echoBody = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	if _, err := io.ReadAll(r.Body); err != nil {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	w.WriteHeader(http.StatusOK)
})

func TestLimitRequestBody_AllowsBodyUnderLimit(t *testing.T) {
	h := limitRequestBody(echoBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/eversports/matches", bytes.NewReader([]byte(`{"hello":"world"}`)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("normal body: want 200, got %d", w.Code)
	}
}

func TestLimitRequestBody_RejectsBodyOverLimit(t *testing.T) {
	h := limitRequestBody(echoBody)
	oversized := bytes.Repeat([]byte("a"), maxRequestBodyBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/eversports/matches", bytes.NewReader(oversized))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: want 413, got %d", w.Code)
	}
}

func TestServeWithReadySignalsOnlyAfterListenerBinds(t *testing.T) {
	srv := &http.Server{Addr: "127.0.0.1:0", Handler: sentinel}
	listening := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- ServeWithReady(srv, slog.Default(), func() { close(listening) })
	}()
	select {
	case <-listening:
	case <-time.After(time.Second):
		t.Fatal("listener did not become ready")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ServeWithReady() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
}
