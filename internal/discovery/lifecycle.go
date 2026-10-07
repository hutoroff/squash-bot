package discovery

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultHeartbeatInterval   = 10 * time.Second
	initialRegistrationBackoff = time.Second
	maximumRegistrationBackoff = 10 * time.Second
	controlRequestTimeout      = 3 * time.Second
)

type RegistrationObserver interface{ Registered(*Lease) }

type Lifecycle struct {
	registrar  Registrar
	observer   RegistrationObserver
	descriptor Descriptor
	readiness  func(context.Context) bool
	logger     *slog.Logger

	// stopping is set before drain begins. controlMu serializes all control
	// requests so a register/heartbeat cannot complete after drain and revive
	// the incarnation at the registry.
	stopping       atomic.Bool
	controlMu      sync.Mutex
	drainOnce      sync.Once
	deregisterOnce sync.Once
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

func (l *Lifecycle) SetRegistrationObserver(observer RegistrationObserver) { l.observer = observer }

func (l *Lifecycle) Run(ctx context.Context) {
	registered := false
	backoff := initialRegistrationBackoff
	for !l.stopping.Load() {
		var err error
		if !registered {
			err = l.control(ctx, func(controlCtx context.Context) error {
				if l.stopping.Load() {
					return context.Canceled
				}
				lease, registerErr := l.registrar.Register(controlCtx, l.descriptor)
				if registerErr == nil && l.observer != nil {
					l.observer.Registered(lease)
				}
				return registerErr
			})
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
			err = l.control(ctx, func(controlCtx context.Context) error {
				if l.stopping.Load() {
					return context.Canceled
				}
				lease, heartbeatErr := l.registrar.Heartbeat(controlCtx, l.descriptor.InstanceID, state)
				if heartbeatErr == nil && l.observer != nil {
					l.observer.Registered(lease)
				}
				return heartbeatErr
			})
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

// BeginDrain stops registration/renewal and marks the process incarnation
// draining. Call it before closing the business listener so resolvers stop
// selecting this instance while the admin listener remains observable.
func (l *Lifecycle) BeginDrain(ctx context.Context) {
	l.stopping.Store(true)
	l.drainOnce.Do(func() {
		err := l.control(ctx, func(controlCtx context.Context) error {
			return l.registrar.Drain(controlCtx, l.descriptor.InstanceID)
		})
		if err != nil {
			l.logger.Warn("service discovery drain failed", "service_key", l.descriptor.ServiceKey, "err", err)
		}
	})
}

// Deregister records the terminal tombstone after bounded local work has
// drained. It intentionally follows BeginDrain rather than replacing it.
func (l *Lifecycle) Deregister(ctx context.Context) {
	l.BeginDrain(ctx)
	l.deregisterOnce.Do(func() {
		err := l.control(ctx, func(controlCtx context.Context) error {
			return l.registrar.Deregister(controlCtx, l.descriptor.InstanceID)
		})
		if err != nil {
			l.logger.Warn("service discovery deregistration failed", "service_key", l.descriptor.ServiceKey, "err", err)
		}
	})
}

// Shutdown preserves the original convenience API for callers without a
// bounded-work phase. Services with tracked work should call BeginDrain before
// stopping admission/listeners and Deregister only after that work finishes.
func (l *Lifecycle) Shutdown(ctx context.Context) {
	l.BeginDrain(ctx)
	l.Deregister(ctx)
}

func (l *Lifecycle) control(ctx context.Context, action func(context.Context) error) error {
	l.controlMu.Lock()
	defer l.controlMu.Unlock()
	controlCtx, cancel := context.WithTimeout(ctx, controlRequestTimeout)
	defer cancel()
	return action(controlCtx)
}

func registrationJitter() time.Duration {
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return 0
	}
	return time.Duration(binary.LittleEndian.Uint64(value[:])%250) * time.Millisecond
}
