package webserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// buildTelegramParams returns a complete, correctly-signed set of Telegram Login
// Widget query parameters for the given bot token and user fields.
// Extra optional key-value pairs (e.g. "username", "alice") can be appended.
func buildTelegramParams(botToken string, telegramID int64, firstName string, extraKV ...string) map[string]string {
	params := map[string]string{
		"id":         fmt.Sprintf("%d", telegramID),
		"first_name": firstName,
		"auth_date":  fmt.Sprintf("%d", time.Now().Unix()),
	}
	for i := 0; i+1 < len(extraKV); i += 2 {
		params[extraKV[i]] = extraKV[i+1]
	}

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + params[k]
	}
	keyHash := sha256.Sum256([]byte(botToken))
	mac := hmac.New(sha256.New, keyHash[:])
	mac.Write([]byte(strings.Join(parts, "\n")))
	params["hash"] = hex.EncodeToString(mac.Sum(nil))
	return params
}

// paramsToQuery encodes a map as a URL query string (no URL-escaping needed for test values).
func paramsToQuery(params map[string]string) string {
	parts := make([]string, 0, len(params))
	for k, v := range params {
		parts = append(parts, k+"="+v)
	}
	return "?" + strings.Join(parts, "&")
}

// testAuthHandler returns an AuthHandler wired to a fake management server.
// Pass nil for mgmtHandler when the management service is not expected to be called.
func testAuthHandler(t *testing.T, mgmtHandler http.Handler) *AuthHandler {
	t.Helper()
	if mgmtHandler == nil {
		mgmtHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
	}
	mgmt := httptest.NewServer(mgmtHandler)
	t.Cleanup(mgmt.Close)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return NewAuthHandler("bot-token", "TestBot", "jwt-secret-32-bytes-long-xxxxxxx", mgmt.URL, "mgmt-secret", nil, logger)
}

// ── verifyTelegramAuth ────────────────────────────────────────────────────────

func TestVerifyTelegramAuth(t *testing.T) {
	const token = "bot-token"

	t.Run("valid params", func(t *testing.T) {
		if !verifyTelegramAuth(token, buildTelegramParams(token, 42, "Alice")) {
			t.Error("want true, got false for valid params")
		}
	})

	t.Run("missing hash", func(t *testing.T) {
		params := buildTelegramParams(token, 42, "Alice")
		delete(params, "hash")
		if verifyTelegramAuth(token, params) {
			t.Error("want false when hash is missing")
		}
	})

	t.Run("tampered value", func(t *testing.T) {
		params := buildTelegramParams(token, 42, "Alice")
		params["first_name"] = "Eve" // mutate after signing
		if verifyTelegramAuth(token, params) {
			t.Error("want false when a param is tampered with")
		}
	})

	t.Run("wrong bot token", func(t *testing.T) {
		if verifyTelegramAuth("other-token", buildTelegramParams(token, 42, "Alice")) {
			t.Error("want false when verified with the wrong bot token")
		}
	})

	t.Run("expired auth_date", func(t *testing.T) {
		// Build params with a stale auth_date and re-sign them.
		params := map[string]string{
			"id":         "42",
			"first_name": "Alice",
			"auth_date":  fmt.Sprintf("%d", time.Now().Add(-25*time.Hour).Unix()),
		}
		keys := []string{"auth_date", "first_name", "id"}
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + "=" + params[k]
		}
		keyHash := sha256.Sum256([]byte(token))
		mac := hmac.New(sha256.New, keyHash[:])
		mac.Write([]byte(strings.Join(parts, "\n")))
		params["hash"] = hex.EncodeToString(mac.Sum(nil))

		if verifyTelegramAuth(token, params) {
			t.Error("want false for auth_date older than 86400 s")
		}
	})

	t.Run("missing auth_date", func(t *testing.T) {
		params := map[string]string{
			"id":         "42",
			"first_name": "Alice",
		}
		keys := []string{"first_name", "id"}
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + "=" + params[k]
		}
		keyHash := sha256.Sum256([]byte(token))
		mac := hmac.New(sha256.New, keyHash[:])
		mac.Write([]byte(strings.Join(parts, "\n")))
		params["hash"] = hex.EncodeToString(mac.Sum(nil))

		if verifyTelegramAuth(token, params) {
			t.Error("want false when auth_date is absent")
		}
	})

	t.Run("optional fields included", func(t *testing.T) {
		params := buildTelegramParams(token, 42, "Alice", "username", "alice", "last_name", "Smith")
		if !verifyTelegramAuth(token, params) {
			t.Error("want true when optional fields are present and correctly signed")
		}
	})
}

// ── issueJWT / parseJWT ───────────────────────────────────────────────────────

func TestJWTRoundTrip(t *testing.T) {
	const secret = "jwt-secret-32-bytes-long-xxxxxxx"
	playerID := int64(99)

	original := JWTClaims{
		TelegramID: 12345,
		PlayerID:   &playerID,
		FirstName:  "Alice",
		LastName:   "Smith",
		Username:   "alice",
		PhotoURL:   "https://example.com/photo.jpg",
		Exp:        time.Now().Add(time.Hour).Unix(),
	}

	token, err := issueJWT(secret, original)
	if err != nil {
		t.Fatalf("issueJWT: %v", err)
	}
	if strings.Count(token, ".") != 2 {
		t.Fatalf("token must have exactly 2 dots, got %q", token)
	}

	got, err := parseJWT(secret, token)
	if err != nil {
		t.Fatalf("parseJWT: %v", err)
	}
	if got.TelegramID != original.TelegramID {
		t.Errorf("TelegramID: want %d, got %d", original.TelegramID, got.TelegramID)
	}
	if got.PlayerID == nil || *got.PlayerID != *original.PlayerID {
		t.Errorf("PlayerID: want %d, got %v", *original.PlayerID, got.PlayerID)
	}
	if got.FirstName != original.FirstName {
		t.Errorf("FirstName: want %q, got %q", original.FirstName, got.FirstName)
	}
	if got.Username != original.Username {
		t.Errorf("Username: want %q, got %q", original.Username, got.Username)
	}
}

func TestJWTHeader_IsValidJSON(t *testing.T) {
	token, err := issueJWT("secret", JWTClaims{TelegramID: 1, Exp: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatalf("issueJWT: %v", err)
	}
	headerSeg := strings.Split(token, ".")[0]
	headerBytes, err := base64.RawURLEncoding.DecodeString(headerSeg)
	if err != nil {
		t.Fatalf("decode header segment: %v", err)
	}
	var hdr map[string]string
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		t.Fatalf("header is not valid JSON: %v (raw: %s)", err, headerBytes)
	}
	if hdr["alg"] != "HS256" {
		t.Errorf("alg: want HS256, got %q", hdr["alg"])
	}
	if hdr["typ"] != "JWT" {
		t.Errorf("typ: want JWT, got %q", hdr["typ"])
	}
}

func TestParseJWT_Errors(t *testing.T) {
	const secret = "jwt-secret-32-bytes-long-xxxxxxx"

	validToken, _ := issueJWT(secret, JWTClaims{
		TelegramID: 1,
		Exp:        time.Now().Add(time.Hour).Unix(),
	})

	t.Run("wrong secret", func(t *testing.T) {
		if _, err := parseJWT("other-secret", validToken); err == nil {
			t.Error("want error for wrong secret")
		}
	})

	t.Run("expired token", func(t *testing.T) {
		expired, _ := issueJWT(secret, JWTClaims{
			TelegramID: 1,
			Exp:        time.Now().Add(-time.Minute).Unix(),
		})
		if _, err := parseJWT(secret, expired); err == nil {
			t.Error("want error for expired token")
		}
	})

	t.Run("malformed — one segment", func(t *testing.T) {
		if _, err := parseJWT(secret, "notavalidtoken"); err == nil {
			t.Error("want error for single-segment token")
		}
	})

	t.Run("malformed — two segments", func(t *testing.T) {
		if _, err := parseJWT(secret, "a.b"); err == nil {
			t.Error("want error for two-segment token")
		}
	})

	t.Run("tampered payload", func(t *testing.T) {
		parts := strings.Split(validToken, ".")
		payload := []byte(parts[1])
		payload[0] ^= 0x01
		tampered := parts[0] + "." + string(payload) + "." + parts[2]
		if _, err := parseJWT(secret, tampered); err == nil {
			t.Error("want error for tampered payload")
		}
	})
}

// ── handleCallback ────────────────────────────────────────────────────────────

func TestHandleCallback_ValidAuth(t *testing.T) {
	h := testAuthHandler(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound) // player not yet in the bot
	}))

	params := buildTelegramParams("bot-token", 42, "Alice", "username", "alice")
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback"+paramsToQuery(params), nil)
	w := httptest.NewRecorder()
	h.handleCallback(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("redirect location: want /, got %q", loc)
	}

	var session *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName {
			session = c
			break
		}
	}
	if session == nil {
		t.Fatal("session cookie not set")
	}
	if !session.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if session.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite: want Lax, got %v", session.SameSite)
	}
	if session.Secure {
		t.Error("Secure must be false for plain HTTP")
	}

	// JWT in the cookie must be parseable and carry correct claims.
	claims, err := parseJWT(h.jwtSecret, session.Value)
	if err != nil {
		t.Fatalf("parseJWT on issued cookie: %v", err)
	}
	if claims.TelegramID != 42 {
		t.Errorf("TelegramID in JWT: want 42, got %d", claims.TelegramID)
	}
	if claims.FirstName != "Alice" {
		t.Errorf("FirstName in JWT: want Alice, got %q", claims.FirstName)
	}
}

func TestHandleCallback_PlayerLinked(t *testing.T) {
	// Management service returns a known player record.
	h := testAuthHandler(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"id":7,"telegram_id":42}`)
	}))

	params := buildTelegramParams("bot-token", 42, "Alice")
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback"+paramsToQuery(params), nil)
	w := httptest.NewRecorder()
	h.handleCallback(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302, got %d", w.Code)
	}
	var session *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName {
			session = c
		}
	}
	if session == nil {
		t.Fatal("session cookie not set")
	}
	claims, err := parseJWT(h.jwtSecret, session.Value)
	if err != nil {
		t.Fatalf("parseJWT: %v", err)
	}
	if claims.PlayerID == nil || *claims.PlayerID != 7 {
		t.Errorf("PlayerID in JWT: want 7, got %v", claims.PlayerID)
	}
}

func TestHandleCallback_InvalidHash(t *testing.T) {
	h := testAuthHandler(t, nil)

	params := buildTelegramParams("bot-token", 42, "Alice")
	params["hash"] = strings.Repeat("0", 64) // valid hex length, wrong value
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback"+paramsToQuery(params), nil)
	w := httptest.NewRecorder()
	h.handleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid hash, got %d", w.Code)
	}
}

func TestHandleCallback_SecureCookieOverProxy(t *testing.T) {
	h := testAuthHandler(t, nil)

	params := buildTelegramParams("bot-token", 42, "Alice")
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback"+paramsToQuery(params), nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	h.handleCallback(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302, got %d", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName && !c.Secure {
			t.Error("Secure must be true when X-Forwarded-Proto is https")
		}
	}
}

// ── handleMe ──────────────────────────────────────────────────────────────────

func TestHandleMe_NoCookie(t *testing.T) {
	h := testAuthHandler(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	w := httptest.NewRecorder()
	h.handleMe(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401 without cookie, got %d", w.Code)
	}
}

func TestHandleMe_ValidCookie(t *testing.T) {
	h := testAuthHandler(t, nil)
	playerID := int64(5)
	token, err := issueJWT(h.jwtSecret, JWTClaims{
		TelegramID: 42,
		PlayerID:   &playerID,
		FirstName:  "Alice",
		Username:   "alice",
		Exp:        time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("issueJWT: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	h.handleMe(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if got, _ := resp["first_name"].(string); got != "Alice" {
		t.Errorf("first_name: want Alice, got %q", got)
	}
	if got, _ := resp["username"].(string); got != "alice" {
		t.Errorf("username: want alice, got %q", got)
	}
	// player_id is encoded as a float64 in JSON unmarshalling into any.
	if pid, ok := resp["player_id"].(float64); !ok || int64(pid) != 5 {
		t.Errorf("player_id: want 5, got %v", resp["player_id"])
	}
}

func TestHandleMe_IsServerOwner_LiveConfig(t *testing.T) {
	// User 42 is a server owner in live config — handleMe must reflect that
	// regardless of the JWT claim value.
	mgmt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(mgmt.Close)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	h := NewAuthHandler("bot-token", "TestBot", "jwt-secret-32-bytes-long-xxxxxxx", mgmt.URL, "mgmt-secret",
		map[int64]bool{42: true}, logger)

	token, err := issueJWT(h.jwtSecret, JWTClaims{
		TelegramID: 42,
		FirstName:  "Alice",
		Exp:        time.Now().Add(time.Hour).Unix(),
		// IsServerOwner deliberately omitted (false) — live config must win
	})
	if err != nil {
		t.Fatalf("issueJWT: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	h.handleMe(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got, _ := resp["is_server_owner"].(bool); !got {
		t.Errorf("is_server_owner: want true for user in serverOwnerIDs, got %v", resp["is_server_owner"])
	}
}

func TestHandleMe_IsServerOwner_StaleJWT(t *testing.T) {
	// User 42 has IsServerOwner=true in the JWT but is NOT in the live config.
	// handleMe must return false (live config wins over stale claim).
	mgmt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(mgmt.Close)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	h := NewAuthHandler("bot-token", "TestBot", "jwt-secret-32-bytes-long-xxxxxxx", mgmt.URL, "mgmt-secret",
		map[int64]bool{}, logger) // user 42 NOT in the map

	token, err := issueJWT(h.jwtSecret, JWTClaims{
		TelegramID:    42,
		FirstName:     "Alice",
		Exp:           time.Now().Add(time.Hour).Unix(),
		IsServerOwner: true, // stale claim that should be ignored
	})
	if err != nil {
		t.Fatalf("issueJWT: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	h.handleMe(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got, _ := resp["is_server_owner"].(bool); got {
		t.Errorf("is_server_owner: want false when user removed from serverOwnerIDs, got true")
	}
}

func TestHandleMe_ExpiredCookie(t *testing.T) {
	h := testAuthHandler(t, nil)
	token, _ := issueJWT(h.jwtSecret, JWTClaims{
		TelegramID: 42,
		FirstName:  "Alice",
		Exp:        time.Now().Add(-time.Minute).Unix(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	h.handleMe(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401 for expired token, got %d", w.Code)
	}
}

func TestHandleMe_TamperedCookie(t *testing.T) {
	h := testAuthHandler(t, nil)
	token, _ := issueJWT(h.jwtSecret, JWTClaims{
		TelegramID: 42,
		FirstName:  "Alice",
		Exp:        time.Now().Add(time.Hour).Unix(),
	})
	parts := strings.Split(token, ".")
	payload := []byte(parts[1])
	payload[0] ^= 0x01
	tampered := parts[0] + "." + string(payload) + "." + parts[2]

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tampered})
	w := httptest.NewRecorder()
	h.handleMe(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401 for tampered token, got %d", w.Code)
	}
}

// ── handleLogout ──────────────────────────────────────────────────────────────

func TestHandleLogout(t *testing.T) {
	h := testAuthHandler(t, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "sometoken"})
	w := httptest.NewRecorder()
	h.handleLogout(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("want 204, got %d", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name != sessionCookieName {
			continue
		}
		if c.MaxAge != -1 {
			t.Errorf("MaxAge: want -1 (delete), got %d", c.MaxAge)
		}
		if !c.Expires.Before(time.Now()) {
			t.Errorf("Expires should be in the past, got %v", c.Expires)
		}
	}
}

// ── isSecureRequest ───────────────────────────────────────────────────────────

func TestIsSecureRequest(t *testing.T) {
	cases := []struct {
		name  string
		proto string // X-Forwarded-Proto value; empty means header not set
		want  bool
	}{
		{"plain HTTP, no header", "", false},
		{"X-Forwarded-Proto: https", "https", true},
		{"X-Forwarded-Proto: http", "http", false},
		{"X-Forwarded-Proto: mixed case", "HTTPS", false}, // must be exact match
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.proto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			if got := isSecureRequest(req); got != tc.want {
				t.Errorf("isSecureRequest: want %v, got %v", tc.want, got)
			}
		})
	}
}
