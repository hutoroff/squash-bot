package webserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hutoroff/squash-bot/internal/discovery"
	"github.com/hutoroff/squash-bot/internal/observability"
)

type ManagementTarget struct {
	staticURL   string
	resolver    discovery.Resolver
	requirement discovery.Requirement
	metrics     *observability.DependencyMetrics
}

func NewStaticManagementTarget(rawURL string) *ManagementTarget {
	return &ManagementTarget{staticURL: strings.TrimRight(rawURL, "/")}
}

func NewDiscoveredManagementTarget(resolver discovery.Resolver, requirement discovery.Requirement) *ManagementTarget {
	return &ManagementTarget{resolver: resolver, requirement: requirement}
}

func (t *ManagementTarget) SetDependencyMetrics(metrics *observability.DependencyMetrics) {
	t.metrics = metrics
}

func (t *ManagementTarget) Resolve(ctx context.Context) (string, error) {
	if t.resolver == nil {
		return t.staticURL, nil
	}
	endpoint, err := discovery.ResolveOne(ctx, t.resolver, t.requirement)
	if err != nil {
		return "", fmt.Errorf("resolve management service: %w", err)
	}
	return strings.TrimRight(endpoint.API, "/"), nil
}

func (t *ManagementTarget) requestBaseURL() string {
	if t.resolver == nil {
		return t.staticURL
	}
	return "http://management.discovery"
}

func newTargetHTTPClient(target *ManagementTarget, timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &http.Client{Timeout: timeout, Transport: targetTransport{target: target, base: transport, tracker: discovery.NewEndpointTracker(transport.CloseIdleConnections)}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type targetTransport struct {
	target  *ManagementTarget
	base    http.RoundTripper
	tracker *discovery.EndpointTracker
}

func (t targetTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	started := time.Now()
	baseURL, err := t.target.Resolve(request.Context())
	if err != nil {
		if t.target.metrics != nil {
			t.target.metrics.Observe("management", 0, err, started)
		}
		return nil, err
	}
	t.tracker.Observe(baseURL)
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	clone := request.Clone(request.Context())
	clonedURL := *request.URL
	clonedURL.Scheme = parsed.Scheme
	clonedURL.Host = parsed.Host
	clone.URL = &clonedURL
	response, err := t.base.RoundTrip(clone)
	if err != nil {
		discovery.Invalidate(t.target.resolver, t.target.requirement)
	}
	if t.target.metrics != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.target.metrics.Observe("management", status, err, started)
	}
	return response, err
}

// mgmtClient holds the management-service connection details shared by every
// JSON API handler, plus the proxy and authorization helpers built on them.
type mgmtClient struct {
	auth       *AuthHandler
	mgmtTarget *ManagementTarget
	mgmtSecret string
	httpClient *http.Client
}

func newMgmtClient(auth *AuthHandler, mgmtURL, mgmtSecret string) mgmtClient {
	return newMgmtClientWithTarget(auth, NewStaticManagementTarget(mgmtURL), mgmtSecret)
}

func newMgmtClientWithTarget(auth *AuthHandler, target *ManagementTarget, mgmtSecret string) mgmtClient {
	return mgmtClient{
		auth:       auth,
		mgmtTarget: target,
		mgmtSecret: mgmtSecret,
		httpClient: newTargetHTTPClient(target, 10*time.Second),
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
	baseURL, err := c.mgmtTarget.Resolve(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "upstream unavailable")
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), method, baseURL+path, rdr)
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
	baseURL, err := c.mgmtTarget.Resolve(r.Context())
	if err != nil {
		return http.StatusServiceUnavailable, err
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, baseURL+path, nil)
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
// administer in Telegram. Both cases are resolved by a single management call
// — /admin-groups already returns every group for an owner — so there is no
// local owner shortcut here; management is the sole source of truth.
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

	var groups []struct {
		ChatID int64 `json:"chat_id"`
	}
	status, err := c.get(r, fmt.Sprintf("/api/v1/users/%d/admin-groups", claims.UserID), &groups)
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
		"actor_user_id": claims.UserID,
		"actor_display": strings.TrimSpace(claims.FirstName + " " + claims.LastName),
	}
}

// actorQuery returns the audit actor params the management service expects on
// DELETE endpoints, which carry no body.
func actorQuery(claims *JWTClaims) url.Values {
	return url.Values{
		"actor_user_id": {strconv.FormatInt(claims.UserID, 10)},
		"actor_display": {strings.TrimSpace(claims.FirstName + " " + claims.LastName)},
	}
}

// decodeWithActor decodes the client's JSON body into a map and overwrites the
// actor fields (and any extra forced fields) so a client can never spoof them.
// Writes 400 and returns false on an unparseable body.
func decodeWithActor(w http.ResponseWriter, r *http.Request, claims *JWTClaims, forced map[string]any) ([]byte, bool) {
	body := map[string]any{}
	// body stays nil on a literal `null`, which would panic on assignment below.
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body == nil {
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
