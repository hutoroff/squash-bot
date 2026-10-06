package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPRegistrarUsesAuthenticatedBoundedControlRequestsWithoutRedirects(t *testing.T) {
	instanceID := testDescriptor().InstanceID
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		switch {
		case strings.HasSuffix(r.URL.Path, "/drain"), r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			_ = json.NewEncoder(w).Encode(Lease{RegistryTime: time.Now(), LeaseRemainingSeconds: 35})
		}
	}))
	defer server.Close()
	registrar, err := NewHTTPRegistrar(server.URL, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registrar.Register(context.Background(), testDescriptor()); err != nil {
		t.Fatal(err)
	}
	if _, err := registrar.Heartbeat(context.Background(), instanceID, StateReady); err != nil {
		t.Fatal(err)
	}
	if err := registrar.Drain(context.Background(), instanceID); err != nil {
		t.Fatal(err)
	}
	if err := registrar.Deregister(context.Background(), instanceID); err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer "+strings.Repeat("s", 32) {
		t.Fatalf("authorization = %q", authorization)
	}

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	redirectRegistrar, err := NewHTTPRegistrar(redirect.URL, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := redirectRegistrar.Register(context.Background(), testDescriptor()); err == nil {
		t.Fatal("redirected registration was accepted")
	}
}

type lifecycleRegistrarFake struct {
	mu         sync.Mutex
	calls      []string
	registered chan struct{}
}

func (f *lifecycleRegistrarFake) Register(context.Context, Descriptor) (*Lease, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "register")
	f.mu.Unlock()
	select {
	case <-f.registered:
	default:
		close(f.registered)
	}
	return &Lease{}, nil
}
func (f *lifecycleRegistrarFake) Heartbeat(context.Context, string, InstanceState) (*Lease, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "heartbeat")
	f.mu.Unlock()
	return &Lease{}, nil
}
func (f *lifecycleRegistrarFake) Drain(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "drain")
	return nil
}
func (f *lifecycleRegistrarFake) Deregister(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "deregister")
	return nil
}

func TestStartLifecycleWithRegistrarStartsWithoutHTTPBootstrap(t *testing.T) {
	registrar := &lifecycleRegistrarFake{registered: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	lifecycle, err := StartLifecycleWithRegistrar(ctx, registrar, testDescriptor(), func(context.Context) bool { return true }, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-registrar.registered:
	case <-time.After(time.Second):
		t.Fatal("local registrar was not called")
	}
	cancel()
	lifecycle.Shutdown(context.Background())
	registrar.mu.Lock()
	defer registrar.mu.Unlock()
	if strings.Join(registrar.calls, ",") != "register,drain,deregister" {
		t.Fatalf("calls = %v", registrar.calls)
	}
}

func TestLifecycleRegistersAsynchronouslyAndShutsDownInOrder(t *testing.T) {
	registrar := &lifecycleRegistrarFake{registered: make(chan struct{})}
	lifecycle, err := NewLifecycle(registrar, testDescriptor(), func(context.Context) bool { return true }, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { lifecycle.Run(ctx); close(done) }()
	select {
	case <-registrar.registered:
	case <-time.After(time.Second):
		t.Fatal("registration did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("lifecycle did not stop")
	}
	lifecycle.Shutdown(context.Background())
	lifecycle.Shutdown(context.Background())
	registrar.mu.Lock()
	defer registrar.mu.Unlock()
	if strings.Join(registrar.calls, ",") != "register,drain,deregister" {
		t.Fatalf("calls = %v", registrar.calls)
	}
}

type delayedRegistrarFake struct {
	attempts chan int
	count    int
	ready    atomic.Bool
}

func (f *delayedRegistrarFake) Register(context.Context, Descriptor) (*Lease, error) {
	f.count++
	f.attempts <- f.count
	if !f.ready.Load() {
		return nil, ErrUnavailable
	}
	return &Lease{}, nil
}
func (*delayedRegistrarFake) Heartbeat(context.Context, string, InstanceState) (*Lease, error) {
	return &Lease{}, nil
}
func (*delayedRegistrarFake) Drain(context.Context, string) error      { return nil }
func (*delayedRegistrarFake) Deregister(context.Context, string) error { return nil }

func TestLifecycleBeginDrainWaitsForInFlightRegistrationAndPreventsHeartbeat(t *testing.T) {
	registrar := &blockingLifecycleRegistrar{registerStarted: make(chan struct{}), allowRegister: make(chan struct{}), drained: make(chan struct{})}
	lifecycle, err := NewLifecycle(registrar, testDescriptor(), func(context.Context) bool { return true }, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go lifecycle.Run(ctx)
	select {
	case <-registrar.registerStarted:
	case <-time.After(time.Second):
		t.Fatal("registration did not begin")
	}

	drainDone := make(chan struct{})
	go func() {
		lifecycle.BeginDrain(context.Background())
		close(drainDone)
	}()
	select {
	case <-registrar.drained:
		t.Fatal("drain ran while registration was still in flight")
	case <-time.After(25 * time.Millisecond):
	}
	close(registrar.allowRegister)
	select {
	case <-drainDone:
	case <-time.After(time.Second):
		t.Fatal("drain did not finish")
	}
	select {
	case <-registrar.drained:
	case <-time.After(time.Second):
		t.Fatal("registrar was not drained")
	}

	lifecycle.Deregister(context.Background())
	registrar.mu.Lock()
	defer registrar.mu.Unlock()
	if strings.Join(registrar.calls, ",") != "register,drain,deregister" {
		t.Fatalf("calls = %v", registrar.calls)
	}
}

type blockingLifecycleRegistrar struct {
	mu              sync.Mutex
	calls           []string
	registerStarted chan struct{}
	allowRegister   chan struct{}
	drained         chan struct{}
}

func (f *blockingLifecycleRegistrar) Register(ctx context.Context, _ Descriptor) (*Lease, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "register")
	f.mu.Unlock()
	close(f.registerStarted)
	select {
	case <-f.allowRegister:
		return &Lease{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (f *blockingLifecycleRegistrar) Heartbeat(context.Context, string, InstanceState) (*Lease, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "heartbeat")
	f.mu.Unlock()
	return &Lease{}, nil
}
func (f *blockingLifecycleRegistrar) Drain(context.Context, string) error {
	f.mu.Lock()
	f.calls = append(f.calls, "drain")
	f.mu.Unlock()
	close(f.drained)
	return nil
}
func (f *blockingLifecycleRegistrar) Deregister(context.Context, string) error {
	f.mu.Lock()
	f.calls = append(f.calls, "deregister")
	f.mu.Unlock()
	return nil
}

func TestLifecycleRegistersWhenRegistryAppearsWithoutProcessRestart(t *testing.T) {
	registrar := &delayedRegistrarFake{attempts: make(chan int, 2)}
	lifecycle, err := NewLifecycle(registrar, testDescriptor(), func(context.Context) bool { return true }, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go lifecycle.Run(ctx)
	select {
	case <-registrar.attempts:
	case <-time.After(time.Second):
		t.Fatal("initial registration was not attempted")
	}
	registrar.ready.Store(true)
	select {
	case attempt := <-registrar.attempts:
		if attempt != 2 {
			t.Fatalf("attempt = %d", attempt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("registration was not retried")
	}
}

func TestControlErrorIsTyped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusGone) }))
	defer server.Close()
	registrar, _ := NewHTTPRegistrar(server.URL, strings.Repeat("s", 32))
	_, err := registrar.Register(context.Background(), testDescriptor())
	var control *ControlError
	if !errors.As(err, &control) || control.Status != http.StatusGone {
		t.Fatalf("error = %v", err)
	}
}
