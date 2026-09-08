package discovery

import (
	"context"
	"log/slog"
	"time"
)

func StartLifecycle(ctx context.Context, registryURL, internalSecret string, descriptor Descriptor, readiness func(context.Context) bool, logger *slog.Logger) (*Lifecycle, error) {
	if registryURL == "" {
		return nil, nil
	}
	registrar, err := NewHTTPRegistrar(registryURL, internalSecret)
	if err != nil {
		return nil, err
	}
	lifecycle, err := NewLifecycle(registrar, descriptor, readiness, logger)
	if err != nil {
		return nil, err
	}
	go lifecycle.Run(ctx)
	return lifecycle, nil
}

func ShutdownLifecycle(lifecycle *Lifecycle) {
	if lifecycle == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	lifecycle.Shutdown(ctx)
}
