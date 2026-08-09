package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
)

// userRepository is satisfied by storage.UserRepo and by test fakes.
type userRepository interface {
	ResolveIdentity(ctx context.Context, provider, externalID, username, firstName, lastName, photoURL string) (*models.User, error)
	GetByID(ctx context.Context, userID int64) (*models.User, error)
	List(ctx context.Context) ([]*storage.UserSummary, error)
	SetServerOwner(ctx context.Context, userID int64, enabled bool) error
	IsServerOwner(ctx context.Context, userID int64) (bool, error)
	SetDMLanguage(ctx context.Context, userID int64, language string) error
	SetResultsOptOut(ctx context.Context, userID int64, optOut bool) error
}

type resolveIdentityRequest struct {
	Provider   string `json:"provider"`
	ExternalID string `json:"external_id"`
	Username   string `json:"username"`
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
	PhotoURL   string `json:"photo_url"`
}

type resolveIdentityResponse struct {
	UserID        int64  `json:"user_id"`
	PlayerID      *int64 `json:"player_id"`
	DisplayName   string `json:"display_name"`
	IsServerOwner bool   `json:"is_server_owner"`
}

// resolveIdentity handles POST /api/v1/identities/resolve. It finds-or-creates
// the user for the given external identity and returns its canonical IDs.
// Only provider="telegram" is accepted; other providers 400 until they're
// implemented (email verification / OAuth linking is a future foundation).
func (h *Handler) resolveIdentity(w http.ResponseWriter, r *http.Request) {
	var req resolveIdentityRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Provider != models.IdentityProviderTelegram {
		writeError(w, http.StatusBadRequest, "unsupported provider")
		return
	}
	// Telegram user IDs are positive 64-bit integers. Validate now so a
	// malformed/zero/negative/overflowing value can't create a persistent
	// identity that UserRepo.TelegramID later fails to parse.
	if tgID, err := strconv.ParseInt(req.ExternalID, 10, 64); err != nil || tgID <= 0 {
		writeError(w, http.StatusBadRequest, "external_id must be a positive telegram user ID")
		return
	}

	user, err := h.userRepo.ResolveIdentity(r.Context(), req.Provider, req.ExternalID, req.Username, req.FirstName, req.LastName, req.PhotoURL)
	if err != nil {
		h.logger.Error("resolveIdentity", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var playerID *int64
	if id, ok, err := h.playerRepo.PlayerIDByUserID(r.Context(), user.ID); err != nil {
		h.logger.Error("resolveIdentity: player lookup", "user_id", user.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	} else if ok {
		playerID = &id
	}

	writeJSON(w, http.StatusOK, resolveIdentityResponse{
		UserID:        user.ID,
		PlayerID:      playerID,
		DisplayName:   user.DisplayName,
		IsServerOwner: user.IsServerOwner,
	})
}

// getUser handles GET /api/v1/users/{userID}.
func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user ID")
		return
	}

	user, err := h.userRepo.GetByID(r.Context(), userID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		h.logger.Error("getUser", "user_id", userID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// listUsers handles GET /api/v1/users. Owner-only: the caller must identify
// itself via X-Caller-User-Id and that user must be a server owner. List
// membership is never trusted from client-controlled headers beyond identity —
// the owner check itself is enforced against the DB.
func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	callerUserID, err := strconv.ParseInt(r.Header.Get("X-Caller-User-Id"), 10, 64)
	if err != nil || callerUserID == 0 {
		writeError(w, http.StatusBadRequest, "X-Caller-User-Id header required")
		return
	}
	isOwner, err := h.userRepo.IsServerOwner(r.Context(), callerUserID)
	if err != nil {
		h.logger.Error("listUsers: check caller", "caller_user_id", callerUserID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !isOwner {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	users, err := h.userRepo.List(r.Context())
	if err != nil {
		h.logger.Error("listUsers", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if users == nil {
		users = []*storage.UserSummary{}
	}
	writeJSON(w, http.StatusOK, users)
}

type setServerOwnerRequest struct {
	Enabled      bool   `json:"enabled"`
	ActorUserID  int64  `json:"actor_user_id"`
	ActorDisplay string `json:"actor_display"`
}

// setUserServerOwner handles PATCH /api/v1/users/{userID}/server-owner.
// The actor must be a server owner; revoking the last remaining owner 409s.
func (h *Handler) setUserServerOwner(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user ID")
		return
	}
	var req setServerOwnerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ActorUserID == 0 {
		writeError(w, http.StatusBadRequest, "actor_user_id is required")
		return
	}

	isOwner, err := h.userRepo.IsServerOwner(r.Context(), req.ActorUserID)
	if err != nil {
		h.logger.Error("setUserServerOwner: check actor", "actor_user_id", req.ActorUserID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !isOwner {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	target, err := h.userRepo.GetByID(r.Context(), userID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		h.logger.Error("setUserServerOwner: getByID", "user_id", userID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if target.IsServerOwner == req.Enabled {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := h.userRepo.SetServerOwner(r.Context(), userID, req.Enabled); err != nil {
		if errors.Is(err, storage.ErrLastServerOwner) {
			writeError(w, http.StatusConflict, "cannot revoke the last server owner")
			return
		}
		h.logger.Error("setUserServerOwner", "user_id", userID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.auditSvc.RecordUserRoleChanged(r.Context(), req.ActorUserID, req.ActorDisplay, userID, target.DisplayName, req.Enabled)
	w.WriteHeader(http.StatusNoContent)
}

// setUserDMLanguage handles PATCH /api/v1/users/{userID}/dm-language.
// Body: {"language": "en"|"de"|"ru"}. Returns 204 on success.
func (h *Handler) setUserDMLanguage(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user ID")
		return
	}

	var req struct {
		Language string `json:"language"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	switch req.Language {
	case "en", "de", "ru":
		// valid
	default:
		writeError(w, http.StatusBadRequest, "unsupported language; use en, de, or ru")
		return
	}

	if err := h.userRepo.SetDMLanguage(r.Context(), userID, req.Language); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		h.logger.Error("setUserDMLanguage", "user_id", userID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// setUserResultsOptOut handles PATCH /api/v1/users/{userID}/results-opt-out.
// Body: {"opt_out": bool}. Returns 204 on success.
func (h *Handler) setUserResultsOptOut(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user ID")
		return
	}

	var req struct {
		OptOut *bool `json:"opt_out"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.OptOut == nil {
		writeError(w, http.StatusBadRequest, "opt_out field is required")
		return
	}

	if err := h.userRepo.SetResultsOptOut(r.Context(), userID, *req.OptOut); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		h.logger.Error("setUserResultsOptOut", "user_id", userID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
