package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/internal/featureflags"
	"github.com/hutoroff/squash-bot/internal/models"
)

type apiFlagRepo struct {
	calls int
	value *bool
	group *int64
	err   error
}

func (f *apiFlagRepo) Get(_ context.Context, key featureflags.Key, _ *int64) (featureflags.State, error) {
	d, _ := featureflags.Lookup(key)
	return featureflags.Resolve(d, nil, nil), f.err
}
func (f *apiFlagRepo) Set(_ context.Context, _ featureflags.Key, group *int64, value *bool) (*bool, error) {
	f.calls++
	f.value, f.group = value, group
	return nil, f.err
}
func flagTestHandler() (*Handler, *apiFlagRepo) {
	h := newGroupsHandler(42)
	repo := &apiFlagRepo{}
	h.SetFeatureFlags(service.NewFeatureFlagService(repo, h.userRepo, &stubGroupRepo{groups: []models.Group{{ChatID: -1}}}, service.NewAuditService(&stubbedAuditRepo{}, h.logger)))
	return h, repo
}
func TestFeatureFlagMutationContract(t *testing.T) {
	for _, tt := range []struct {
		name, key, query, body string
		status                 int
	}{
		{"enable", "rating.score_aware", "", `{"enabled":true,"actor_user_id":42}`, 204},
		{"explicit disable", "rating.score_aware", "?group_id=-1", `{"enabled":false,"actor_user_id":42}`, 204},
		{"inherit", "rating.score_aware", "?group_id=-1", `{"enabled":null,"actor_user_id":42}`, 204},
		{"no actor", "rating.score_aware", "", `{"enabled":true}`, 403},
		{"not owner", "rating.score_aware", "?group_id=-1", `{"enabled":true,"actor_user_id":43}`, 403},
		{"missing enabled", "rating.score_aware", "", `{"actor_user_id":42}`, 400},
		{"string enabled", "rating.score_aware", "", `{"enabled":"false","actor_user_id":42}`, 400},
		{"unknown key", "typo", "", `{"enabled":true,"actor_user_id":42}`, 400},
		{"unknown group", "rating.score_aware", "?group_id=-2", `{"enabled":true,"actor_user_id":42}`, 400},
		{"empty group", "rating.score_aware", "?group_id=", `{"enabled":true,"actor_user_id":42}`, 400},
		{"duplicate scope", "rating.score_aware", "?group_id=-1&group_id=-2", `{"enabled":true,"actor_user_id":42}`, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, repo := flagTestHandler()
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/feature-flags/"+tt.key+tt.query, strings.NewReader(tt.body))
			req.SetPathValue("key", tt.key)
			w := httptest.NewRecorder()
			h.setFeatureFlag(w, req)
			if w.Code != tt.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if (repo.calls == 1) != (tt.status == 204) {
				t.Fatalf("unexpected mutations: %d", repo.calls)
			}
			if tt.name == "explicit disable" && (repo.value == nil || *repo.value || repo.group == nil || *repo.group != -1) {
				t.Fatal("false/group not preserved")
			}
			if tt.name == "inherit" && repo.value != nil {
				t.Fatal("reset not preserved")
			}
		})
	}
}
func TestFeatureFlagsReadAuthorizationAndFailure(t *testing.T) {
	h, repo := flagTestHandler()
	for _, actor := range []string{"", "43", "42"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/feature-flags", nil)
		req.Header.Set("X-Caller-User-Id", actor)
		w := httptest.NewRecorder()
		h.listFeatureFlags(w, req)
		expected := 403
		if actor == "42" {
			expected = 200
		}
		if w.Code != expected {
			t.Fatalf("actor %s: %d", actor, w.Code)
		}
	}
	repo.err = errors.New("database unavailable")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/feature-flags", nil)
	req.Header.Set("X-Caller-User-Id", "42")
	w := httptest.NewRecorder()
	h.listFeatureFlags(w, req)
	if w.Code != 500 {
		t.Fatal("read error incorrectly fell back to disabled")
	}
	// Role revocation takes effect without trusting a stale UI/session role.
	h.userRepo.(*fakeUserRepo).byID[42].IsServerOwner = false
	w = httptest.NewRecorder()
	h.listFeatureFlags(w, req)
	if w.Code != 403 {
		t.Fatal("stale owner authorization")
	}
}
