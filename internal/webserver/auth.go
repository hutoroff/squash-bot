package webserver

import (
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
type JWTClaims struct {
	TelegramID int64  `json:"tid"`
	PlayerID   *int64 `json:"pid,omitempty"`
	FirstName  string `json:"fn"`
	LastName   string `json:"ln,omitempty"`
	Username   string `json:"un,omitempty"`
	PhotoURL   string `json:"ph,omitempty"`
	Exp        int64  `json:"exp"`
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

	claims := JWTClaims{
		TelegramID: telegramID,
		FirstName:  params["first_name"],
		LastName:   params["last_name"],
		Username:   params["username"],
		PhotoURL:   params["photo_url"],
		Exp:        time.Now().Add(tokenExpiry).Unix(),
	}

	if pid, err := a.lookupPlayer(r.Context(), telegramID); err == nil && pid != nil {
		claims.PlayerID = pid
	} else if err != nil {
		a.logger.Debug("lookupPlayer", "telegram_id", telegramID, "err", err)
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
func (a *AuthHandler) handleMe(w http.ResponseWriter, r *http.Request) {
	claims, err := a.claimsFromRequest(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`)) //nolint:errcheck
		return
	}

	type userResponse struct {
		TelegramID int64  `json:"telegram_id"`
		PlayerID   *int64 `json:"player_id,omitempty"`
		FirstName  string `json:"first_name"`
		LastName   string `json:"last_name,omitempty"`
		Username   string `json:"username,omitempty"`
		PhotoURL   string `json:"photo_url,omitempty"`
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(userResponse{ //nolint:errcheck
		TelegramID: claims.TelegramID,
		PlayerID:   claims.PlayerID,
		FirstName:  claims.FirstName,
		LastName:   claims.LastName,
		Username:   claims.Username,
		PhotoURL:   claims.PhotoURL,
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

// lookupPlayer fetches the player record from the management service.
// Returns the player ID if found, nil if the player hasn't used the bot yet (404).
func (a *AuthHandler) lookupPlayer(ctx context.Context, telegramID int64) (*int64, error) {
	url := fmt.Sprintf("%s/api/v1/players/%d", a.mgmtURL, telegramID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.mgmtSecret)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("management service returned %d", resp.StatusCode)
	}

	var p struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return nil, err
	}
	return &p.ID, nil
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
	if time.Now().Unix() > c.Exp {
		return nil, fmt.Errorf("token expired")
	}
	return &c, nil
}
