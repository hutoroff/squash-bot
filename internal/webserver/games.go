package webserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// GamesHandler serves the /api/games endpoint for the authenticated web frontend.
type GamesHandler struct {
	auth       *AuthHandler
	mgmtURL    string
	mgmtSecret string
	httpClient *http.Client
}

// NewGamesHandler creates a GamesHandler.
func NewGamesHandler(auth *AuthHandler, mgmtURL, mgmtSecret string) *GamesHandler {
	return &GamesHandler{
		auth:       auth,
		mgmtURL:    mgmtURL,
		mgmtSecret: mgmtSecret,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// handleListGames handles GET /api/games.
//
// The player_id is taken exclusively from the validated JWT session cookie —
// never from query parameters or the request body. This guarantees that an
// authenticated user can only retrieve their own games.
//
// If the JWT does not contain a player_id (user authenticated via Telegram but
// has never interacted with the bot), an empty list is returned rather than an
// error, because the user simply has no games yet.
func (g *GamesHandler) handleListGames(w http.ResponseWriter, r *http.Request) {
	claims, err := g.auth.claimsFromRequest(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`)) //nolint:errcheck
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// Resolve the player ID from the JWT, performing a live lookup when the
	// JWT was issued before the player record existed and refreshing the cookie.
	playerID, err := g.auth.resolvePlayerID(w, r, claims)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"upstream unavailable"}`)) //nolint:errcheck
		return
	}
	if playerID == nil {
		// User is authenticated but has no player record yet — no games to show.
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]")) //nolint:errcheck
		return
	}

	url := fmt.Sprintf("%s/api/v1/players/%d/games", g.mgmtURL, *playerID)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal error"}`)) //nolint:errcheck
		return
	}
	req.Header.Set("Authorization", "Bearer "+g.mgmtSecret)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"upstream unavailable"}`)) //nolint:errcheck
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"upstream error"}`)) //nolint:errcheck
		return
	}

	// Decode management service response and re-encode to avoid forwarding
	// internal fields that should not reach the browser.
	var games []struct {
		ID                  int64  `json:"id"`
		GameDate            string `json:"game_date"`
		CourtsCount         int    `json:"courts_count"`
		Courts              string `json:"courts"`
		Completed           bool   `json:"completed"`
		ParticipationStatus string `json:"participation_status"`
		ParticipantCount    int    `json:"participant_count"`
		VenueName           string `json:"venue_name,omitempty"`
		VenueAddress        string `json:"venue_address,omitempty"`
		GroupTitle          string `json:"group_title"`
		Timezone            string `json:"timezone"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&games); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal error"}`)) //nolint:errcheck
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(games) //nolint:errcheck
}
