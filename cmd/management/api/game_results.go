package api

import (
	"errors"
	"net/http"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/cmd/management/storage"
)

// submitGameResult handles POST /api/v1/game-results
func (h *Handler) submitGameResult(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GameID           int64  `json:"game_id"`
		AuthorUserID     int64  `json:"author_user_id"`
		OpponentPlayerID int64  `json:"opponent_player_id"`
		WinnerPlayerID   *int64 `json:"winner_player_id"`
		Score            string `json:"score"`
		ActorDisplay     string `json:"actor_display"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.GameID == 0 || req.AuthorUserID == 0 || req.OpponentPlayerID == 0 {
		writeError(w, http.StatusBadRequest, "game_id, author_user_id, and opponent_player_id are required")
		return
	}

	result, err := h.gameResultSvc.Submit(r.Context(),
		req.GameID, req.AuthorUserID, req.OpponentPlayerID,
		req.WinnerPlayerID, req.Score, req.ActorDisplay,
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrGameNotFound):
			writeError(w, http.StatusNotFound, "game not found")
		case errors.Is(err, service.ErrGameResultNotInGame):
			writeError(w, http.StatusBadRequest, "not_in_game")
		case errors.Is(err, service.ErrGameResultWindowClosed):
			writeError(w, http.StatusBadRequest, "window_closed")
		case errors.Is(err, service.ErrGameResultBadScore):
			writeError(w, http.StatusBadRequest, "bad_score")
		case errors.Is(err, service.ErrGameResultSamePlayer):
			writeError(w, http.StatusBadRequest, "same_player")
		case errors.Is(err, service.ErrOpponentOptedOut):
			writeError(w, http.StatusConflict, "opponent_opted_out")
		case errors.Is(err, service.ErrAuthorOptedOut):
			writeError(w, http.StatusConflict, "author_opted_out")
		case errors.Is(err, service.ErrResultsNotSupported):
			writeError(w, http.StatusConflict, "results_not_supported")
		default:
			h.logger.Error("submitGameResult", "err", err)
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

// getGameResult handles GET /api/v1/game-results/{id}
func (h *Handler) getGameResult(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	result, err := h.gameResultSvc.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrGameResultNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		h.logger.Error("getGameResult", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// setGameResultApprovalMessage handles POST /api/v1/game-results/{id}/approval-message
func (h *Handler) setGameResultApprovalMessage(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		ChatID    int64 `json:"chat_id"`
		MessageID int   `json:"message_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.gameResultSvc.SetApprovalMessage(r.Context(), id, req.ChatID, req.MessageID); err != nil {
		h.logger.Error("setGameResultApprovalMessage", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// approveGameResult handles POST /api/v1/game-results/{id}/approve
func (h *Handler) approveGameResult(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		ActorUserID  int64  `json:"actor_user_id"`
		ActorDisplay string `json:"actor_display"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := h.gameResultSvc.Approve(r.Context(), id, req.ActorUserID, req.ActorDisplay)
	if err != nil {
		h.handleResultDecisionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// rejectGameResult handles POST /api/v1/game-results/{id}/reject
func (h *Handler) rejectGameResult(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		ActorUserID  int64  `json:"actor_user_id"`
		ActorDisplay string `json:"actor_display"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := h.gameResultSvc.Reject(r.Context(), id, req.ActorUserID, req.ActorDisplay)
	if err != nil {
		h.handleResultDecisionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// cancelGameResult handles POST /api/v1/game-results/{id}/cancel
func (h *Handler) cancelGameResult(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		ActorUserID  int64  `json:"actor_user_id"`
		ActorDisplay string `json:"actor_display"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := h.gameResultSvc.CancelByAuthor(r.Context(), id, req.ActorUserID, req.ActorDisplay)
	if err != nil {
		h.handleResultDecisionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// getRecentCompletedGames handles GET /api/v1/users/{userID}/recent-completed-games?group_id=X&days=14
func (h *Handler) getRecentCompletedGames(w http.ResponseWriter, r *http.Request) {
	userID, err := parseID(r.PathValue("userID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	groupID, err := parseID(r.URL.Query().Get("group_id"))
	if err != nil || groupID == 0 {
		writeError(w, http.StatusBadRequest, "group_id is required")
		return
	}

	games, err := h.gameService.GetRecentCompletedGamesForPlayer(r.Context(), userID, groupID)
	if err != nil {
		h.logger.Error("getRecentCompletedGames", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, games)
}

func (h *Handler) handleResultDecisionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrGameResultNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, service.ErrGameResultForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, storage.ErrGameResultNotPending):
		writeError(w, http.StatusConflict, "result is not pending")
	default:
		h.logger.Error("game result decision", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
