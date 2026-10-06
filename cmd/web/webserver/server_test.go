package webserver

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"log/slog"
)

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
	req := httptest.NewRequest(http.MethodPatch, "/api/groups/1/auto-booking-allowed", bytes.NewReader([]byte(`{"enabled":true}`)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("normal body: want 200, got %d", w.Code)
	}
}

func TestLimitRequestBody_RejectsBodyOverLimit(t *testing.T) {
	h := limitRequestBody(echoBody)
	oversized := bytes.Repeat([]byte("a"), maxRequestBodyBytes+1)
	req := httptest.NewRequest(http.MethodPatch, "/api/groups/1/auto-booking-allowed", bytes.NewReader(oversized))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: want 413, got %d", w.Code)
	}
}

func TestRunWithReadySignalsOnlyAfterListenerBinds(t *testing.T) {
	srv := &http.Server{Addr: "127.0.0.1:0", Handler: echoBody}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listening := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- RunWithReady(ctx, srv, slog.Default(), func() { close(listening) })
	}()
	select {
	case <-listening:
	case <-time.After(time.Second):
		t.Fatal("listener did not become ready")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunWithReady() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
}
