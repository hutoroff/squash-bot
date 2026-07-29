package eversports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─── Test harness ─────────────────────────────────────────────────────────────

// counters records how many times each faked Eversports endpoint was hit.
// Fields are atomic: the httptest handler runs on its own goroutine.
type counters struct {
	logins      atomic.Int64
	matches     atomic.Int64
	cancels     atomic.Int64
	step1       atomic.Int64
	payOffline  atomic.Int64
	createMatch atomic.Int64
}

// newTestClient returns a Client talking to an httptest server built from mux.
// The baseURL field is the seam that makes the HTTP layer testable — production
// code always uses defaultBaseURL.
func newTestClient(t *testing.T, mux *http.ServeMux) *Client {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := New("bot@example.com", "secret", slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.baseURL = srv.URL
	return c
}

// gqlHandler answers one GraphQL operation. call is the 1-based invocation
// count for that operation, so tests can script per-attempt responses.
type gqlHandler func(call int, w http.ResponseWriter)

// registerGraphQL wires the GraphQL gateway, dispatching on operationName.
// LoginCredentialLogin is always handled: it sets the `et` session cookie the
// real site sets, which is what the client's session checks rely on.
func registerGraphQL(mux *http.ServeMux, ctr *counters, handlers map[string]gqlHandler) {
	var mu sync.Mutex
	calls := make(map[string]int)

	mux.HandleFunc("POST "+graphqlEndpoint, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req gqlRequest
		_ = json.Unmarshal(body, &req)

		mu.Lock()
		calls[req.OperationName]++
		call := calls[req.OperationName]
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")

		switch req.OperationName {
		case "LoginCredentialLogin":
			ctr.logins.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "et", Value: fmt.Sprintf("session-%d", call), Path: "/"})
			fmt.Fprint(w, `{"data":{"credentialLogin":{"__typename":"AuthResult","apiToken":"tok","user":{"id":"user-1"}}}}`)
			return
		case "Match":
			ctr.matches.Add(1)
		case "CancelMatch":
			ctr.cancels.Add(1)
		}

		h, ok := handlers[req.OperationName]
		if !ok {
			w.WriteHeader(http.StatusNotImplemented)
			return
		}
		h(call, w)
	})
}

const cancelMatchSuccessJSON = `{"data":{"cancelMatch":{"__typename":"BallsportMatch","id":"match-1","state":"CANCELLED","relativeLink":"/match/match-1"}}}`

// loginPageHTML mimics what Eversports returns when an API request is redirected
// to the login page: HTTP 200 with an HTML document instead of JSON.
const loginPageHTML = `<!DOCTYPE html><html><head><title>Login</title></head><body>Bitte einloggen</body></html>`

// ─── Session liveness ─────────────────────────────────────────────────────────

// TestEnsureLoggedIn_ReloginsWhenSessionCookieMissing covers the root cause of
// the July 2026 outage: the client held loggedIn=true while the 30-day `et`
// cookie had expired out of the jar, so it never re-authenticated.
func TestEnsureLoggedIn_ReloginsWhenSessionCookieMissing(t *testing.T) {
	var ctr counters
	mux := http.NewServeMux()
	registerGraphQL(mux, &ctr, nil)
	c := newTestClient(t, mux)

	c.loggedIn.Store(true) // stale flag, empty cookie jar

	if err := c.EnsureLoggedIn(context.Background()); err != nil {
		t.Fatalf("EnsureLoggedIn: %v", err)
	}
	if got := ctr.logins.Load(); got != 1 {
		t.Errorf("logins = %d, want 1 (missing et cookie must trigger a re-login)", got)
	}
	if !c.hasCookie("et") {
		t.Error("expected the et cookie to be stored after login")
	}

	// With the cookie in place the fast path must not hit the network again.
	if err := c.EnsureLoggedIn(context.Background()); err != nil {
		t.Fatalf("second EnsureLoggedIn: %v", err)
	}
	if got := ctr.logins.Load(); got != 1 {
		t.Errorf("logins = %d, want 1 (live session must use the fast path)", got)
	}
}

// TestInvalidateSession_DropsSessionCookie guards the case where Eversports
// invalidates a session server-side while the cookie is still unexpired
// locally: a leftover cookie would make Login's success check pass without a
// fresh session.
func TestInvalidateSession_DropsSessionCookie(t *testing.T) {
	var ctr counters
	mux := http.NewServeMux()
	registerGraphQL(mux, &ctr, nil)
	c := newTestClient(t, mux)

	if err := c.EnsureLoggedIn(context.Background()); err != nil {
		t.Fatalf("EnsureLoggedIn: %v", err)
	}
	if !c.hasCookie("et") {
		t.Fatal("expected an et cookie after login")
	}

	c.invalidateSession()

	if c.hasCookie("et") {
		t.Error("expected the et cookie to be dropped by invalidateSession")
	}
	if c.loggedIn.Load() {
		t.Error("expected loggedIn to be false after invalidateSession")
	}
}

// ─── Auth-shaped GraphQL errors ───────────────────────────────────────────────

// TestCancelMatch_RetriesOnAuthShapedGraphQLError reproduces the exact
// production failure: HTTP 200 with a top-level "User is a non complete user"
// error. It must be treated as an expired session, not a permanent failure.
func TestCancelMatch_RetriesOnAuthShapedGraphQLError(t *testing.T) {
	var ctr counters
	mux := http.NewServeMux()
	registerGraphQL(mux, &ctr, map[string]gqlHandler{
		"CancelMatch": func(call int, w http.ResponseWriter) {
			if call == 1 {
				fmt.Fprint(w, `{"errors":[{"message":"User is a non complete user"}]}`)
				return
			}
			fmt.Fprint(w, cancelMatchSuccessJSON)
		},
	})
	c := newTestClient(t, mux)

	res, err := c.CancelMatch(context.Background(), "match-1")
	if err != nil {
		t.Fatalf("CancelMatch: unexpected error: %v", err)
	}
	if res.State != "CANCELLED" {
		t.Errorf("State = %q, want %q", res.State, "CANCELLED")
	}
	if got := ctr.cancels.Load(); got != 2 {
		t.Errorf("cancel attempts = %d, want 2 (one failure + one retry)", got)
	}
	if got := ctr.logins.Load(); got != 2 {
		t.Errorf("logins = %d, want 2 (initial login + re-login after the auth error)", got)
	}
}

// TestCancelMatch_BusinessErrorIsNotRetried protects the distinction between an
// expired session and a legitimate refusal (e.g. past the cancellation
// deadline), which must surface to the caller unchanged.
func TestCancelMatch_BusinessErrorIsNotRetried(t *testing.T) {
	const businessMsg = "Eine Stornierung der Buchung ist nicht möglich"

	var ctr counters
	mux := http.NewServeMux()
	registerGraphQL(mux, &ctr, map[string]gqlHandler{
		"CancelMatch": func(_ int, w http.ResponseWriter) {
			fmt.Fprintf(w, `{"data":{"cancelMatch":{"__typename":"ExpectedErrors","errors":[{"id":"e1","message":%q,"path":"matchId"}]}}}`, businessMsg)
		},
	})
	c := newTestClient(t, mux)

	_, err := c.CancelMatch(context.Background(), "match-1")
	if err == nil {
		t.Fatal("expected an error for a business-rule rejection")
	}
	if errors.Is(err, errUnauthorized) {
		t.Error("business error must not be classified as unauthorized")
	}
	if !strings.Contains(err.Error(), businessMsg) {
		t.Errorf("error %q should contain the upstream message", err)
	}
	if got := ctr.cancels.Load(); got != 1 {
		t.Errorf("cancel attempts = %d, want 1 (no retry for business errors)", got)
	}
}

// ─── HTML instead of JSON ─────────────────────────────────────────────────────

// TestCreateBooking_RetriesStep1OnHTMLResponse covers the create-booking half of
// the outage: step 1 answered with a login page (HTTP 200 + HTML), which used to
// surface as an opaque "invalid character '<'" decode error with no retry.
// The retry must reserve the slot once and pay exactly once.
func TestCreateBooking_RetriesStep1OnHTMLResponse(t *testing.T) {
	var ctr counters
	mux := http.NewServeMux()
	registerGraphQL(mux, &ctr, nil)

	mux.HandleFunc("POST /checkout/api/payableitem/courtbooking", func(w http.ResponseWriter, _ *http.Request) {
		if ctr.step1.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/html; charset=UTF-8")
			fmt.Fprint(w, loginPageHTML)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"bookingUuid":"booking-uuid","bookingId":42,"payment":{"id":7},"success":true,"status":"CONFIRMED"}`)
	})
	mux.HandleFunc("POST /checkout/api/payment/{id}/pay-offline", func(w http.ResponseWriter, _ *http.Request) {
		ctr.payOffline.Add(1)
		fmt.Fprint(w, `{}`)
	})
	mux.HandleFunc("POST /checkout/api/match/create-from-booking", func(w http.ResponseWriter, _ *http.Request) {
		ctr.createMatch.Add(1)
		fmt.Fprint(w, `{"matchId":"match-9"}`)
	})
	mux.HandleFunc("POST /checkout/api/tracking/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{}`)
	})

	c := newTestClient(t, mux)

	start := time.Date(2026, 8, 10, 20, 45, 0, 0, time.UTC)
	res, err := c.CreateBooking(context.Background(), "facility-uuid", "court-uuid", "sport-uuid", start, start.Add(45*time.Minute))
	if err != nil {
		t.Fatalf("CreateBooking: unexpected error: %v", err)
	}
	if res.BookingUUID != "booking-uuid" || res.BookingID != 42 || res.MatchID != "match-9" {
		t.Errorf("unexpected result: %+v", res)
	}
	if got := ctr.step1.Load(); got != 2 {
		t.Errorf("step 1 attempts = %d, want 2 (HTML response + retry)", got)
	}
	if got := ctr.payOffline.Load(); got != 1 {
		t.Errorf("pay-offline calls = %d, want 1 (must never pay twice)", got)
	}
	if got := ctr.logins.Load(); got != 2 {
		t.Errorf("logins = %d, want 2 (initial login + re-login)", got)
	}
}

// TestCreateBooking_PayOfflineHTMLDoesNotRetry pins the duplicate-booking guard:
// once the slot is reserved, an expired session must fail loudly rather than
// restart the checkout.
func TestCreateBooking_PayOfflineHTMLDoesNotRetry(t *testing.T) {
	var ctr counters
	mux := http.NewServeMux()
	registerGraphQL(mux, &ctr, nil)

	mux.HandleFunc("POST /checkout/api/payableitem/courtbooking", func(w http.ResponseWriter, _ *http.Request) {
		ctr.step1.Add(1)
		fmt.Fprint(w, `{"bookingUuid":"booking-uuid","bookingId":42,"payment":{"id":7},"success":true,"status":"CONFIRMED"}`)
	})
	mux.HandleFunc("POST /checkout/api/payment/{id}/pay-offline", func(w http.ResponseWriter, _ *http.Request) {
		ctr.payOffline.Add(1)
		fmt.Fprint(w, loginPageHTML)
	})

	c := newTestClient(t, mux)

	start := time.Date(2026, 8, 10, 20, 45, 0, 0, time.UTC)
	_, err := c.CreateBooking(context.Background(), "facility-uuid", "court-uuid", "sport-uuid", start, start.Add(45*time.Minute))
	if err == nil {
		t.Fatal("expected an error when pay-offline returns HTML")
	}
	if errors.Is(err, errUnauthorized) {
		t.Error("pay-offline HTML must not be retryable — it would duplicate the booking")
	}
	if got := ctr.step1.Load(); got != 1 {
		t.Errorf("step 1 attempts = %d, want 1 (no retry after the slot was reserved)", got)
	}
	if got := ctr.payOffline.Load(); got != 1 {
		t.Errorf("pay-offline calls = %d, want 1", got)
	}
}

// ─── Diagnostics ──────────────────────────────────────────────────────────────

// TestGetSlots_DecodeErrorIncludesBodySnippet ensures an unexpected body shape is
// diagnosable from the logs alone — the original incident showed only
// "invalid character '<'" with no trace of what was actually returned.
func TestGetSlots_DecodeErrorIncludesBodySnippet(t *testing.T) {
	var ctr counters
	mux := http.NewServeMux()
	registerGraphQL(mux, &ctr, nil)
	mux.HandleFunc("GET /api/slot", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `not-json {{{`)
	})

	c := newTestClient(t, mux)

	_, err := c.GetSlots(context.Background(), "76443", []string{"1"}, "2026-08-10")
	if err == nil {
		t.Fatal("expected a decode error")
	}
	if !strings.Contains(err.Error(), "not-json") {
		t.Errorf("error %q should include a snippet of the response body", err)
	}
	if errors.Is(err, errUnauthorized) {
		t.Error("malformed JSON is not an auth failure")
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func TestBodySnippet(t *testing.T) {
	if got := bodySnippet(nil); got != "" {
		t.Errorf("bodySnippet(nil) = %q, want empty", got)
	}
	if got := bodySnippet([]byte("  {\"a\":1}\n")); got != `{"a":1}` {
		t.Errorf("bodySnippet trimmed = %q", got)
	}
	if got := bodySnippet([]byte("line one\n\tline two")); got != "line one line two" {
		t.Errorf("bodySnippet should collapse whitespace to one line, got %q", got)
	}

	long := bodySnippet([]byte(strings.Repeat("a", 6000)))
	if strings.Contains(long, "\n") {
		t.Error("snippet must stay on a single line")
	}
	if len(long) > bodySnippetLimit+len("…") {
		t.Errorf("snippet length = %d, want <= %d", len(long), bodySnippetLimit+len("…"))
	}
	if !strings.HasSuffix(long, "…") {
		t.Error("truncated snippet should be marked with an ellipsis")
	}
}

func TestIsAuthErrorMessage(t *testing.T) {
	authShaped := []string{
		"User is a non complete user",
		"USER IS A NON COMPLETE USER",
		"Not authenticated",
		"unauthenticated request",
		"Unauthorized",
	}
	for _, msg := range authShaped {
		if !isAuthErrorMessage(msg) {
			t.Errorf("isAuthErrorMessage(%q) = false, want true", msg)
		}
	}

	other := []string{
		"Eine Stornierung der Buchung ist nicht möglich",
		"Slot is already booked",
		"",
	}
	for _, msg := range other {
		if isAuthErrorMessage(msg) {
			t.Errorf("isAuthErrorMessage(%q) = true, want false", msg)
		}
	}
}

func TestGQLTopLevelError(t *testing.T) {
	authErr := gqlTopLevelError("CancelMatch", "User is a non complete user")
	if !errors.Is(authErr, errUnauthorized) {
		t.Error("auth-shaped message should wrap errUnauthorized")
	}
	if !strings.Contains(authErr.Error(), "User is a non complete user") {
		t.Errorf("error %q should preserve the upstream message", authErr)
	}

	businessErr := gqlTopLevelError("CancelMatch", "Slot is already booked")
	if errors.Is(businessErr, errUnauthorized) {
		t.Error("business message must not wrap errUnauthorized")
	}
}

func TestIsHTMLResponse(t *testing.T) {
	html := [][]byte{
		[]byte(loginPageHTML),
		[]byte("  \n<html><body>hi</body></html>"),
		[]byte(`<script src="/cdn-cgi/challenge-platform/scripts/jsd/main.js"></script>`),
	}
	for _, b := range html {
		if !isHTMLResponse(b) {
			t.Errorf("isHTMLResponse(%.30q) = false, want true", b)
		}
	}

	notHTML := [][]byte{
		[]byte(`{"bookingUuid":"x"}`),
		[]byte(`[]`),
		{},
		nil,
	}
	for _, b := range notHTML {
		if isHTMLResponse(b) {
			t.Errorf("isHTMLResponse(%.30q) = true, want false", b)
		}
	}
}

func TestHTMLAuthError(t *testing.T) {
	if err := htmlAuthError("court booking", []byte(`{"success":true}`)); err != nil {
		t.Errorf("expected nil for a JSON body, got %v", err)
	}

	err := htmlAuthError("court booking", []byte(loginPageHTML))
	if err == nil {
		t.Fatal("expected an error for an HTML body")
	}
	if !errors.Is(err, errUnauthorized) {
		t.Error("HTML response should wrap errUnauthorized so the call is retried after re-login")
	}
	if !strings.Contains(err.Error(), "Bitte einloggen") {
		t.Errorf("error %q should include a body snippet", err)
	}
}
