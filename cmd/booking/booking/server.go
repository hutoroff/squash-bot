package booking

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/hutoroff/squash-bot/internal/observability"
)

// maxRequestBodyBytes bounds request bodies to guard against memory/CPU
// exhaustion from oversized payloads. 1 MiB is far larger than any real
// request this API handles.
const maxRequestBodyBytes = 1 << 20

type HTTPObserver interface {
	ObserveHTTPRequest(method, route, status string, duration time.Duration)
}

// NewServer builds an http.Server with the handler's routes registered.
// Only /health is public; business and legacy version routes require the
// business bearer token. Rich version data is exposed by the admin listener.
func NewServer(addr string, h *Handler, secret string) *http.Server {
	return NewObservedServer(addr, h, secret, nil)
}

func NewObservedServer(addr string, h *Handler, secret string, observer HTTPObserver) *http.Server {
	return NewObservedServerWithWriteTimeout(addr, h, secret, observer, 30*time.Second)
}

func NewObservedServerWithWriteTimeout(addr string, h *Handler, secret string, observer HTTPObserver, writeTimeout time.Duration) *http.Server {
	if writeTimeout <= 0 {
		writeTimeout = 30 * time.Second
	}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	var routes http.Handler = requireBearer(secret, mux)
	if observer != nil {
		routes = observeHTTP(observer, mux, routes)
	}
	return &http.Server{
		Addr:         addr,
		Handler:      limitRequestBody(routes),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: writeTimeout,
		IdleTimeout:  60 * time.Second,
	}
}

// limitRequestBody caps every request body at maxRequestBodyBytes, returning
// an error from the body reader (surfaced as 400 by the JSON decoders) once
// the limit is exceeded, instead of buffering an unbounded payload.
func limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		next.ServeHTTP(w, r)
	})
}

func observeHTTP(observer HTTPObserver, mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}
		_, route := mux.Handler(r)
		if route == "" {
			route = "unmatched"
		}
		observer.ObserveHTTPRequest(observability.BoundedMethod(r.Method), route, strconv.Itoa(recorder.status), time.Since(started))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// requireBearer validates the Authorization: Bearer <secret> header.
// Only the compatibility liveness endpoint is exempt.
func requireBearer(secret string, next http.Handler) http.Handler {
	expected := []byte("Bearer " + secret)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, expected) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Run starts the server and blocks until ctx is cancelled, then gracefully shuts down.
func Run(ctx context.Context, srv *http.Server, logger *slog.Logger) error {
	errCh := make(chan error, 1)
	go func() {
		if err := ServeWithReady(srv, logger, nil); err != nil {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		logger.Info("HTTP server shutting down")
		return srv.Shutdown(shutCtx)
	}
}

// ServeWithReady binds and serves business traffic. listening runs only after
// the TCP listener exists, allowing callers to expose accurate local readiness.
func ServeWithReady(srv *http.Server, logger *slog.Logger, listening func()) error {
	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	logger.Info("HTTP server listening", "addr", listener.Addr().String())
	if listening != nil {
		listening()
	}
	if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON: encode", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
