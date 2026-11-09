package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

func signedSession(secret, header, payload string) string {
	signing := base64.RawURLEncoding.EncodeToString([]byte(header)) + "." + base64.RawURLEncoding.EncodeToString([]byte(payload))
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signing))
	return signing + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestVerifyUserID(t *testing.T) {
	secret := strings.Repeat("s", 32)
	header := `{"alg":"HS256","typ":"JWT"}`
	payload := fmt.Sprintf(`{"uid":42,"exp":%d}`, time.Now().Add(time.Hour).Unix())
	valid := signedSession(secret, header, payload)
	if uid, err := VerifyUserID(secret, valid); err != nil || uid != 42 {
		t.Fatalf("valid session: uid=%d err=%v", uid, err)
	}
	cases := map[string]string{
		"no token":        "",
		"malformed":       "a.b",
		"oversized":       strings.Repeat("a", 4097),
		"tampered":        valid + "x",
		"unsigned actor":  "42",
		"expired":         signedSession(secret, header, `{"uid":42,"exp":1}`),
		"zero user":       signedSession(secret, header, fmt.Sprintf(`{"uid":0,"exp":%d}`, time.Now().Add(time.Hour).Unix())),
		"wrong algorithm": signedSession(secret, `{"alg":"none","typ":"JWT"}`, payload),
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := VerifyUserID(secret, token); err != ErrInvalid {
				t.Fatalf("invalid session error=%v", err)
			}
		})
	}
	if _, err := VerifyUserID(strings.Repeat("a", 32), valid); err != ErrInvalid {
		t.Fatalf("wrong key accepted: %v", err)
	}
	if _, err := VerifyUserID("short", valid); err != ErrInvalid {
		t.Fatalf("short key accepted: %v", err)
	}
}
