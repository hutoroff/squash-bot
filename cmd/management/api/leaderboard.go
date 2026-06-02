package api

import (
	"errors"
	"net/http"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
)

// getGroupLeaderboard handles GET /api/v1/groups/{chatID}/leaderboard
func (h *Handler) getGroupLeaderboard(w http.ResponseWriter, r *http.Request) {
	chatID, err := parseID(r.PathValue("chatID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat_id")
		return
	}
	if h.ratingService == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	entries, err := h.ratingService.GetLeaderboard(r.Context(), chatID)
	if err != nil {
		h.logger.Error("getGroupLeaderboard", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if entries == nil {
		writeJSON(w, http.StatusOK, []service.LeaderboardEntry{})
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

// getPlayerGroupsWithResults handles GET /api/v1/players/{tgID}/groups-with-results
// Returns the groups where the player has at least one game participation,
// so any member can view the leaderboard regardless of personal rated results.
func (h *Handler) getPlayerGroupsWithResults(w http.ResponseWriter, r *http.Request) {
	tgID, err := parseID(r.PathValue("tgID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid telegram_id")
		return
	}
	if h.gameService == nil {
		writeJSON(w, http.StatusOK, []*models.Group{})
		return
	}

	player, err := h.playerRepo.GetByTelegramID(r.Context(), tgID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, []*models.Group{})
			return
		}
		h.logger.Error("getPlayerGroupsWithResults: get player", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	groupIDs, err := h.gameService.ListGroupIDsForPlayer(r.Context(), player.ID)
	if err != nil {
		h.logger.Error("getPlayerGroupsWithResults: list groups", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	result := make([]*models.Group, 0, len(groupIDs))
	for _, gid := range groupIDs {
		g, err := h.groupRepo.GetByID(r.Context(), gid)
		if err != nil || g == nil {
			continue
		}
		result = append(result, g)
	}
	writeJSON(w, http.StatusOK, result)
}
