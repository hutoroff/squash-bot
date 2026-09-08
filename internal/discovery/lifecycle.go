package discovery

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"log/slog"
	"sync/atomic"
	"time"
)

const (
	defaultHeartbeatInterval   = 10 * time.Second
	initialRegistrationBackoff = time.Second
	maximumRegistrationBackoff = 10 * time.Second
)

type Lifecycle struct {
	registrar  Registrar
	descriptor Descriptor
	readiness  func(context.Context) bool
	logger     *slog.Logger
	stopping   atomic.Bool
}

func NewLifecycle(registrar Registrar, descriptor Descriptor, readiness func(context.Context) bool, logger *slog.Logger) (*Lifecycle, error) {
	if registrar == nil || readiness == nil || logger == nil {
		return nil, ErrUnavailable
	}
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	descriptor.State = StateStarting
	return &Lifecycle{registrar: registrar, descriptor: descriptor, readiness: readiness, logger: logger}, nil
}

func (l *Lifecycle) Run(ctx context.Context) {
	registered := false
	backoff := initialRegistrationBackoff
	for !l.stopping.Load() {
		var err error
		if !registered {
			_, err = l.registrar.Register(ctx, l.descriptor)
			if err == nil {
				registered = true
				backoff = initialRegistrationBackoff
				l.logger.Info("service discovery registration active", "service_key", l.descriptor.ServiceKey, "instance_id", l.descriptor.InstanceID)
			}
		} else {
			state := StateNotReady
			readyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			if l.readiness(readyCtx) {
				state = StateReady
			}
			cancel()
			_, err = l.registrar.Heartbeat(ctx, l.descriptor.InstanceID, state)
			if err != nil {
				registered = false
			}
		}
		if ctx.Err() != nil || l.stopping.Load() {
			return
		}
		wait := defaultHeartbeatInterval
		if err != nil {
			l.logger.Warn("service discovery control request failed", "service_key", l.descriptor.ServiceKey, "err", err)
			wait = backoff + registrationJitter()
			backoff *= 2
			if backoff > maximumRegistrationBackoff {
				backoff = maximumRegistrationBackoff
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (l *Lifecycle) Shutdown(ctx context.Context) {
	if !l.stopping.CompareAndSwap(false, true) {
		return
	}
	if err := l.registrar.Drain(ctx, l.descriptor.InstanceID); err != nil {
		l.logger.Warn("service discovery drain failed", "service_key", l.descriptor.ServiceKey, "err", err)
	}
	if err := l.registrar.Deregister(ctx, l.descriptor.InstanceID); err != nil {
		l.logger.Warn("service discovery deregistration failed", "service_key", l.descriptor.ServiceKey, "err", err)
	}
}

func registrationJitter() time.Duration {
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return 0
	}
	return time.Duration(binary.LittleEndian.Uint64(value[:])%250) * time.Millisecond
}
