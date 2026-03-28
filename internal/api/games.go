package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// createGame handles POST /api/v1/games
func (h *Handler) createGame(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChatID   int64     `json:"chat_id"`
		GameDate time.Time `json:"game_date"`
		Courts   string    `json:"courts"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ChatID == 0 || req.Courts == "" {
		writeError(w, http.StatusBadRequest, "chat_id and courts are required")
		return
	}

	game, err := h.gameService.CreateGame(r.Context(), req.ChatID, req.GameDate, req.Courts)
	if err != nil {
		h.logger.Error("createGame", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, game)
}

// listGames handles GET /api/v1/games?upcoming=true[&chat_ids=1,2,3]
func (h *Handler) listGames(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ctx := r.Context()

	if q.Get("upcoming") != "true" {
		writeError(w, http.StatusBadRequest, "missing query parameter: upcoming=true")
		return
	}

	rawIDs := q.Get("chat_ids")
	if rawIDs != "" {
		chatIDs, err := parseChatIDs(rawIDs)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid chat_ids: "+err.Error())
			return
		}
		games, err := h.gameService.GetUpcomingGamesByChatIDs(ctx, chatIDs)
		if err != nil {
			h.logger.Error("listGames upcoming by chat_ids", "err", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, games)
		return
	}

	games, err := h.gameService.GetUpcomingGames(ctx)
	if err != nil {
		h.logger.Error("listGames upcoming", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, games)
}

// getGame handles GET /api/v1/games/{id}
func (h *Handler) getGame(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	game, err := h.gameService.GetByID(r.Context(), id)
	if err != nil {
		h.logger.Error("getGame", "err", err, "id", id)
		writeError(w, http.StatusNotFound, "game not found")
		return
	}
	writeJSON(w, http.StatusOK, game)
}

// updateMessageID handles PATCH /api/v1/games/{id}/message-id
func (h *Handler) updateMessageID(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	var req struct {
		MessageID int64 `json:"message_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.gameService.UpdateMessageID(r.Context(), id, req.MessageID); err != nil {
		h.logger.Error("updateMessageID", "err", err, "id", id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// updateCourts handles PATCH /api/v1/games/{id}/courts
func (h *Handler) updateCourts(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	var req struct {
		Courts string `json:"courts"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.gameService.UpdateCourts(r.Context(), id, req.Courts); err != nil {
		h.logger.Error("updateCourts", "err", err, "id", id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getNextGame handles GET /api/v1/players/{telegramID}/next-game
func (h *Handler) getNextGame(w http.ResponseWriter, r *http.Request) {
	telegramID, err := parseID(r.PathValue("telegramID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid telegram_id")
		return
	}
	game, err := h.gameService.GetNextGameForTelegramUser(r.Context(), telegramID)
	if err != nil {
		h.logger.Error("getNextGame", "err", err, "telegram_id", telegramID)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if game == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, game)
}

// parseID parses a string path value into int64.
func parseID(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

// parseChatIDs parses a comma-separated list of int64 chat IDs.
func parseChatIDs(s string) ([]int64, error) {
	parts := strings.Split(s, ",")
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}
