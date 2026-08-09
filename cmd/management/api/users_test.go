package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
)

// fakeUserRepo is an in-memory stub of userRepository for handler tests.
type fakeUserRepo struct {
	byID              map[int64]*models.User
	ownerByTelegramID map[int64]bool
	list              []*storage.UserSummary
	listErr           error
	resolveUser       *models.User
	resolveErr        error
	setOwnerErr       error
}

func (f *fakeUserRepo) ResolveIdentity(_ context.Context, _, _, _, _, _, _ string) (*models.User, error) {
	return f.resolveUser, f.resolveErr
}

func (f *fakeUserRepo) GetByID(_ context.Context, userID int64) (*models.User, error) {
	u, ok := f.byID[userID]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return u, nil
}

func (f *fakeUserRepo) List(_ context.Context) ([]*storage.UserSummary, error) {
	return f.list, f.listErr
}

func (f *fakeUserRepo) SetServerOwner(_ context.Context, userID int64, enabled bool) error {
	if f.setOwnerErr != nil {
		return f.setOwnerErr
	}
	if u, ok := f.byID[userID]; ok {
		u.IsServerOwner = enabled
	}
	return nil
}

func (f *fakeUserRepo) IsServerOwner(_ context.Context, userID int64) (bool, error) {
	u, ok := f.byID[userID]
	if !ok {
		return false, nil
	}
	return u.IsServerOwner, nil
}

func (f *fakeUserRepo) IsServerOwnerByTelegramID(_ context.Context, tgID int64) (bool, error) {
	return f.ownerByTelegramID[tgID], nil
}

func newUsersTestHandler(repo *fakeUserRepo) *Handler {
	auditSvc := service.NewAuditService(&stubbedAuditRepo{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return &Handler{
		userRepo: repo,
		auditSvc: auditSvc,
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// ── resolveIdentity validation ─────────────────────────────────────────────

func TestResolveIdentity_InvalidJSON_Returns400(t *testing.T) {
	h := newUsersTestHandler(&fakeUserRepo{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/identities/resolve", strings.NewReader("{bad"))
	w := httptest.NewRecorder()
	h.resolveIdentity(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", w.Code)
	}
}

func TestResolveIdentity_UnsupportedProvider_Returns400(t *testing.T) {
	h := newUsersTestHandler(&fakeUserRepo{})
	body := `{"provider":"strava","external_id":"1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/identities/resolve", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.resolveIdentity(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", w.Code)
	}
}

func TestResolveIdentity_MissingExternalID_Returns400(t *testing.T) {
	h := newUsersTestHandler(&fakeUserRepo{})
	body := `{"provider":"telegram","external_id":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/identities/resolve", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.resolveIdentity(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", w.Code)
	}
}

func TestResolveIdentity_ExternalIDValidation(t *testing.T) {
	cases := []string{
		"abc",                  // non-numeric
		"0",                    // zero
		"-5",                   // negative
		"99999999999999999999", // overflows int64
		"1.5",                  // not an integer
		" 5",                   // whitespace, not a clean integer
	}
	for _, externalID := range cases {
		t.Run(externalID, func(t *testing.T) {
			h := newUsersTestHandler(&fakeUserRepo{})
			body := `{"provider":"telegram","external_id":"` + externalID + `"}`
			req := httptest.NewRequest(http.MethodPost, "/api/v1/identities/resolve", strings.NewReader(body))
			w := httptest.NewRecorder()
			h.resolveIdentity(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("external_id=%q: got %d, want 400", externalID, w.Code)
			}
		})
	}
}

// ── getUser ─────────────────────────────────────────────────────────────────

func TestGetUser_InvalidID_Returns400(t *testing.T) {
	h := newUsersTestHandler(&fakeUserRepo{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/abc", nil)
	req.SetPathValue("userID", "abc")
	w := httptest.NewRecorder()
	h.getUser(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", w.Code)
	}
}

func TestGetUser_NotFound_Returns404(t *testing.T) {
	h := newUsersTestHandler(&fakeUserRepo{byID: map[int64]*models.User{}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/1", nil)
	req.SetPathValue("userID", "1")
	w := httptest.NewRecorder()
	h.getUser(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", w.Code)
	}
}

func TestGetUser_Success_Returns200(t *testing.T) {
	h := newUsersTestHandler(&fakeUserRepo{byID: map[int64]*models.User{
		1: {ID: 1, DisplayName: "@alice"},
	}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/1", nil)
	req.SetPathValue("userID", "1")
	w := httptest.NewRecorder()
	h.getUser(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200", w.Code)
	}
}

// ── listUsers authorization ─────────────────────────────────────────────────

func TestListUsers_MissingHeader_Returns400(t *testing.T) {
	h := newUsersTestHandler(&fakeUserRepo{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	w := httptest.NewRecorder()
	h.listUsers(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", w.Code)
	}
}

func TestListUsers_CallerNotOwner_Returns403(t *testing.T) {
	h := newUsersTestHandler(&fakeUserRepo{byID: map[int64]*models.User{
		1: {ID: 1, IsServerOwner: false},
	}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("X-Caller-User-Id", "1")
	w := httptest.NewRecorder()
	h.listUsers(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", w.Code)
	}
}

func TestListUsers_CallerIsOwner_Returns200(t *testing.T) {
	h := newUsersTestHandler(&fakeUserRepo{
		byID: map[int64]*models.User{1: {ID: 1, IsServerOwner: true}},
		list: []*storage.UserSummary{{User: models.User{ID: 1}, Providers: []string{"telegram"}}},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	req.Header.Set("X-Caller-User-Id", "1")
	w := httptest.NewRecorder()
	h.listUsers(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got %d, want 200", w.Code)
	}
}

// ── setUserServerOwner ───────────────────────────────────────────────────────

func TestSetUserServerOwner_MissingActorUserID_Returns400(t *testing.T) {
	h := newUsersTestHandler(&fakeUserRepo{})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/2/server-owner", strings.NewReader(`{"enabled":true}`))
	req.SetPathValue("userID", "2")
	w := httptest.NewRecorder()
	h.setUserServerOwner(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", w.Code)
	}
}

func TestSetUserServerOwner_ActorNotOwner_Returns403(t *testing.T) {
	h := newUsersTestHandler(&fakeUserRepo{byID: map[int64]*models.User{
		1: {ID: 1, IsServerOwner: false},
		2: {ID: 2, IsServerOwner: false},
	}})
	body := `{"enabled":true,"actor_user_id":1,"actor_display":"@bob"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/2/server-owner", strings.NewReader(body))
	req.SetPathValue("userID", "2")
	w := httptest.NewRecorder()
	h.setUserServerOwner(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", w.Code)
	}
}

func TestSetUserServerOwner_TargetNotFound_Returns404(t *testing.T) {
	h := newUsersTestHandler(&fakeUserRepo{byID: map[int64]*models.User{
		1: {ID: 1, IsServerOwner: true},
	}})
	body := `{"enabled":true,"actor_user_id":1,"actor_display":"@owner"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/99/server-owner", strings.NewReader(body))
	req.SetPathValue("userID", "99")
	w := httptest.NewRecorder()
	h.setUserServerOwner(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", w.Code)
	}
}

func TestSetUserServerOwner_LastOwnerConflict_Returns409(t *testing.T) {
	h := newUsersTestHandler(&fakeUserRepo{
		byID: map[int64]*models.User{
			1: {ID: 1, IsServerOwner: true},
		},
		setOwnerErr: storage.ErrLastServerOwner,
	})
	body := `{"enabled":false,"actor_user_id":1,"actor_display":"@owner"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/1/server-owner", strings.NewReader(body))
	req.SetPathValue("userID", "1")
	w := httptest.NewRecorder()
	h.setUserServerOwner(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("got %d, want 409", w.Code)
	}
}

func TestSetUserServerOwner_Success_Returns204(t *testing.T) {
	repo := &fakeUserRepo{byID: map[int64]*models.User{
		1: {ID: 1, IsServerOwner: true, DisplayName: "@owner"},
		2: {ID: 2, IsServerOwner: false, DisplayName: "@bob"},
	}}
	h := newUsersTestHandler(repo)
	body := `{"enabled":true,"actor_user_id":1,"actor_display":"@owner"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/2/server-owner", strings.NewReader(body))
	req.SetPathValue("userID", "2")
	w := httptest.NewRecorder()
	h.setUserServerOwner(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("got %d, want 204", w.Code)
	}
	if !repo.byID[2].IsServerOwner {
		t.Error("expected target to become server owner")
	}
}

func TestSetUserServerOwner_NoOp_Returns204(t *testing.T) {
	repo := &fakeUserRepo{byID: map[int64]*models.User{
		1: {ID: 1, IsServerOwner: true, DisplayName: "@owner"},
	}}
	h := newUsersTestHandler(repo)
	body := `{"enabled":true,"actor_user_id":1,"actor_display":"@owner"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/users/1/server-owner", strings.NewReader(body))
	req.SetPathValue("userID", "1")
	w := httptest.NewRecorder()
	h.setUserServerOwner(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("got %d, want 204", w.Code)
	}
}
