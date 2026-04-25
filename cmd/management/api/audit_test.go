package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/internal/models"
)

// stubbedAuditRepo is an in-memory stub of AuditEventRepository for handler tests.
type stubbedAuditRepo struct {
	captured []models.AuditQueryFilter
	toReturn []*models.AuditEvent
}

func (r *stubbedAuditRepo) Insert(_ context.Context, _ *models.AuditEvent) error { return nil }

func (r *stubbedAuditRepo) Query(_ context.Context, f models.AuditQueryFilter) ([]*models.AuditEvent, error) {
	r.captured = append(r.captured, f)
	return r.toReturn, nil
}

func (r *stubbedAuditRepo) DeleteOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

// fakeAdminResolver is a test stub for adminGroupsResolver.
type fakeAdminResolver struct {
	groups []int64
	err    error
}

func (f *fakeAdminResolver) AdminGroupsFor(_ context.Context, _ int64) ([]int64, error) {
	return f.groups, f.err
}

func newAuditHandler(ownerIDs ...int64) (*Handler, *stubbedAuditRepo) {
	return newAuditHandlerWithResolver(nil, ownerIDs...)
}

func newAuditHandlerWithResolver(resolver adminGroupsResolver, ownerIDs ...int64) (*Handler, *stubbedAuditRepo) {
	repo := &stubbedAuditRepo{}
	auditSvc := service.NewAuditService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	owners := make(map[int64]bool, len(ownerIDs))
	for _, id := range ownerIDs {
		owners[id] = true
	}
	return &Handler{
		auditSvc:       auditSvc,
		adminResolver:  resolver,
		serverOwnerIDs: owners,
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, repo
}

// ── Missing header ────────────────────────────────────────────────────────────

func TestListAuditEvents_MissingCallerHeader(t *testing.T) {
	h, _ := newAuditHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil)
	w := httptest.NewRecorder()
	h.listAuditEvents(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestListAuditEvents_ZeroCallerHeader(t *testing.T) {
	h, _ := newAuditHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil)
	req.Header.Set("X-Caller-Tg-Id", "0")
	w := httptest.NewRecorder()
	h.listAuditEvents(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestListAuditEvents_InvalidCallerHeader(t *testing.T) {
	h, _ := newAuditHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil)
	req.Header.Set("X-Caller-Tg-Id", "notanumber")
	w := httptest.NewRecorder()
	h.listAuditEvents(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

// ── Non-owner visibility enforcement ─────────────────────────────────────────

func TestListAuditEvents_NonOwner_RestrictsToOwnEvents(t *testing.T) {
	// 999 is server owner; caller 123 has no admin groups
	h, repo := newAuditHandlerWithResolver(&fakeAdminResolver{groups: nil}, 999)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil)
	req.Header.Set("X-Caller-Tg-Id", "123")
	w := httptest.NewRecorder()
	h.listAuditEvents(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if len(repo.captured) != 1 {
		t.Fatalf("want 1 query, got %d", len(repo.captured))
	}
	f := repo.captured[0]
	if f.OwnTgID == nil || *f.OwnTgID != 123 {
		t.Errorf("OwnTgID: want 123, got %v", f.OwnTgID)
	}
	if len(f.AdminGroupIDs) != 0 {
		t.Errorf("AdminGroupIDs: want empty, got %v", f.AdminGroupIDs)
	}
	// Legacy fields must not be set
	if f.ActorTgID != nil {
		t.Errorf("ActorTgID: should be nil in non-owner path, got %v", f.ActorTgID)
	}
	if len(f.Visibilities) != 0 {
		t.Errorf("Visibilities: should be empty in non-owner path, got %v", f.Visibilities)
	}
}

func TestListAuditEvents_NonOwner_WithAdminGroups(t *testing.T) {
	// Caller 123 is admin in groups [10, 20]
	resolver := &fakeAdminResolver{groups: []int64{10, 20}}
	h, repo := newAuditHandlerWithResolver(resolver, 999)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil)
	req.Header.Set("X-Caller-Tg-Id", "123")
	w := httptest.NewRecorder()
	h.listAuditEvents(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	f := repo.captured[0]
	if f.OwnTgID == nil || *f.OwnTgID != 123 {
		t.Errorf("OwnTgID: want 123, got %v", f.OwnTgID)
	}
	if len(f.AdminGroupIDs) != 2 || f.AdminGroupIDs[0] != 10 || f.AdminGroupIDs[1] != 20 {
		t.Errorf("AdminGroupIDs: want [10 20], got %v", f.AdminGroupIDs)
	}
}

func TestListAuditEvents_NonOwner_AdminResolverError_GracefulDegradation(t *testing.T) {
	// Resolver fails — caller should still see own events via OwnTgID
	resolver := &fakeAdminResolver{err: fmt.Errorf("telegram unreachable")}
	h, repo := newAuditHandlerWithResolver(resolver, 999)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil)
	req.Header.Set("X-Caller-Tg-Id", "123")
	w := httptest.NewRecorder()
	h.listAuditEvents(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	f := repo.captured[0]
	if f.OwnTgID == nil || *f.OwnTgID != 123 {
		t.Errorf("OwnTgID: want 123, got %v", f.OwnTgID)
	}
	if len(f.AdminGroupIDs) != 0 {
		t.Errorf("AdminGroupIDs: should be empty on resolver error, got %v", f.AdminGroupIDs)
	}
}

func TestListAuditEvents_NonOwner_IgnoresGroupIDParam(t *testing.T) {
	h, repo := newAuditHandlerWithResolver(&fakeAdminResolver{}, 999)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?group_id=42&actor_tg_id=777", nil)
	req.Header.Set("X-Caller-Tg-Id", "123")
	w := httptest.NewRecorder()
	h.listAuditEvents(w, req)

	f := repo.captured[0]
	if f.GroupID != nil {
		t.Errorf("GroupID: non-owner must not filter by group_id, got %v", *f.GroupID)
	}
	// actor_tg_id param must be ignored; caller's own scope is set via OwnTgID
	if f.ActorTgID != nil {
		t.Errorf("ActorTgID: must be nil in non-owner path, got %v", f.ActorTgID)
	}
	if f.OwnTgID == nil || *f.OwnTgID != 123 {
		t.Errorf("OwnTgID: want 123 (caller), got %v", f.OwnTgID)
	}
}

// ── Server owner privileges ───────────────────────────────────────────────────

func TestListAuditEvents_ServerOwner_CanFilterByGroupID(t *testing.T) {
	h, repo := newAuditHandler(123) // caller IS server owner
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?group_id=42", nil)
	req.Header.Set("X-Caller-Tg-Id", "123")
	w := httptest.NewRecorder()
	h.listAuditEvents(w, req)

	f := repo.captured[0]
	if f.GroupID == nil || *f.GroupID != 42 {
		t.Errorf("GroupID: want 42, got %v", f.GroupID)
	}
}

func TestListAuditEvents_ServerOwner_CanFilterByActorTgID(t *testing.T) {
	h, repo := newAuditHandler(123)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?actor_tg_id=777", nil)
	req.Header.Set("X-Caller-Tg-Id", "123")
	w := httptest.NewRecorder()
	h.listAuditEvents(w, req)

	f := repo.captured[0]
	if f.ActorTgID == nil || *f.ActorTgID != 777 {
		t.Errorf("ActorTgID: want 777, got %v", f.ActorTgID)
	}
}

func TestListAuditEvents_ServerOwner_NoVisibilityRestriction(t *testing.T) {
	h, repo := newAuditHandler(123)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil)
	req.Header.Set("X-Caller-Tg-Id", "123")
	w := httptest.NewRecorder()
	h.listAuditEvents(w, req)

	f := repo.captured[0]
	if len(f.Visibilities) != 0 {
		t.Errorf("Visibilities: server owner should get no filter, got %v", f.Visibilities)
	}
	if f.OwnTgID != nil {
		t.Errorf("OwnTgID: server owner should not have scope restriction, got %v", f.OwnTgID)
	}
}

// ── Query param forwarding ────────────────────────────────────────────────────

func TestListAuditEvents_LimitParam(t *testing.T) {
	h, repo := newAuditHandler(123)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?limit=25", nil)
	req.Header.Set("X-Caller-Tg-Id", "123")
	h.listAuditEvents(w(req), req)
	if len(repo.captured) == 0 {
		t.Fatal("no query captured")
	}
	if repo.captured[0].Limit != 25 {
		t.Errorf("Limit: want 25, got %d", repo.captured[0].Limit)
	}
}

func TestListAuditEvents_DefaultLimit(t *testing.T) {
	h, repo := newAuditHandler(123)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil)
	req.Header.Set("X-Caller-Tg-Id", "123")
	h.listAuditEvents(w(req), req)
	if repo.captured[0].Limit != 50 {
		t.Errorf("Limit: want default 50, got %d", repo.captured[0].Limit)
	}
}

func TestListAuditEvents_EventTypeParam(t *testing.T) {
	h, repo := newAuditHandler(123)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit?event_type=game.created", nil)
	req.Header.Set("X-Caller-Tg-Id", "123")
	h.listAuditEvents(w(req), req)
	if repo.captured[0].EventType != "game.created" {
		t.Errorf("EventType: want %q, got %q", "game.created", repo.captured[0].EventType)
	}
}

// ── Response body ─────────────────────────────────────────────────────────────

func TestListAuditEvents_ReturnsEmptyArray(t *testing.T) {
	h, _ := newAuditHandler(123)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit", nil)
	req.Header.Set("X-Caller-Tg-Id", "123")
	rec := httptest.NewRecorder()
	h.listAuditEvents(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var body []any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("body: want empty array, got %v", body)
	}
}

// w is a helper that creates an httptest.ResponseRecorder (used inline in table-style tests).
func w(_ *http.Request) http.ResponseWriter {
	return httptest.NewRecorder()
}
