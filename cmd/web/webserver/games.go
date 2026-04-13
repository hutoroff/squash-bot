package webserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// mgmtPlayer is a player record as returned by the management service.
type mgmtPlayer struct {
	TelegramID int64   `json:"telegram_id"`
	Username   *string `json:"username"`
	FirstName  *string `json:"first_name"`
	LastName   *string `json:"last_name"`
}

// mgmtParticipation is a participation record as returned by the management service.
type mgmtParticipation struct {
	ID     int64      `json:"id"`
	Player mgmtPlayer `json:"player"`
	Status string     `json:"status"`
}

// mgmtGuest is a guest participation record as returned by the management service.
type mgmtGuest struct {
	ID        int64      `json:"id"`
	InvitedBy mgmtPlayer `json:"invited_by"`
}

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

// fetchParticipantsData fetches participations and guests for a game from the management service.
func (g *GamesHandler) fetchParticipantsData(ctx context.Context, gameID string) ([]mgmtParticipation, []mgmtGuest, error) {
	partsReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/api/v1/games/%s/participations", g.mgmtURL, gameID), nil)
	if err != nil {
		return nil, nil, err
	}
	partsReq.Header.Set("Authorization", "Bearer "+g.mgmtSecret)
	partsResp, err := g.httpClient.Do(partsReq)
	if err != nil {
		return nil, nil, err
	}
	defer partsResp.Body.Close()
	if partsResp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, partsResp.Body) //nolint:errcheck
		return nil, nil, fmt.Errorf("management returned %d for participations", partsResp.StatusCode)
	}
	var parts []mgmtParticipation
	if err := json.NewDecoder(partsResp.Body).Decode(&parts); err != nil {
		return nil, nil, err
	}

	guestsReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/api/v1/games/%s/guests", g.mgmtURL, gameID), nil)
	if err != nil {
		return nil, nil, err
	}
	guestsReq.Header.Set("Authorization", "Bearer "+g.mgmtSecret)
	guestsResp, err := g.httpClient.Do(guestsReq)
	if err != nil {
		return nil, nil, err
	}
	defer guestsResp.Body.Close()
	if guestsResp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, guestsResp.Body) //nolint:errcheck
		return nil, nil, fmt.Errorf("management returned %d for guests", guestsResp.StatusCode)
	}
	var guests []mgmtGuest
	if err := json.NewDecoder(guestsResp.Body).Decode(&guests); err != nil {
		return nil, nil, err
	}

	if parts == nil {
		parts = []mgmtParticipation{}
	}
	if guests == nil {
		guests = []mgmtGuest{}
	}
	return parts, guests, nil
}

// writeParticipantsResponse fetches the current participants state and writes it to w.
func (g *GamesHandler) writeParticipantsResponse(w http.ResponseWriter, r *http.Request, gameID string) {
	parts, guests, err := g.fetchParticipantsData(r.Context(), gameID)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"upstream unavailable"}`)) //nolint:errcheck
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"participations": parts,
		"guests":         guests,
	})
}

// handleGetParticipants handles GET /api/games/{id}/participants.
func (g *GamesHandler) handleGetParticipants(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if _, err := g.auth.claimsFromRequest(r); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`)) //nolint:errcheck
		return
	}
	g.writeParticipantsResponse(w, r, r.PathValue("id"))
}

// handleJoinGame handles POST /api/games/{id}/join.
func (g *GamesHandler) handleJoinGame(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	claims, err := g.auth.claimsFromRequest(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`)) //nolint:errcheck
		return
	}
	id := r.PathValue("id")
	body, _ := json.Marshal(map[string]any{
		"telegram_id": claims.TelegramID,
		"username":    claims.Username,
		"first_name":  claims.FirstName,
		"last_name":   claims.LastName,
	})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		fmt.Sprintf("%s/api/v1/games/%s/join", g.mgmtURL, id), bytes.NewReader(body))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal error"}`)) //nolint:errcheck
		return
	}
	req.Header.Set("Authorization", "Bearer "+g.mgmtSecret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.httpClient.Do(req)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"upstream unavailable"}`)) //nolint:errcheck
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"action failed"}`)) //nolint:errcheck
		return
	}
	g.writeParticipantsResponse(w, r, id)
}

// handleSkipGame handles POST /api/games/{id}/skip.
func (g *GamesHandler) handleSkipGame(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	claims, err := g.auth.claimsFromRequest(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`)) //nolint:errcheck
		return
	}
	id := r.PathValue("id")
	body, _ := json.Marshal(map[string]any{
		"telegram_id": claims.TelegramID,
		"username":    claims.Username,
		"first_name":  claims.FirstName,
		"last_name":   claims.LastName,
	})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		fmt.Sprintf("%s/api/v1/games/%s/skip", g.mgmtURL, id), bytes.NewReader(body))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal error"}`)) //nolint:errcheck
		return
	}
	req.Header.Set("Authorization", "Bearer "+g.mgmtSecret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.httpClient.Do(req)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"upstream unavailable"}`)) //nolint:errcheck
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"action failed"}`)) //nolint:errcheck
		return
	}
	g.writeParticipantsResponse(w, r, id)
}

// handleAddGuest handles POST /api/games/{id}/guest.
func (g *GamesHandler) handleAddGuest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	claims, err := g.auth.claimsFromRequest(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`)) //nolint:errcheck
		return
	}
	id := r.PathValue("id")
	body, _ := json.Marshal(map[string]any{
		"telegram_id": claims.TelegramID,
		"username":    claims.Username,
		"first_name":  claims.FirstName,
		"last_name":   claims.LastName,
	})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		fmt.Sprintf("%s/api/v1/games/%s/guests", g.mgmtURL, id), bytes.NewReader(body))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal error"}`)) //nolint:errcheck
		return
	}
	req.Header.Set("Authorization", "Bearer "+g.mgmtSecret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.httpClient.Do(req)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"upstream unavailable"}`)) //nolint:errcheck
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"action failed"}`)) //nolint:errcheck
		return
	}
	g.writeParticipantsResponse(w, r, id)
}

// handleRemoveGuest handles DELETE /api/games/{id}/guest.
func (g *GamesHandler) handleRemoveGuest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	claims, err := g.auth.claimsFromRequest(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`)) //nolint:errcheck
		return
	}
	id := r.PathValue("id")
	body, _ := json.Marshal(map[string]any{
		"telegram_id": claims.TelegramID,
	})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodDelete,
		fmt.Sprintf("%s/api/v1/games/%s/guests", g.mgmtURL, id), bytes.NewReader(body))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal error"}`)) //nolint:errcheck
		return
	}
	req.Header.Set("Authorization", "Bearer "+g.mgmtSecret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.httpClient.Do(req)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"upstream unavailable"}`)) //nolint:errcheck
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"action failed"}`)) //nolint:errcheck
		return
	}
	g.writeParticipantsResponse(w, r, id)
}
