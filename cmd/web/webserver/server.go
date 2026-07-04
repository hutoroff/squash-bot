package webserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// maxRequestBodyBytes bounds request bodies to guard against memory/CPU
// exhaustion from oversized payloads. 1 MiB is far larger than any real
// request this API handles.
const maxRequestBodyBytes = 1 << 20

// NewServer builds an http.Server with the handler's routes registered.
func NewServer(addr string, h *Handler) *http.Server {
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return &http.Server{
		Addr:         addr,
		Handler:      limitRequestBody(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}

// limitRequestBody caps every request body at maxRequestBodyBytes, returning
// an error from the body reader once the limit is exceeded, instead of
// buffering an unbounded payload.
func limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// Run starts the server and blocks until ctx is cancelled, then gracefully shuts down.
func Run(ctx context.Context, srv *http.Server, logger *slog.Logger) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("HTTP server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
