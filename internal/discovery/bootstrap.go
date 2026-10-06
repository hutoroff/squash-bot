package discovery

import (
	"context"
	"log/slog"
)

func StartLifecycle(ctx context.Context, registryURL, internalSecret string, descriptor Descriptor, readiness func(context.Context) bool, logger *slog.Logger) (*Lifecycle, error) {
	if registryURL == "" {
		return nil, nil
	}
	registrar, err := NewHTTPRegistrar(registryURL, internalSecret)
	if err != nil {
		return nil, err
	}
	return StartLifecycleWithRegistrar(ctx, registrar, descriptor, readiness, logger)
}

// StartLifecycleWithRegistrar starts registration using a supplied registrar.
// Management uses this path for its own registry so it does not call its
// business HTTP listener merely to register itself.
func StartLifecycleWithRegistrar(ctx context.Context, registrar Registrar, descriptor Descriptor, readiness func(context.Context) bool, logger *slog.Logger) (*Lifecycle, error) {
	lifecycle, err := NewLifecycle(registrar, descriptor, readiness, logger)
	if err != nil {
		return nil, err
	}
	go lifecycle.Run(ctx)
	return lifecycle, nil
}

func BeginDrainLifecycle(lifecycle *Lifecycle) {
	if lifecycle == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlRequestTimeout)
	defer cancel()
	lifecycle.BeginDrain(ctx)
}

func DeregisterLifecycle(lifecycle *Lifecycle) {
	if lifecycle == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlRequestTimeout)
	defer cancel()
	lifecycle.Deregister(ctx)
}

// ShutdownLifecycle preserves the one-call shutdown helper for services that
// have no bounded local drain phase. Tracked executors should use the two
// phase helpers instead.
func ShutdownLifecycle(lifecycle *Lifecycle) {
	BeginDrainLifecycle(lifecycle)
	DeregisterLifecycle(lifecycle)
}
