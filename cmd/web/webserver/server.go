package webserver

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/hutoroff/squash-bot/internal/observability"
)

// maxRequestBodyBytes bounds request bodies to guard against memory/CPU
// exhaustion from oversized payloads. 1 MiB is far larger than any real
// request this API handles.
const maxRequestBodyBytes = 1 << 20

// NewServer builds an http.Server with the handler's routes registered.
func NewServer(addr string, h *Handler) *http.Server { return NewObservedServer(addr, h, nil) }
func NewObservedServer(addr string, h *Handler, metrics *observability.HTTPMetrics) *http.Server {
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	var routes http.Handler = mux
	if metrics != nil {
		routes = metrics.WrapMux(mux)
	}
	return &http.Server{
		Addr:         addr,
		Handler:      limitRequestBody(routes),
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
	return RunWithReady(ctx, srv, logger, nil)
}

// RunWithReady invokes listening after the TCP listener is successfully bound,
// before serving requests. It lets local readiness distinguish a constructed
// server from a process that can actually accept business traffic.
func RunWithReady(ctx context.Context, srv *http.Server, logger *slog.Logger, listening func()) error {
	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	logger.Info("HTTP server listening", "addr", listener.Addr().String())
	if listening != nil {
		listening()
	}
	errCh := make(chan error, 1)
	go func() {
		if serveErr := srv.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			errCh <- serveErr
		}
	}()

	select {
	case serveErr := <-errCh:
		return serveErr
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		logger.Info("HTTP server shutting down")
		return srv.Shutdown(shutCtx)
	}
}
