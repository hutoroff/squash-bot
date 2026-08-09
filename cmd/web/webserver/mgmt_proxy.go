package webserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// mgmtClient holds the management-service connection details shared by every
// JSON API handler, plus the proxy and authorization helpers built on them.
type mgmtClient struct {
	auth       *AuthHandler
	mgmtURL    string
	mgmtSecret string
	httpClient *http.Client
}

func newMgmtClient(auth *AuthHandler, mgmtURL, mgmtSecret string) mgmtClient {
	return mgmtClient{
		auth:       auth,
		mgmtURL:    mgmtURL,
		mgmtSecret: mgmtSecret,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// writeAPIError writes {"error": msg} with the given status.
func writeAPIError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
}

// claims returns the session claims, writing 401 and returning false when the
// session cookie is missing or invalid.
func (c mgmtClient) claims(w http.ResponseWriter, r *http.Request) (*JWTClaims, bool) {
	claims, err := c.auth.claimsFromRequest(r)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized")
		return nil, false
	}
	return claims, true
}

// proxy forwards a request to the management service and streams the upstream
// status code and body back verbatim. path must start with "/" and may carry a
// query string. body may be nil.
func (c mgmtClient) proxy(w http.ResponseWriter, r *http.Request, method, path string, body []byte) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(r.Context(), method, c.mgmtURL+path, rdr)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.mgmtSecret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "upstream unavailable")
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
}

// get fetches JSON from the management service and decodes it into out.
func (c mgmtClient) get(r *http.Request, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, c.mgmtURL+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.mgmtSecret)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		return resp.StatusCode, nil
	}
	return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
}

// authorizeGroup resolves the {chatID} path value and verifies the caller may
// manage it: server owners always may, everyone else only for groups they
// administer in Telegram (resolved by the management service, which caches).
// It writes the error response itself and returns ok=false when access is denied.
func (c mgmtClient) authorizeGroup(w http.ResponseWriter, r *http.Request) (claims *JWTClaims, chatID int64, ok bool) {
	claims, ok = c.claims(w, r)
	if !ok {
		return nil, 0, false
	}
	chatID, err := strconv.ParseInt(r.PathValue("chatID"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid chat id")
		return nil, 0, false
	}
	if c.auth.serverOwnerIDs[claims.TelegramID] {
		return claims, chatID, true
	}

	var groups []struct {
		ChatID int64 `json:"chat_id"`
	}
	status, err := c.get(r, fmt.Sprintf("/api/v1/admins/%d/groups", claims.TelegramID), &groups)
	if err != nil || status != http.StatusOK {
		writeAPIError(w, http.StatusBadGateway, "upstream unavailable")
		return nil, 0, false
	}
	for _, g := range groups {
		if g.ChatID == chatID {
			return claims, chatID, true
		}
	}
	writeAPIError(w, http.StatusForbidden, "forbidden")
	return nil, 0, false
}

// actorFields returns the audit actor fields the management service expects in
// mutation bodies. Always derived from the JWT, never from the request body.
func actorFields(claims *JWTClaims) map[string]any {
	return map[string]any{
		"actor_telegram_id": claims.TelegramID,
		"actor_display":     strings.TrimSpace(claims.FirstName + " " + claims.LastName),
	}
}

// actorQuery returns the audit actor params the management service expects on
// DELETE endpoints, which carry no body.
func actorQuery(claims *JWTClaims) url.Values {
	return url.Values{
		"actor_tg_id":   {strconv.FormatInt(claims.TelegramID, 10)},
		"actor_display": {strings.TrimSpace(claims.FirstName + " " + claims.LastName)},
	}
}

// decodeWithActor decodes the client's JSON body into a map and overwrites the
// actor fields (and any extra forced fields) so a client can never spoof them.
// Writes 400 and returns false on an unparseable body.
func decodeWithActor(w http.ResponseWriter, r *http.Request, claims *JWTClaims, forced map[string]any) ([]byte, bool) {
	body := map[string]any{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid request body")
		return nil, false
	}
	for k, v := range actorFields(claims) {
		body[k] = v
	}
	for k, v := range forced {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid request body")
		return nil, false
	}
	return raw, true
}
