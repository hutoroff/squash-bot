package webserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFeatureFlagsProxyIdentityAndScope(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			rec := &usersRecorder{respJSON: `[]`}
			mgmt := newUsersMgmtServer(t, rec)
			auth := testAuthHandler(t, nil)
			f := NewFeatureFlagsHandler(auth, mgmt.URL, "mgmt-secret")
			path := "/api/feature-flags?group_id=-1"
			pattern := "GET /api/feature-flags"
			if method == http.MethodPatch {
				path = "/api/feature-flags/rating.score_aware?group_id=-1"
				pattern = "PATCH /api/feature-flags/{key}"
			}
			req := httptest.NewRequest(method, path, strings.NewReader(`{"enabled":null,"actor_user_id":999,"actor_display":"forged"}`))
			req.Header.Set("X-Caller-User-Id", "999")
			req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
			w := routeAndServe(pattern, f.handle, req)
			if w.Code != 200 || len(rec.paths) != 1 || !strings.HasSuffix(rec.paths[0], "?group_id=-1") {
				t.Fatalf("response %d, paths %v", w.Code, rec.paths)
			}
			if rec.headers[0].Get("Authorization") != "Bearer mgmt-secret" {
				t.Fatal("missing bearer")
			}
			if method == http.MethodGet {
				if rec.headers[0].Get("X-Caller-User-Id") != "42" {
					t.Fatal("spoofed caller forwarded")
				}
			} else {
				var body map[string]any
				if err := json.Unmarshal([]byte(rec.bodies[0]), &body); err != nil {
					t.Fatal(err)
				}
				if body["actor_user_id"] != float64(42) || body["actor_display"] == "forged" || body["enabled"] != nil {
					t.Fatalf("bad body: %v", body)
				}
			}
		})
	}
}
func TestFeatureFlagsProxyRejectsUnauthenticatedAndPreserves403(t *testing.T) {
	rec := &usersRecorder{status: 403, respJSON: `{"error":"forbidden"}`}
	mgmt := newUsersMgmtServer(t, rec)
	auth := testAuthHandler(t, nil)
	f := NewFeatureFlagsHandler(auth, mgmt.URL, "secret")
	for _, method := range []string{http.MethodGet, http.MethodPatch} {
		req := httptest.NewRequest(method, "/api/feature-flags", nil)
		w := httptest.NewRecorder()
		f.handle(w, req)
		if w.Code != 401 || len(rec.paths) != 0 {
			t.Fatal("unauthenticated request forwarded")
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/feature-flags", nil)
	req.AddCookie(validSessionCookie(t, auth, 42, "alice"))
	w := httptest.NewRecorder()
	f.handle(w, req)
	if w.Code != 403 || w.Body.String() != rec.respJSON {
		t.Fatal("upstream denial not preserved")
	}
}
