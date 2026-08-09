package webserver

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const sessionCookieName = "session"
const tokenExpiry = 7 * 24 * time.Hour

// AuthHandler handles Telegram Login Widget authentication and session management.
type AuthHandler struct {
	botToken   string
	botName    string
	jwtSecret  string
	mgmtURL    string
	mgmtSecret string
	httpClient *http.Client
	logger     *slog.Logger
}

// NewAuthHandler creates an AuthHandler.
func NewAuthHandler(botToken, botName, jwtSecret, mgmtURL, mgmtSecret string, logger *slog.Logger) *AuthHandler {
	return &AuthHandler{
		botToken:   botToken,
		botName:    botName,
		jwtSecret:  jwtSecret,
		mgmtURL:    mgmtURL,
		mgmtSecret: mgmtSecret,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		logger:     logger,
	}
}

// JWTClaims holds the user information stored in the session JWT.
// UserID is the canonical identity — the presence check in parseJWT rejects
// any pre-v2 cookie (which never had "uid"), forcing a clean re-login.
type JWTClaims struct {
	UserID    int64  `json:"uid"`
	PlayerID  *int64 `json:"pid,omitempty"`
	FirstName string `json:"fn"`
	LastName  string `json:"ln,omitempty"`
	Username  string `json:"un,omitempty"`
	PhotoURL  string `json:"ph,omitempty"`
	Exp       int64  `json:"exp"`
}

// resolvedIdentity mirrors the management service's POST /api/v1/identities/resolve response.
type resolvedIdentity struct {
	UserID        int64  `json:"user_id"`
	PlayerID      *int64 `json:"player_id"`
	DisplayName   string `json:"display_name"`
	IsServerOwner bool   `json:"is_server_owner"`
}

// resolveUser finds-or-creates the canonical user for a Telegram identity.
func (a *AuthHandler) resolveUser(ctx context.Context, telegramID int64, username, firstName, lastName, photoURL string) (*resolvedIdentity, error) {
	body, err := json.Marshal(map[string]string{
		"provider":    "telegram",
		"external_id": strconv.FormatInt(telegramID, 10),
		"username":    username,
		"first_name":  firstName,
		"last_name":   lastName,
		"photo_url":   photoURL,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.mgmtURL+"/api/v1/identities/resolve", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.mgmtSecret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("management service returned %d", resp.StatusCode)
	}

	var out resolvedIdentity
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// getUser fetches the canonical user record from the management service.
func (a *AuthHandler) getUser(ctx context.Context, userID int64) (*resolvedIdentity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/v1/users/%d", a.mgmtURL, userID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.mgmtSecret)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("management service returned %d", resp.StatusCode)
	}

	var out struct {
		UserID        int64 `json:"user_id"`
		IsServerOwner bool  `json:"is_server_owner"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &resolvedIdentity{UserID: out.UserID, IsServerOwner: out.IsServerOwner}, nil
}

// handleCallback handles GET /api/auth/callback.
// Telegram redirects here after the user approves the Login Widget.
func (a *AuthHandler) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	params := make(map[string]string, len(q))
	for k, vs := range q {
		if len(vs) > 0 {
			params[k] = vs[0]
		}
	}

	if !verifyTelegramAuth(a.botToken, params) {
		http.Error(w, "invalid auth data", http.StatusBadRequest)
		return
	}

	telegramID, err := strconv.ParseInt(params["id"], 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	resolved, err := a.resolveUser(r.Context(), telegramID, params["username"], params["first_name"], params["last_name"], params["photo_url"])
	if err != nil {
		a.logger.Error("resolveUser", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	claims := JWTClaims{
		UserID:    resolved.UserID,
		PlayerID:  resolved.PlayerID,
		FirstName: params["first_name"],
		LastName:  params["last_name"],
		Username:  params["username"],
		PhotoURL:  params["photo_url"],
		Exp:       time.Now().Add(tokenExpiry).Unix(),
	}

	token, err := issueJWT(a.jwtSecret, claims)
	if err != nil {
		a.logger.Error("issueJWT", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(tokenExpiry),
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

// handleMe handles GET /api/auth/me.
// Returns 200 with the current user's info if authenticated, or 401 if not.
// is_server_owner is always read live from the management service so a role
// change takes effect immediately, without waiting for the session to expire.
func (a *AuthHandler) handleMe(w http.ResponseWriter, r *http.Request) {
	claims, err := a.claimsFromRequest(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`)) //nolint:errcheck
		return
	}

	user, err := a.getUser(r.Context(), claims.UserID)
	if err != nil {
		a.logger.Error("handleMe: getUser", "user_id", claims.UserID, "err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"upstream unavailable"}`)) //nolint:errcheck
		return
	}

	type userResponse struct {
		UserID        int64  `json:"user_id"`
		PlayerID      *int64 `json:"player_id,omitempty"`
		FirstName     string `json:"first_name"`
		LastName      string `json:"last_name,omitempty"`
		Username      string `json:"username,omitempty"`
		PhotoURL      string `json:"photo_url,omitempty"`
		IsServerOwner bool   `json:"is_server_owner,omitempty"`
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(userResponse{ //nolint:errcheck
		UserID:        claims.UserID,
		PlayerID:      claims.PlayerID,
		FirstName:     claims.FirstName,
		LastName:      claims.LastName,
		Username:      claims.Username,
		PhotoURL:      claims.PhotoURL,
		IsServerOwner: user.IsServerOwner,
	})
}

// handleLogout handles POST /api/auth/logout.
func (a *AuthHandler) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// isSecureRequest reports whether the request arrived over HTTPS, either
// directly (r.TLS != nil) or via a TLS-terminating reverse proxy that sets
// the de-facto standard X-Forwarded-Proto header.
func isSecureRequest(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// claimsFromRequest extracts and validates the JWT from the session cookie.
func (a *AuthHandler) claimsFromRequest(r *http.Request) (*JWTClaims, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil, err
	}
	return parseJWT(a.jwtSecret, cookie.Value)
}

// verifyTelegramAuth validates the Telegram Login Widget data hash.
// See https://core.telegram.org/widgets/login#checking-authorization
func verifyTelegramAuth(botToken string, params map[string]string) bool {
	hash, ok := params["hash"]
	if !ok {
		return false
	}

	authDateStr, ok := params["auth_date"]
	if !ok {
		return false
	}
	authDate, err := strconv.ParseInt(authDateStr, 10, 64)
	if err != nil || time.Now().Unix()-authDate > 86400 {
		return false
	}

	// Build sorted key=value check string (excluding "hash").
	keys := make([]string, 0, len(params)-1)
	for k := range params {
		if k != "hash" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + params[k]
	}
	checkString := strings.Join(parts, "\n")

	// key = SHA256(bot_token); signature = hex(HMAC-SHA256(key, checkString))
	keyHash := sha256.Sum256([]byte(botToken))
	mac := hmac.New(sha256.New, keyHash[:])
	mac.Write([]byte(checkString))
	expectedHash := hex.EncodeToString(mac.Sum(nil))

	return subtle.ConstantTimeCompare([]byte(expectedHash), []byte(hash)) == 1
}

// issueJWT creates a signed HS256 JWT containing the given claims.
func issueJWT(secret string, claims JWTClaims) (string, error) {
	type jwtHeader struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	headerBytes, err := json.Marshal(jwtHeader{Alg: "HS256", Typ: "JWT"})
	if err != nil {
		return "", err
	}
	header := base64.RawURLEncoding.EncodeToString(headerBytes)

	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	payloadEnc := base64.RawURLEncoding.EncodeToString(payload)
	signing := header + "." + payloadEnc
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signing))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signing + "." + sig, nil
}

// parseJWT validates a JWT's signature and expiry, returning its claims.
// A missing UserID (uid == 0) rejects pre-v2 cookies, which never carried
// that field, forcing a clean re-login instead of resolving to user 0.
func parseJWT(secret, token string) (*JWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("malformed token")
	}
	signing := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signing))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(expectedSig), []byte(parts[2])) != 1 {
		return nil, fmt.Errorf("invalid signature")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	var c JWTClaims
	if err := json.Unmarshal(payloadBytes, &c); err != nil {
		return nil, fmt.Errorf("unmarshal claims: %w", err)
	}
	if c.UserID == 0 {
		return nil, fmt.Errorf("stale session: missing uid")
	}
	if time.Now().Unix() > c.Exp {
		return nil, fmt.Errorf("token expired")
	}
	return &c, nil
}
