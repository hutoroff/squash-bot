package discovery

import "sync"

// EndpointTracker retires only idle transport connections when a caller starts
// selecting a different resolved origin. It never closes active connections and
// never triggers a request retry.
type EndpointTracker struct {
	mu             sync.Mutex
	current        string
	closeIdleConns func()
}

func NewEndpointTracker(closeIdleConns func()) *EndpointTracker {
	return &EndpointTracker{closeIdleConns: closeIdleConns}
}

func (t *EndpointTracker) Observe(origin string) {
	if t == nil || origin == "" {
		return
	}
	t.mu.Lock()
	changed := t.current != "" && t.current != origin
	t.current = origin
	closeIdleConns := t.closeIdleConns
	t.mu.Unlock()
	if changed && closeIdleConns != nil {
		closeIdleConns()
	}
}
