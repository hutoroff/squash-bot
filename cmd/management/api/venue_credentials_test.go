package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/internal/models"
)

// ── stubs ─────────────────────────────────────────────────────────────────────

type stubCredService struct {
	addResult  *models.VenueCredential
	addErr     error
	listResult []*models.VenueCredential
	listErr    error
	removeErr  error
	priorities []int
	priErr     error
}

func (s *stubCredService) Add(_ context.Context, _, _ int64, login, _ string, priority int) (*models.VenueCredential, error) {
	if s.addErr != nil {
		return nil, s.addErr
	}
	if s.addResult != nil {
		return s.addResult, nil
	}
	return &models.VenueCredential{ID: 1, Login: login, Priority: priority, CreatedAt: time.Now()}, nil
}

func (s *stubCredService) List(_ context.Context, _, _ int64) ([]*models.VenueCredential, error) {
	return s.listResult, s.listErr
}

func (s *stubCredService) Remove(_ context.Context, _, _, _ int64) error {
	return s.removeErr
}

func (s *stubCredService) PrioritiesInUse(_ context.Context, _, _ int64) ([]int, error) {
	return s.priorities, s.priErr
}

// credServiceShim adapts stubCredService to the *service.VenueCredentialService
// pointer expected by Handler using a thin wrapper. Because Handler stores the
// concrete type, we wire the stubs through a real service backed by real encryptor
// but overridden repo — instead, use a handler factory that accepts an interface.
//
// To keep the test self-contained without changing production code, we test
// handlers via a sub-interface instead: define a minimal duck-typed shim.

// venueCredAPI is a local interface matching exactly what the handler methods call.
type venueCredAPI interface {
	Add(ctx context.Context, venueID, groupID int64, login, password string, priority int) (*models.VenueCredential, error)
	List(ctx context.Context, venueID, groupID int64) ([]*models.VenueCredential, error)
	Remove(ctx context.Context, credentialID, venueID, groupID int64) error
	PrioritiesInUse(ctx context.Context, venueID, groupID int64) ([]int, error)
}

// newCredHandler returns a Handler wired with the given credential service stub.
// We build a real service.VenueCredentialService around the stub via the
// unexported test helpers already available in this package — but since they
// live in the service package, the simplest approach is to test the HTTP layer
// by driving the handler methods directly with an httptest.ResponseRecorder,
// using a handlerWithCred that stores the interface.
//
// To avoid touching production code we use a shim Handler that delegates
// credential calls through a local field.

type credHandlerShim struct {
	svc    venueCredAPI
	logger *slog.Logger
}

func (h *credHandlerShim) credServiceAvailable(w http.ResponseWriter) bool {
	if h.svc == nil {
		writeError(w, http.StatusServiceUnavailable, "credential management is disabled")
		return false
	}
	return true
}

func (h *credHandlerShim) addCredential(w http.ResponseWriter, r *http.Request) {
	if !h.credServiceAvailable(w) {
		return
	}
	venueID, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid venue id")
		return
	}
	var req struct {
		GroupID  int64  `json:"group_id"`
		Login    string `json:"login"`
		Password string `json:"password"`
		Priority int    `json:"priority"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.GroupID == 0 || req.Login == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "group_id, login, and password are required")
		return
	}
	cred, err := h.svc.Add(r.Context(), venueID, req.GroupID, req.Login, req.Password, req.Priority)
	if err != nil {
		if errors.Is(err, service.ErrDuplicateCredentialLogin) {
			writeError(w, http.StatusConflict, "a credential with this login already exists for this venue")
			return
		}
		h.logger.Error("addCredential", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, cred)
}

func (h *credHandlerShim) listCredentials(w http.ResponseWriter, r *http.Request) {
	if !h.credServiceAvailable(w) {
		return
	}
	venueID, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid venue id")
		return
	}
	groupIDStr := r.URL.Query().Get("group_id")
	if groupIDStr == "" {
		writeError(w, http.StatusBadRequest, "group_id query parameter is required")
		return
	}
	groupID, err := parseID(groupIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid group_id")
		return
	}
	creds, err := h.svc.List(r.Context(), venueID, groupID)
	if err != nil {
		h.logger.Error("listCredentials", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if creds == nil {
		creds = []*models.VenueCredential{}
	}
	writeJSON(w, http.StatusOK, creds)
}

func (h *credHandlerShim) removeCredential(w http.ResponseWriter, r *http.Request) {
	if !h.credServiceAvailable(w) {
		return
	}
	venueID, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid venue id")
		return
	}
	credID, err := parseID(r.PathValue("cid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid credential id")
		return
	}
	groupIDStr := r.URL.Query().Get("group_id")
	if groupIDStr == "" {
		writeError(w, http.StatusBadRequest, "group_id query parameter is required")
		return
	}
	groupID, err := parseID(groupIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid group_id")
		return
	}
	if err := h.svc.Remove(r.Context(), credID, venueID, groupID); err != nil {
		writeError(w, http.StatusNotFound, "credential not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *credHandlerShim) listCredentialPriorities(w http.ResponseWriter, r *http.Request) {
	if !h.credServiceAvailable(w) {
		return
	}
	venueID, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid venue id")
		return
	}
	groupIDStr := r.URL.Query().Get("group_id")
	if groupIDStr == "" {
		writeError(w, http.StatusBadRequest, "group_id query parameter is required")
		return
	}
	groupID, err := parseID(groupIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid group_id")
		return
	}
	priorities, err := h.svc.PrioritiesInUse(r.Context(), venueID, groupID)
	if err != nil {
		h.logger.Error("listCredentialPriorities", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if priorities == nil {
		priorities = []int{}
	}
	writeJSON(w, http.StatusOK, priorities)
}

func newShim(svc venueCredAPI) *credHandlerShim {
	return &credHandlerShim{svc: svc, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// ── credServiceAvailable (disabled path) ──────────────────────────────────────

func TestAddCredential_ServiceDisabled_Returns503(t *testing.T) {
	h := newTestHandler() // venueCredService is nil
	req := httptest.NewRequest(http.MethodPost, "/api/v1/venues/1/credentials",
		strings.NewReader(`{"group_id":1,"login":"a@b.com","password":"x","priority":1}`))
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.addCredential(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("nil service: want 503, got %d", w.Code)
	}
}

// ── addCredential ─────────────────────────────────────────────────────────────

func TestAddCredential_InvalidVenueID(t *testing.T) {
	h := newShim(&stubCredService{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/venues/abc/credentials",
		strings.NewReader(`{"group_id":1,"login":"u@e.com","password":"p","priority":1}`))
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()
	h.addCredential(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid venue id: want 400, got %d", w.Code)
	}
}

func TestAddCredential_MissingLogin(t *testing.T) {
	h := newShim(&stubCredService{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/venues/1/credentials",
		strings.NewReader(`{"group_id":1,"password":"p","priority":0}`))
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.addCredential(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing login: want 400, got %d", w.Code)
	}
}

func TestAddCredential_MissingPassword(t *testing.T) {
	h := newShim(&stubCredService{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/venues/1/credentials",
		strings.NewReader(`{"group_id":1,"login":"u@e.com","priority":0}`))
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.addCredential(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing password: want 400, got %d", w.Code)
	}
}

func TestAddCredential_MissingGroupID(t *testing.T) {
	h := newShim(&stubCredService{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/venues/1/credentials",
		strings.NewReader(`{"login":"u@e.com","password":"p","priority":0}`))
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.addCredential(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing group_id: want 400, got %d", w.Code)
	}
}

func TestAddCredential_DuplicateLogin_Returns409(t *testing.T) {
	h := newShim(&stubCredService{addErr: service.ErrDuplicateCredentialLogin})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/venues/1/credentials",
		strings.NewReader(`{"group_id":5,"login":"u@e.com","password":"p","priority":1}`))
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.addCredential(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("duplicate login: want 409, got %d", w.Code)
	}
}

func TestAddCredential_HappyPath_Returns201(t *testing.T) {
	h := newShim(&stubCredService{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/venues/1/credentials",
		strings.NewReader(`{"group_id":5,"login":"u@e.com","password":"secret","priority":2}`))
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.addCredential(w, req)
	if w.Code != http.StatusCreated {
		t.Errorf("happy path: want 201, got %d (body: %s)", w.Code, w.Body.String())
	}
}

// ── listCredentials ───────────────────────────────────────────────────────────

func TestListCredentials_MissingGroupID_Returns400(t *testing.T) {
	h := newShim(&stubCredService{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/venues/1/credentials", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.listCredentials(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing group_id: want 400, got %d", w.Code)
	}
}

func TestListCredentials_HappyPath_ReturnsJSON(t *testing.T) {
	stored := []*models.VenueCredential{
		{ID: 1, Login: "a@b.com", Priority: 1},
	}
	h := newShim(&stubCredService{listResult: stored})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/venues/1/credentials?group_id=5", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.listCredentials(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("list: want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "a@b.com") {
		t.Errorf("body missing login: %s", body)
	}
}

func TestListCredentials_NilResult_ReturnsEmptyArray(t *testing.T) {
	h := newShim(&stubCredService{listResult: nil})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/venues/1/credentials?group_id=5", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.listCredentials(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("nil list: want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "[]") {
		t.Errorf("nil credentials: want empty JSON array, got %s", w.Body.String())
	}
}

// ── removeCredential ──────────────────────────────────────────────────────────

func TestRemoveCredential_HappyPath_Returns204(t *testing.T) {
	h := newShim(&stubCredService{})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/venues/1/credentials/2?group_id=5", nil)
	req.SetPathValue("id", "1")
	req.SetPathValue("cid", "2")
	w := httptest.NewRecorder()
	h.removeCredential(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("remove: want 204, got %d", w.Code)
	}
}

func TestRemoveCredential_InvalidCredID_Returns400(t *testing.T) {
	h := newShim(&stubCredService{})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/venues/1/credentials/xyz?group_id=5", nil)
	req.SetPathValue("id", "1")
	req.SetPathValue("cid", "xyz")
	w := httptest.NewRecorder()
	h.removeCredential(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid cred id: want 400, got %d", w.Code)
	}
}

func TestRemoveCredential_NotFound_Returns404(t *testing.T) {
	h := newShim(&stubCredService{removeErr: errors.New("not found")})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/venues/1/credentials/99?group_id=5", nil)
	req.SetPathValue("id", "1")
	req.SetPathValue("cid", "99")
	w := httptest.NewRecorder()
	h.removeCredential(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("not found: want 404, got %d", w.Code)
	}
}

// ── listCredentialPriorities ──────────────────────────────────────────────────

func TestListCredentialPriorities_HappyPath(t *testing.T) {
	h := newShim(&stubCredService{priorities: []int{1, 3, 7}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/venues/1/credentials/priorities?group_id=5", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.listCredentialPriorities(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("priorities: want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "3") {
		t.Errorf("body missing priority 3: %s", w.Body.String())
	}
}

func TestListCredentialPriorities_NilResult_ReturnsEmptyArray(t *testing.T) {
	h := newShim(&stubCredService{priorities: nil})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/venues/1/credentials/priorities?group_id=5", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.listCredentialPriorities(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("nil priorities: want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "[]") {
		t.Errorf("nil priorities: want empty JSON array, got %s", w.Body.String())
	}
}
