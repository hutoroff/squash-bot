package eversports

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// errUnauthorized is returned (wrapped) by client methods when Eversports
// signals an expired or missing session (HTTP 401, or a redirect to the login
// page). Callers should re-login and retry.
var errUnauthorized = errors.New("eversports: unauthorized")

const (
	baseURL = "https://www.eversports.de"

	// graphqlEndpoint is the single GraphQL gateway used by the Eversports frontend.
	graphqlEndpoint = "/api/checkout"

	// selfEndpoint returns the authenticated user's profile including the legacy
	// numeric user ID required by the /api/user/activities endpoint.
	selfEndpoint = "/u/self"

	// loginMutation is the GraphQL mutation captured from browser DevTools.
	loginMutation = `mutation LoginCredentialLogin($params: AuthParamsInput!, $credentials: CredentialLoginInput!) {
  credentialLogin(params: $params, credentials: $credentials) {
    ... on AuthResult {
      apiToken
      user {
        id
        __typename
      }
      __typename
    }
    ... on ExpectedErrors {
      errors {
        id
        message
        path
        __typename
      }
      __typename
    }
    __typename
  }
}`
)

// browserHeaders are sent with every request to mimic a real browser and pass
// Cloudflare's bot-detection checks.
var browserHeaders = map[string]string{
	"User-Agent":      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Accept":          "application/json, text/plain, */*",
	"Accept-Language": "de-DE,de;q=0.9,en;q=0.8",
	"Referer":         baseURL + "/",
	"Origin":          baseURL,
}

// htmlAccept is the Accept header a browser sends when navigating to a page
// (as opposed to an XHR/fetch request). Using the correct value avoids servers
// returning JSON or a redirect instead of the rendered HTML.
const htmlAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8"

// Client interacts with the Eversports website via its internal GraphQL API
// and HTML pages. Authentication is cookie-based: after login the `et` session
// cookie is stored in the http.Client's CookieJar and sent automatically.
type Client struct {
	http         *http.Client
	email        string
	password     string
	bookingsPath string // path for the SSR bookings page (used by debug-page endpoint)

	loginMu  sync.Mutex
	loggedIn atomic.Bool
	userID   atomic.Value // string — GraphQL UUID from login response
	// activitiesUserID is the legacy numeric user ID fetched from GET /u/self
	// after login. It is required by the GET /api/user/activities endpoint.
	activitiesUserID atomic.Value // string

	// bookingMu serialises CreateBooking calls. The Eversports checkout flow is
	// a three-step sequence (reserve → pay → create-from-booking) where step 3
	// implicitly operates on the server's "most recently created booking" for
	// the session. Allowing two concurrent calls to interleave could attach the
	// match record to the wrong booking.
	bookingMu sync.Mutex

	logger *slog.Logger
}

// New creates a new Eversports client.
// bookingsPath is the URL path to the user's bookings page (e.g. "/user/bookings"),
// used only by the debug-page diagnostic endpoint.
func New(email, password, bookingsPath string, logger *slog.Logger) *Client {
	jar, _ := cookiejar.New(nil) // never errors with nil options
	return &Client{
		http: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		},
		email:        email,
		password:     password,
		bookingsPath: bookingsPath,
		logger:       logger,
	}
}

// Login authenticates with Eversports via the GraphQL login mutation.
// On success, the `et` session cookie is stored in the client's cookie jar
// and all subsequent requests will be authenticated automatically.
func (c *Client) Login(ctx context.Context) error {
	payload := gqlRequest{
		OperationName: "LoginCredentialLogin",
		Variables: map[string]any{
			"credentials": map[string]any{
				"email":    c.email,
				"password": c.password,
			},
			"params": map[string]any{
				"origin":                   "ORIGIN_MARKETPLACE",
				"corporatePartner":         nil,
				"corporateInvitationToken": nil,
				"queryString":              "?origin=eversport&redirectPath=%2F",
				"region":                   "DE",
			},
		},
		Query: loginMutation,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("eversports: marshal login payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+graphqlEndpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("eversports: create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	setBrowserHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("eversports: login request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("eversports: login HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	// The response body may be empty; the session is established via the `et`
	// cookie that the CookieJar stores automatically. We parse the body only
	// when present to surface any GraphQL-level errors (e.g. wrong password).
	respBody, _ := io.ReadAll(resp.Body)
	if len(respBody) > 0 {
		var gqlResp gqlLoginResponse
		if err := json.Unmarshal(respBody, &gqlResp); err == nil {
			cl := gqlResp.Data.CredentialLogin
			if len(cl.Errors) > 0 {
				return fmt.Errorf("eversports: login error: %s", cl.Errors[0].Message)
			}
			if len(gqlResp.Errors) > 0 {
				return fmt.Errorf("eversports: graphql error: %s", gqlResp.Errors[0].Message)
			}
			if cl.User.ID != "" {
				c.userID.Store(cl.User.ID)
			}
		}
	}

	// Confirm that the `et` cookie was actually set by the server.
	if !c.hasCookie("et") {
		return fmt.Errorf("eversports: login succeeded HTTP-wise but no 'et' session cookie was returned")
	}

	c.loggedIn.Store(true)
	c.logger.Info("eversports login successful")
	return nil
}

// fetchSelfUserID calls GET /u/self to retrieve the authenticated user's legacy
// numeric ID and stores it in activitiesUserID for use by GetBookings.
func (c *Client) fetchSelfUserID(ctx context.Context) error {
	resp, err := c.doAuthed(ctx, http.MethodGet, baseURL+selfEndpoint, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var selfResp selfResponse
	if err := json.Unmarshal(respBytes, &selfResp); err != nil {
		return fmt.Errorf("decode /u/self response: %w", err)
	}
	if selfResp.Data.User.ID == 0 {
		return fmt.Errorf("user ID missing in /u/self response")
	}
	c.activitiesUserID.Store(fmt.Sprintf("%d", selfResp.Data.User.ID))
	c.logger.Debug("eversports user ID fetched", "userID", selfResp.Data.User.ID)
	return nil
}

// EnsureLoggedIn logs in if the client does not already hold a valid session.
// It is safe for concurrent use: at most one login attempt runs at a time.
func (c *Client) EnsureLoggedIn(ctx context.Context) error {
	if c.loggedIn.Load() {
		return nil // fast path — already authenticated
	}
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	if c.loggedIn.Load() { // double-check after acquiring the lock
		return nil
	}
	return c.Login(ctx)
}

// invalidateSession clears the logged-in flag so the next EnsureLoggedIn call
// triggers a fresh login. Called when an API response signals session expiry.
func (c *Client) invalidateSession() {
	c.loggedIn.Store(false)
}

// doAuthed executes an authenticated JSON/XHR request. The CookieJar carries
// the `et` session cookie automatically; no Authorization header is needed.
// When body is non-nil, Content-Type is set to application/json.
func (c *Client) doAuthed(ctx context.Context, method, rawURL string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	setBrowserHeaders(req)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}

// doAuthedPage fetches an HTML page with the session cookie. It uses a
// browser-style Accept header so the server returns rendered HTML rather than
// JSON or a redirect. It also detects silent redirects: if the final URL after
// following redirects differs from the requested URL, an error is returned so
// callers can surface a clear "session expired / wrong path" message.
func (c *Client) doAuthedPage(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	setBrowserHeaders(req)
	req.Header.Set("Accept", htmlAccept)
	// Page navigation does not set Origin/X-Requested-With.
	req.Header.Del("Origin")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}

	// Detect silent redirect: http.Client follows 3xx automatically, so by the
	// time we see the response it is always 200. We compare the final URL to
	// the original to catch login-page redirects.
	if finalURL := resp.Request.URL.String(); finalURL != rawURL {
		resp.Body.Close()
		return nil, fmt.Errorf("%w: redirected from %s to %s (session may have expired or EVERSPORTS_BOOKINGS_PATH is wrong)", errUnauthorized, rawURL, finalURL)
	}

	return resp, nil
}

// hasCookie returns true if the CookieJar holds a cookie with the given name
// for the base Eversports URL.
func (c *Client) hasCookie(name string) bool {
	u, _ := url.Parse(baseURL)
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == name {
			return true
		}
	}
	return false
}

func setBrowserHeaders(req *http.Request) {
	for k, v := range browserHeaders {
		req.Header.Set(k, v)
	}
}
