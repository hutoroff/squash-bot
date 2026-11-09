package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

func signedRiskTestSession(secret, payload string) string {
	encode := base64.RawURLEncoding.EncodeToString
	signed := encode([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + encode([]byte(payload))
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signed))
	return signed + "." + encode(mac.Sum(nil))
}

func TestRiskActorNeedsSignedSessionAndLiveOwnerStatus(t *testing.T) {
	secret := strings.Repeat("s", 32)
	h := &Handler{userRepo: &fakeUserRepo{byID: map[int64]*models.User{
		42: {ID: 42, IsServerOwner: true}, 12: {ID: 12, IsServerOwner: false},
	}}, riskSessionSecret: secret}
	valid := signedRiskTestSession(secret, `{"uid":42,"exp":`+strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)+`}`)
	// A forged actor header/internal bearer alone must not pass.
	check := func(token, actor string) (int, int64, bool) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("X-Web-Session", token)
		req.Header.Set("X-Caller-User-Id", actor)
		rec := httptest.NewRecorder()
		id, ok := h.riskActor(rec, req)
		return rec.Code, id, ok
	}
	if status, _, ok := check("", "42"); ok || status != http.StatusUnauthorized {
		t.Fatalf("unsigned owner status=%d ok=%t", status, ok)
	}
	if status, id, ok := check(valid, "12"); !ok || id != 42 || status != http.StatusOK {
		t.Fatalf("verified owner status=%d id=%d ok=%t", status, id, ok)
	}
	ordinary := signedRiskTestSession(secret, `{"uid":12,"exp":`+strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)+`}`)
	if status, _, ok := check(ordinary, "42"); ok || status != http.StatusForbidden {
		t.Fatalf("non-owner status=%d ok=%t", status, ok)
	}
	h.userRepo.(*fakeUserRepo).byID[42].IsServerOwner = false
	if status, _, ok := check(valid, "42"); ok || status != http.StatusForbidden {
		t.Fatalf("revoked owner status=%d ok=%t", status, ok)
	}
	h.userRepo = nil
	if status, _, ok := check(valid, "42"); ok || status != http.StatusServiceUnavailable {
		t.Fatalf("owner store unavailable status=%d ok=%t", status, ok)
	}
}
