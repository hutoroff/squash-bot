package api

import (
	"net/http"

	"github.com/hutoroff/squash-bot/cmd/management/service"
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
// Returns groups the player belongs to that have at least one approved game result.
func (h *Handler) getPlayerGroupsWithResults(w http.ResponseWriter, r *http.Request) {
	tgID, err := parseID(r.PathValue("tgID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid telegram_id")
		return
	}

	// Get all groups, then filter to ones where this player has approved results.
	groups, err := h.groupRepo.GetAll(r.Context())
	if err != nil {
		h.logger.Error("getPlayerGroupsWithResults: get groups", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Filter to groups where the player participated.
	// A quick check: get recent completed games for each group.
	var result []any
	for _, g := range groups {
		games, err := h.gameService.GetRecentCompletedGamesForPlayer(r.Context(), tgID, g.ChatID, 90)
		if err != nil || len(games) == 0 {
			continue
		}
		result = append(result, g)
	}
	if result == nil {
		result = []any{}
	}
	writeJSON(w, http.StatusOK, result)
}
