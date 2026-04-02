package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
)

// getPlayerByTelegramID handles GET /api/v1/players/{telegramID}.
// Returns the player record for the given Telegram user ID, or 404 if not found.
// Used by squash-web to link authenticated web users with known bot players.
func (h *Handler) getPlayerByTelegramID(w http.ResponseWriter, r *http.Request) {
	telegramID, err := strconv.ParseInt(r.PathValue("telegramID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid telegram ID")
		return
	}

	player, err := h.playerRepo.GetByTelegramID(r.Context(), telegramID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "player not found")
		return
	}
	if err != nil {
		h.logger.Error("getPlayerByTelegramID", "telegram_id", telegramID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, player)
}
