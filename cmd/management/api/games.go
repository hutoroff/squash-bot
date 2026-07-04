package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/internal/models"
)

// createGame handles POST /api/v1/games
func (h *Handler) createGame(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChatID          int64     `json:"chat_id"`
		GameDate        time.Time `json:"game_date"`
		Courts          string    `json:"courts"`
		VenueID         *int64    `json:"venue_id"`
		ActorTelegramID int64     `json:"actor_telegram_id"`
		ActorDisplay    string    `json:"actor_display"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ChatID == 0 || req.Courts == "" {
		writeError(w, http.StatusBadRequest, "chat_id and courts are required")
		return
	}

	game, err := h.gameService.CreateGame(r.Context(), req.ChatID, req.GameDate, req.Courts, req.VenueID)
	if err != nil {
		h.logger.Error("createGame", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.ActorTelegramID != 0 {
		h.auditSvc.RecordGameCreated(r.Context(), game.ID, req.ChatID, req.ActorTelegramID, req.ActorDisplay, game.Courts, game.GameDate)
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

// updateCourts handles PATCH /api/v1/games/{id}/courts.
// When cancel_bookings=true, active Eversports bookings for removed courts are canceled first.
func (h *Handler) updateCourts(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	var req struct {
		Courts          string `json:"courts"`
		GroupID         int64  `json:"group_id"`
		ActorTelegramID int64  `json:"actor_telegram_id"`
		ActorDisplay    string `json:"actor_display"`
		CancelBookings  bool   `json:"cancel_bookings"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.CancelBookings {
		canceledLabels, cancelErrors, svcErr := h.gameService.RemoveCourtsAndCancelBookings(r.Context(), id, req.Courts)
		if svcErr != nil {
			h.logger.Error("updateCourts (cancel)", "err", svcErr, "id", id)
			writeError(w, http.StatusInternalServerError, svcErr.Error())
			return
		}
		if req.ActorTelegramID != 0 {
			h.auditSvc.RecordCourtsReserved(r.Context(), id, req.GroupID, req.ActorTelegramID, req.ActorDisplay, req.Courts)
		}
		if len(cancelErrors) > 0 {
			type failureItem struct {
				Court  string `json:"court"`
				Reason string `json:"reason"`
			}
			failures := make([]failureItem, len(cancelErrors))
			for i, ce := range cancelErrors {
				failures[i] = failureItem{Court: ce.CourtLabel, Reason: ce.Err.Error()}
			}
			for _, ce := range cancelErrors {
				h.logger.Warn("updateCourts: partial cancellation failure",
					"id", id, "court", ce.CourtLabel, "err", ce.Err)
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"canceled": canceledLabels,
				"failed":   failures,
			})
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := h.gameService.UpdateCourts(r.Context(), id, req.Courts); err != nil {
		h.logger.Error("updateCourts", "err", err, "id", id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.ActorTelegramID != 0 {
		h.auditSvc.RecordCourtsReserved(r.Context(), id, req.GroupID, req.ActorTelegramID, req.ActorDisplay, req.Courts)
	}
	w.WriteHeader(http.StatusNoContent)
}

// listActiveCourtBookings handles GET /api/v1/games/{id}/active-court-bookings?courts=1,2
func (h *Handler) listActiveCourtBookings(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	var courts []string
	if raw := r.URL.Query().Get("courts"); raw != "" {
		for _, p := range strings.Split(raw, ",") {
			if t := strings.TrimSpace(p); t != "" {
				courts = append(courts, t)
			}
		}
	}
	infos, err := h.gameService.ListActiveCourtBookings(r.Context(), id, courts)
	if err != nil {
		h.logger.Error("listActiveCourtBookings", "err", err, "id", id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if infos == nil {
		infos = []service.CourtBookingInfo{}
	}
	writeJSON(w, http.StatusOK, infos)
}

// checkGameAccess handles GET /api/v1/games/{id}/access?telegram_id=<id>.
// Used by the web service to authorize its per-game endpoints before acting
// on a caller-supplied game id (IDOR guard) — reports whether telegram_id is
// associated with the game's group.
func (h *Handler) checkGameAccess(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	telegramID, err := parseID(r.URL.Query().Get("telegram_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid or missing telegram_id")
		return
	}
	allowed, err := h.gameService.PlayerCanAccessGame(r.Context(), telegramID, id)
	if err != nil {
		h.logger.Error("checkGameAccess", "err", err, "id", id, "telegram_id", telegramID)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"allowed": allowed})
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

// listPlayerGames handles GET /api/v1/players/{playerID}/games.
// Returns all games in which the player has any participation record, newest-first,
// with participation status, registered count, venue info, and group timezone included.
func (h *Handler) listPlayerGames(w http.ResponseWriter, r *http.Request) {
	playerID, err := parseID(r.PathValue("playerID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid player id")
		return
	}
	games, err := h.gameService.GetGamesForPlayer(r.Context(), playerID)
	if err != nil {
		h.logger.Error("listPlayerGames", "err", err, "player_id", playerID)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if games == nil {
		games = []models.PlayerGame{}
	}
	writeJSON(w, http.StatusOK, games)
}

// publishGame handles POST /api/v1/games/{id}/publish
func (h *Handler) publishGame(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	var req struct {
		ActorTelegramID int64  `json:"actor_telegram_id"`
		ActorDisplay    string `json:"actor_display"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	game, err := h.gameService.PublishGame(r.Context(), id, req.ActorTelegramID, req.ActorDisplay)
	if err != nil {
		if errors.Is(err, service.ErrGameNotFound) {
			writeError(w, http.StatusNotFound, "game not found")
			return
		}
		if errors.Is(err, service.ErrGameAlreadyPublished) {
			writeError(w, http.StatusConflict, "game already published")
			return
		}
		h.logger.Error("publishGame: send failed", "err", err, "id", id)
		writeError(w, http.StatusBadGateway, "failed to send announcement")
		return
	}
	writeJSON(w, http.StatusOK, game)
}

// bookCourts handles POST /api/v1/games/{id}/book-courts
func (h *Handler) bookCourts(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid game id")
		return
	}
	var req struct {
		Count           int    `json:"count"`
		GroupID         int64  `json:"group_id"`
		ActorTelegramID int64  `json:"actor_telegram_id"`
		ActorDisplay    string `json:"actor_display"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Count <= 0 {
		writeError(w, http.StatusBadRequest, "count must be a positive integer")
		return
	}

	result, err := h.gameService.BookGameCourts(r.Context(), id, req.Count, req.ActorTelegramID, req.ActorDisplay, h.credentialErrorCooldown)
	if err != nil {
		if errors.Is(err, service.ErrGameNotFound) {
			writeError(w, http.StatusNotFound, "game not found")
			return
		}
		if errors.Is(err, service.ErrAutoBookingNotAvailable) {
			writeError(w, http.StatusConflict, "auto-booking not available for this game")
			return
		}
		h.logger.Error("bookCourts", "err", err, "id", id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type failureItem struct {
		Reason string `json:"reason"`
	}
	failures := make([]failureItem, len(result.Failures))
	for i, f := range result.Failures {
		failures[i] = failureItem{Reason: f.Reason}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"requested":     result.Requested,
		"booked_count":  len(result.BookedLabels),
		"booked_labels": result.BookedLabels,
		"failures":      failures,
	})
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
