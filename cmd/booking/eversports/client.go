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
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// errUnauthorized is returned (wrapped) by client methods when Eversports
// signals an expired or missing session (HTTP 401, or a redirect to the login
// page). Callers should re-login and retry.
var errUnauthorized = errors.New("eversports: unauthorized")

// ErrNotFound is returned when a requested resource does not exist (e.g. an
// unknown facility slug). Callers can test for it with errors.Is.
var ErrNotFound = errors.New("eversports: not found")

const (
	defaultBaseURL = "https://www.eversports.de"

	// graphqlEndpoint is the single GraphQL gateway used by the Eversports frontend.
	graphqlEndpoint = "/api/checkout"

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
	"Referer":         defaultBaseURL + "/",
	"Origin":          defaultBaseURL,
}

// htmlAccept is the Accept header a browser sends when navigating to a page
// (as opposed to an XHR/fetch request). Using the correct value avoids servers
// returning JSON or a redirect instead of the rendered HTML.
const htmlAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8"

// Client interacts with the Eversports website via its internal GraphQL API
// and HTML pages. Authentication is cookie-based: after login the `et` session
// cookie is stored in the http.Client's CookieJar and sent automatically.
type Client struct {
	http *http.Client

	// baseURL is defaultBaseURL in production; tests override it with an
	// httptest.Server URL. It is set once in New and never mutated afterwards.
	baseURL string

	email    string
	password string
	loginMu  sync.Mutex
	loggedIn atomic.Bool
	userID   atomic.Value // string — GraphQL UUID from login response

	// bookingMu serialises CreateBooking calls. The Eversports checkout flow is
	// a three-step sequence (reserve → pay → create-from-booking) where step 3
	// implicitly operates on the server's "most recently created booking" for
	// the session. Allowing two concurrent calls to interleave could attach the
	// match record to the wrong booking.
	bookingMu sync.Mutex

	logger *slog.Logger
}

// New creates a new Eversports client.
func New(email, password string, logger *slog.Logger) *Client {
	jar, _ := cookiejar.New(nil) // never errors with nil options
	return &Client{
		http: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		},
		baseURL:  defaultBaseURL,
		email:    email,
		password: password,
		logger:   logger,
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+graphqlEndpoint, bytes.NewReader(body))
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
		return fmt.Errorf("eversports: login HTTP %d: %s", resp.StatusCode, bodySnippet(respBody))
	}

	// The response body may be empty; the session is established via the `et`
	// cookie that the CookieJar stores automatically. We parse the body only
	// when present to surface any GraphQL-level errors (e.g. wrong password).
	respBody, _ := io.ReadAll(resp.Body)
	if len(respBody) > 0 {
		var gqlResp gqlLoginResponse
		if err := json.Unmarshal(respBody, &gqlResp); err != nil {
			// An unparseable body is not fatal (the session lives in the cookie),
			// but it is worth surfacing: it is how a Cloudflare challenge or a
			// changed API shape first becomes visible.
			c.logger.Warn("eversports: login response body is not valid JSON; relying on et-cookie check",
				"err", err, "body", bodySnippet(respBody))
		} else {
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

// EnsureLoggedIn logs in if the client does not already hold a valid session.
// It is safe for concurrent use: at most one login attempt runs at a time.
//
// The `et` cookie must still be present for the session to count as valid: it
// expires after ~30 days, and the CookieJar drops expired cookies silently. A
// flag-only check would leave the client convinced it is authenticated while
// sending cookieless requests forever.
func (c *Client) EnsureLoggedIn(ctx context.Context) error {
	if c.loggedIn.Load() && c.hasCookie("et") {
		return nil // fast path — authenticated and the session cookie is still live
	}
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	if c.loggedIn.Load() && c.hasCookie("et") { // double-check after acquiring the lock
		return nil
	}
	return c.Login(ctx)
}

// invalidateSession clears the logged-in flag so the next EnsureLoggedIn call
// triggers a fresh login. Called when an API response signals session expiry.
//
// The `et` cookie is dropped as well. Eversports can invalidate a session
// server-side while the cookie is still unexpired locally; leaving it in the jar
// would make Login's "did the server set an et cookie?" success check pass on
// the stale value, turning the re-login into a silent no-op.
func (c *Client) invalidateSession() {
	c.loggedIn.Store(false)
	if u, err := url.Parse(c.baseURL); err == nil {
		// MaxAge < 0 tells the jar to delete the cookie. Mutating the jar is
		// safe for concurrent use; replacing c.http.Jar would not be.
		c.http.Jar.SetCookies(u, []*http.Cookie{{Name: "et", Value: "", Path: "/", MaxAge: -1}})
	}
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

// hasCookie returns true if the CookieJar holds a cookie with the given name
// for the base Eversports URL.
func (c *Client) hasCookie(name string) bool {
	u, _ := url.Parse(c.baseURL)
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

// ─── Response inspection helpers ──────────────────────────────────────────────

// bodySnippetLimit caps how much of a response body is embedded in errors and
// logs. Eversports login pages and Cloudflare challenges run to several KB.
const bodySnippetLimit = 300

// bodySnippet renders a response body for inclusion in an error or log entry:
// whitespace is collapsed so the result stays on one line, and the output is
// capped at bodySnippetLimit bytes. Truncation is byte-based and may split a
// multi-byte rune — acceptable for diagnostics.
func bodySnippet(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > bodySnippetLimit {
		return s[:bodySnippetLimit] + "…"
	}
	return s
}

// authErrorMessages are substrings of top-level GraphQL error messages that
// indicate a dead session rather than a business-rule rejection. Eversports
// answers a cookieless GraphQL request with HTTP 200 and "User is a non
// complete user" — it resolves the caller to an incomplete guest identity
// instead of returning 401.
var authErrorMessages = []string{
	"non complete user",
	"not authenticated",
	"unauthenticated",
	"unauthorized",
}

// isAuthErrorMessage reports whether a top-level GraphQL error message is
// auth-shaped, meaning the session should be re-established and the call retried.
func isAuthErrorMessage(msg string) bool {
	lower := strings.ToLower(msg)
	for _, needle := range authErrorMessages {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// gqlTopLevelError converts a top-level GraphQL error into an error value,
// wrapping errUnauthorized when the message is auth-shaped so that withAuth
// re-logs in and retries once. Retrying is safe: GraphQL rejects unauthenticated
// requests before the resolver runs, so the failed attempt had no side effects.
//
// Errors from the ExpectedErrors union (business rules such as "Eine Stornierung
// der Buchung ist nicht möglich") must NOT be routed through this helper — they
// are legitimate answers and retrying them is pointless.
func gqlTopLevelError(op, msg string) error {
	if isAuthErrorMessage(msg) {
		return fmt.Errorf("eversports: %s graphql error: %s: %w", op, msg, errUnauthorized)
	}
	return fmt.Errorf("eversports: %s graphql error: %s", op, msg)
}

// isHTMLResponse reports whether a body is an HTML page rather than the expected
// JSON. This happens when Eversports redirects an unauthenticated API request to
// the login page (the HTTP client follows the redirect and returns HTTP 200 with
// HTML), or when Cloudflare interposes a bot challenge.
func isHTMLResponse(b []byte) bool {
	return bytes.HasPrefix(bytes.TrimSpace(b), []byte("<")) || isCFChallenge(b)
}

// htmlAuthError returns nil when body is valid-looking JSON, and an
// errUnauthorized-wrapped error when it is an HTML page. Call it after the
// status check and before decoding, so an expired session surfaces as an auth
// failure that withAuth can recover from instead of an opaque decode error.
func htmlAuthError(op string, body []byte) error {
	if !isHTMLResponse(body) {
		return nil
	}
	return fmt.Errorf("eversports: %s returned HTML instead of JSON (session likely expired): %s: %w",
		op, bodySnippet(body), errUnauthorized)
}

// withAuth ensures the client is logged in, executes do(), and retries once on
// errUnauthorized. Used by every public method except CreateBooking, which has
// mid-flow 401 handling that cannot safely retry from step 1.
func withAuth[T any](ctx context.Context, c *Client, do func() (T, error)) (T, error) {
	if err := c.EnsureLoggedIn(ctx); err != nil {
		var zero T
		return zero, err
	}
	result, err := do()
	if err != nil && errors.Is(err, errUnauthorized) {
		c.invalidateSession()
		if loginErr := c.EnsureLoggedIn(ctx); loginErr != nil {
			var zero T
			return zero, loginErr
		}
		return do()
	}
	return result, err
}
