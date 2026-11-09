package webserver

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWebRiskProxyRequiresSameOriginAndForwardsOnlySignedSession(t *testing.T) {
	secret := strings.Repeat("s", 32)
	token, err := issueJWT(secret, JWTClaims{UserID: 42, Exp: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.Header.Get("X-Web-Session"); got != token {
			t.Errorf("session proof missing or spoofed")
		}
		if r.Header.Get("X-Caller-User-Id") != "" {
			t.Errorf("untrusted actor forwarded")
		}
		if r.URL.Path != "/api/v1/booking/operations/aaaaaaaa-aaaa-7aaa-8aaa-aaaaaaaaaaaa/authorize-risk" {
			t.Errorf("path=%q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "actor_user_id") || !strings.Contains(string(body), `"credential_id":9`) {
			t.Errorf("unexpected forwarded body: %s", body)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"operation_id":"synthetic"}`))
	}))
	defer upstream.Close()
	auth := NewAuthHandler("bot", "bot", secret, upstream.URL, strings.Repeat("m", 32), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := NewUsersHandler(auth, upstream.URL, strings.Repeat("m", 32))
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/booking/operations/{id}/authorize-risk", h.handleAuthorizeBookingRisk)
	url := "http://web.test/api/booking/operations/aaaaaaaa-aaaa-7aaa-8aaa-aaaaaaaaaaaa/authorize-risk"
	call := func(origin, body string, cookie bool) int {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Web-Session", "attacker")
		r.Header.Set("X-Caller-User-Id", "999")
		if cookie {
			r.AddCookie(&http.Cookie{Name: "session", Value: token})
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	body := `{"credential_id":9,"reason":"manual","expected_revision":2,"accept_duplicate_risk":true}`
	for _, tc := range []struct {
		origin, body string
		cookie       bool
		want         int
	}{
		{"http://web.test", body, false, 401},
		{"", body, true, 403},
		{"http://attacker.test", body, true, 403},
		{"http://web.test", `{"credential_id":9,"actor_user_id":999}`, true, 400},
		{"http://web.test", body, true, 202},
	} {
		if got := call(tc.origin, tc.body, tc.cookie); got != tc.want {
			t.Fatalf("origin=%q status=%d want=%d", tc.origin, got, tc.want)
		}
	}
	if calls != 1 {
		t.Fatalf("upstream calls=%d", calls)
	}
}
