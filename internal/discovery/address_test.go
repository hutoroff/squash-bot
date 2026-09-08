package discovery

import "testing"

func TestResolveAdvertiseHostValidatesExplicitAddress(t *testing.T) {
	address, err := ResolveAdvertiseHost("10.20.30.40", "")
	if err != nil || address != "10.20.30.40" {
		t.Fatalf("address = %q err = %v", address, err)
	}
	for _, invalid := range []string{"localhost", "127.0.0.1", "0.0.0.0"} {
		if _, err := ResolveAdvertiseHost(invalid, ""); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
}
