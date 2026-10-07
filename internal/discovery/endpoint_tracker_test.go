package discovery

import "testing"

func TestEndpointTrackerClosesOnlyWhenOriginChanges(t *testing.T) {
	calls := 0
	tracker := NewEndpointTracker(func() { calls++ })
	tracker.Observe("http://10.0.0.1:8080")
	tracker.Observe("http://10.0.0.1:8080")
	if calls != 0 {
		t.Fatalf("close calls after initial/same origin = %d", calls)
	}
	tracker.Observe("http://10.0.0.2:8080")
	if calls != 1 {
		t.Fatalf("close calls after origin change = %d", calls)
	}
}
