package storage

import (
	"bytes"
	"strings"
	"testing"
)

func TestDecodeIdentityKeyAndMarker(t *testing.T) {
	key, err := DecodeIdentityKey("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 {
		t.Fatalf("decoded key length = %d, want 32", len(key))
	}
	marker, err := IdentityKeyMarker(key)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(marker, "hmac-sha256:v1:") || bytes.Contains([]byte(marker), key) {
		t.Fatalf("invalid identity key marker %q", marker)
	}
	markerAgain, err := IdentityKeyMarker(key)
	if err != nil || markerAgain != marker {
		t.Fatalf("marker is not deterministic: %q, %v", markerAgain, err)
	}
}

func TestDecodeIdentityKeyRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"", "not-a-key", strings.Repeat("z", 64), strings.Repeat("0", 62)} {
		if _, err := DecodeIdentityKey(value); err == nil {
			t.Errorf("DecodeIdentityKey(%q) unexpectedly succeeded", value)
		}
	}
	if _, err := IdentityKeyMarker(make([]byte, 31)); err == nil {
		t.Fatal("IdentityKeyMarker accepted a short key")
	}
}
