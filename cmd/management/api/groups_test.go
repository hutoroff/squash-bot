package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newGroupsHandler(ownerIDs ...int64) *Handler {
	owners := make(map[int64]bool, len(ownerIDs))
	for _, id := range ownerIDs {
		owners[id] = true
	}
	return &Handler{
		serverOwnerIDs: owners,
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
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
	w := patchAutoBookingAllowed(h, "42", `{"enabled":true,"actor_telegram_id":999}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("non-owner: want 403, got %d", w.Code)
	}
}

func TestSetGroupAutoBookingAllowed_ZeroActorID(t *testing.T) {
	h := newGroupsHandler(111)
	w := patchAutoBookingAllowed(h, "42", `{"enabled":true,"actor_telegram_id":0}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("zero actor_telegram_id: want 403, got %d", w.Code)
	}
}

func TestSetGroupAutoBookingAllowed_MissingActorID(t *testing.T) {
	h := newGroupsHandler(111)
	w := patchAutoBookingAllowed(h, "42", `{"enabled":true}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("missing actor_telegram_id: want 403, got %d", w.Code)
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
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/groups/abc/auto-booking-allowed", strings.NewReader(`{"enabled":true,"actor_telegram_id":111}`))
	req.SetPathValue("chatID", "abc")
	w := httptest.NewRecorder()
	h.setGroupAutoBookingAllowed(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid chat_id: want 400, got %d", w.Code)
	}
}

func TestSetGroupAutoBookingAllowed_EmptyOwnerSet(t *testing.T) {
	h := newGroupsHandler() // no owners configured
	w := patchAutoBookingAllowed(h, "42", `{"enabled":true,"actor_telegram_id":111}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("empty owner set: want 403, got %d", w.Code)
	}
}
