package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
)

func newGroupsHandler(ownerIDs ...int64) *Handler {
	byID := make(map[int64]*models.User, len(ownerIDs))
	for _, id := range ownerIDs {
		byID[id] = &models.User{ID: id, IsServerOwner: true}
	}
	return &Handler{
		userRepo: &fakeUserRepo{byID: byID},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func patchAutoBookingAllowed(h *Handler, chatID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/groups/"+chatID+"/auto-booking-allowed", strings.NewReader(body))
	req.SetPathValue("chatID", chatID)
	w := httptest.NewRecorder()
	h.setGroupAutoBookingAllowed(w, req)
	return w
}

func TestSetGroupAutoBookingAllowed_NonOwner(t *testing.T) {
	h := newGroupsHandler(111)
	w := patchAutoBookingAllowed(h, "42", `{"enabled":true,"actor_user_id":999}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("non-owner: want 403, got %d", w.Code)
	}
}

func TestSetGroupAutoBookingAllowed_ZeroActorID(t *testing.T) {
	h := newGroupsHandler(111)
	w := patchAutoBookingAllowed(h, "42", `{"enabled":true,"actor_user_id":0}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("zero actor_user_id: want 403, got %d", w.Code)
	}
}

func TestSetGroupAutoBookingAllowed_MissingActorID(t *testing.T) {
	h := newGroupsHandler(111)
	w := patchAutoBookingAllowed(h, "42", `{"enabled":true}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("missing actor_user_id: want 403, got %d", w.Code)
	}
}

func TestSetGroupAutoBookingAllowed_InvalidBody(t *testing.T) {
	h := newGroupsHandler(111)
	w := patchAutoBookingAllowed(h, "42", `not-json`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid body: want 400, got %d", w.Code)
	}
}

func TestSetGroupAutoBookingAllowed_InvalidChatID(t *testing.T) {
	h := newGroupsHandler(111)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/groups/abc/auto-booking-allowed", strings.NewReader(`{"enabled":true,"actor_user_id":111}`))
	req.SetPathValue("chatID", "abc")
	w := httptest.NewRecorder()
	h.setGroupAutoBookingAllowed(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid chat_id: want 400, got %d", w.Code)
	}
}

func TestSetGroupAutoBookingAllowed_EmptyOwnerSet(t *testing.T) {
	h := newGroupsHandler() // no owners configured
	w := patchAutoBookingAllowed(h, "42", `{"enabled":true,"actor_user_id":111}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("empty owner set: want 403, got %d", w.Code)
	}
}

// ── listAdminGroups ───────────────────────────────────────────────────────────

// stubGroupRepo implements service.GroupRepository for API handler tests.
type stubGroupRepo struct {
	groups []models.Group
}

func (r *stubGroupRepo) GetAll(_ context.Context) ([]models.Group, error) { return r.groups, nil }
func (r *stubGroupRepo) GetByID(_ context.Context, chatID int64) (*models.Group, error) {
	for i := range r.groups {
		if r.groups[i].ChatID == chatID {
			return &r.groups[i], nil
		}
	}
	return nil, pgx.ErrNoRows
}
func (r *stubGroupRepo) Upsert(_ context.Context, _ int64, _ string, _ bool) error    { return nil }
func (r *stubGroupRepo) SetLanguage(_ context.Context, _ int64, _ string) error       { return nil }
func (r *stubGroupRepo) SetTimezone(_ context.Context, _ int64, _ string) error       { return nil }
func (r *stubGroupRepo) SetChangelogEnabled(_ context.Context, _ int64, _ bool) error { return nil }
func (r *stubGroupRepo) SetLeaderboardNotificationsEnabled(_ context.Context, _ int64, _ bool) error {
	return nil
}
func (r *stubGroupRepo) SetAutoBookingAllowed(_ context.Context, _ int64, _ bool) ([]int64, error) {
	return nil, nil
}
func (r *stubGroupRepo) Remove(_ context.Context, _ int64) error { return nil }
func (r *stubGroupRepo) Exists(_ context.Context, _ int64) (bool, error) {
	return false, nil
}
func (r *stubGroupRepo) SetLastLeaderboardPostedFor(_ context.Context, _ int64, _ time.Time) error {
	return nil
}

func newAdminGroupsHandler(resolver adminGroupsResolver, groups []models.Group, ownerIDs ...int64) *Handler {
	h := newGroupsHandler(ownerIDs...)
	h.groupRepo = &stubGroupRepo{groups: groups}
	h.adminResolver = resolver
	return h
}

func getAdminGroups(h *Handler, userID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+userID+"/admin-groups", nil)
	req.SetPathValue("userID", userID)
	w := httptest.NewRecorder()
	h.listAdminGroups(w, req)
	return w
}

func decodeGroups(t *testing.T, w *httptest.ResponseRecorder) []models.Group {
	t.Helper()
	var got []models.Group
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body %q: %v", w.Body.String(), err)
	}
	return got
}

func TestListAdminGroups_Owner_SeesAll(t *testing.T) {
	all := []models.Group{{ChatID: -100}, {ChatID: -200}}
	// A resolver that would deny everything — owners must not consult it.
	h := newAdminGroupsHandler(&fakeAdminResolver{groups: nil}, all, 42)

	w := getAdminGroups(h, "42")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if got := decodeGroups(t, w); len(got) != 2 {
		t.Errorf("owner should see all groups, got %v", got)
	}
}

func TestListAdminGroups_Admin_SeesOwnGroupsOnly(t *testing.T) {
	all := []models.Group{{ChatID: -100}, {ChatID: -200}}
	h := newAdminGroupsHandler(&fakeAdminResolver{groups: []int64{-200}}, all, 999)

	w := getAdminGroups(h, "42")
	got := decodeGroups(t, w)
	if len(got) != 1 || got[0].ChatID != -200 {
		t.Errorf("want only group -200, got %v", got)
	}
}

func TestListAdminGroups_PlainUser_EmptyArray(t *testing.T) {
	h := newAdminGroupsHandler(&fakeAdminResolver{groups: nil}, []models.Group{{ChatID: -100}}, 999)

	w := getAdminGroups(h, "42")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if body := strings.TrimSpace(w.Body.String()); body != "[]" {
		t.Errorf("want [], got %q", body)
	}
}

func TestListAdminGroups_ResolverError_500(t *testing.T) {
	h := newAdminGroupsHandler(&fakeAdminResolver{err: context.DeadlineExceeded}, []models.Group{{ChatID: -100}}, 999)

	if w := getAdminGroups(h, "42"); w.Code != http.StatusInternalServerError {
		t.Errorf("want 500, got %d", w.Code)
	}
}

func TestListAdminGroups_InvalidUserID(t *testing.T) {
	h := newAdminGroupsHandler(&fakeAdminResolver{}, nil, 999)
	if w := getAdminGroups(h, "abc"); w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}
