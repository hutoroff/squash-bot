package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
)

func (h *Handler) getPlayerByTelegramID(w http.ResponseWriter, r *http.Request) {
	telegramID, err := strconv.ParseInt(r.PathValue("telegramID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid telegram ID")
		return
	}

	player, err := h.playerSvc.GetByTelegramID(r.Context(), telegramID)
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
