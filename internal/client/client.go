// Package client provides an HTTP client for the squash-games-management service.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vkhutorov/squash_bot/internal/models"
)

// Client wraps all HTTP calls to the squash-games-management service.
type Client struct {
	baseURL    string
	apiSecret  string
	httpClient *http.Client
}

// New creates a new Client targeting baseURL (e.g. "http://squash-games-management:8080").
// apiSecret is sent as a Bearer token in every request.
func New(baseURL, apiSecret string) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		apiSecret: apiSecret,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ── Games ─────────────────────────────────────────────────────────────────────

func (c *Client) CreateGame(ctx context.Context, chatID int64, gameDate time.Time, courts string, venueID *int64) (*models.Game, error) {
	body := map[string]any{
		"chat_id":   chatID,
		"game_date": gameDate,
		"courts":    courts,
		"venue_id":  venueID,
	}
	var game models.Game
	if err := c.do(ctx, http.MethodPost, "/api/v1/games", body, &game); err != nil {
		return nil, err
	}
	return &game, nil
}

func (c *Client) GetGameByID(ctx context.Context, id int64) (*models.Game, error) {
	var game models.Game
	if err := c.do(ctx, http.MethodGet, "/api/v1/games/"+strconv.FormatInt(id, 10), nil, &game); err != nil {
		return nil, err
	}
	return &game, nil
}

func (c *Client) UpdateMessageID(ctx context.Context, gameID, messageID int64) error {
	body := map[string]int64{"message_id": messageID}
	return c.do(ctx, http.MethodPatch, "/api/v1/games/"+strconv.FormatInt(gameID, 10)+"/message-id", body, nil)
}

func (c *Client) UpdateCourts(ctx context.Context, gameID int64, courts string) error {
	body := map[string]string{"courts": courts}
	return c.do(ctx, http.MethodPatch, "/api/v1/games/"+strconv.FormatInt(gameID, 10)+"/courts", body, nil)
}

func (c *Client) GetUpcomingGames(ctx context.Context) ([]*models.Game, error) {
	var games []*models.Game
	if err := c.do(ctx, http.MethodGet, "/api/v1/games?upcoming=true", nil, &games); err != nil {
		return nil, err
	}
	return games, nil
}

func (c *Client) GetUpcomingGamesByChatIDs(ctx context.Context, chatIDs []int64) ([]*models.Game, error) {
	parts := make([]string, len(chatIDs))
	for i, id := range chatIDs {
		parts[i] = strconv.FormatInt(id, 10)
	}
	path := "/api/v1/games?upcoming=true&chat_ids=" + strings.Join(parts, ",")
	var games []*models.Game
	if err := c.do(ctx, http.MethodGet, path, nil, &games); err != nil {
		return nil, err
	}
	return games, nil
}

func (c *Client) GetNextGameForTelegramUser(ctx context.Context, telegramID int64) (*models.Game, error) {
	path := "/api/v1/players/" + strconv.FormatInt(telegramID, 10) + "/next-game"
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil // no upcoming game
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseErrorBody(resp)
	}
	var game models.Game
	if err := json.NewDecoder(resp.Body).Decode(&game); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &game, nil
}

// ── Participations ────────────────────────────────────────────────────────────

type playerBody struct {
	TelegramID int64  `json:"telegram_id"`
	Username   string `json:"username"`
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
}

func (c *Client) Join(ctx context.Context, gameID, telegramID int64, username, firstName, lastName string) ([]*models.GameParticipation, error) {
	body := playerBody{TelegramID: telegramID, Username: username, FirstName: firstName, LastName: lastName}
	var participations []*models.GameParticipation
	if err := c.do(ctx, http.MethodPost, "/api/v1/games/"+strconv.FormatInt(gameID, 10)+"/join", body, &participations); err != nil {
		return nil, err
	}
	return participations, nil
}

type skipResponse struct {
	Skipped        bool                        `json:"skipped"`
	Participations []*models.GameParticipation `json:"participations"`
}

func (c *Client) Skip(ctx context.Context, gameID, telegramID int64, username, firstName, lastName string) ([]*models.GameParticipation, bool, error) {
	body := playerBody{TelegramID: telegramID, Username: username, FirstName: firstName, LastName: lastName}
	var resp skipResponse
	if err := c.do(ctx, http.MethodPost, "/api/v1/games/"+strconv.FormatInt(gameID, 10)+"/skip", body, &resp); err != nil {
		return nil, false, err
	}
	return resp.Participations, resp.Skipped, nil
}

type guestResponse struct {
	Added          bool                         `json:"added"`
	Participations []*models.GameParticipation  `json:"participations"`
	Guests         []*models.GuestParticipation `json:"guests"`
}

func (c *Client) AddGuest(ctx context.Context, gameID, telegramID int64, username, firstName, lastName string) (bool, []*models.GameParticipation, []*models.GuestParticipation, error) {
	body := playerBody{TelegramID: telegramID, Username: username, FirstName: firstName, LastName: lastName}
	var resp guestResponse
	if err := c.do(ctx, http.MethodPost, "/api/v1/games/"+strconv.FormatInt(gameID, 10)+"/guests", body, &resp); err != nil {
		return false, nil, nil, err
	}
	return resp.Added, resp.Participations, resp.Guests, nil
}

type removeGuestResponse struct {
	Removed        bool                         `json:"removed"`
	Participations []*models.GameParticipation  `json:"participations"`
	Guests         []*models.GuestParticipation `json:"guests"`
}

func (c *Client) RemoveGuest(ctx context.Context, gameID, telegramID int64) (bool, []*models.GameParticipation, []*models.GuestParticipation, error) {
	body := map[string]int64{"telegram_id": telegramID}
	var resp removeGuestResponse
	if err := c.do(ctx, http.MethodDelete, "/api/v1/games/"+strconv.FormatInt(gameID, 10)+"/guests", body, &resp); err != nil {
		return false, nil, nil, err
	}
	return resp.Removed, resp.Participations, resp.Guests, nil
}

func (c *Client) GetParticipations(ctx context.Context, gameID int64) ([]*models.GameParticipation, error) {
	var participations []*models.GameParticipation
	if err := c.do(ctx, http.MethodGet, "/api/v1/games/"+strconv.FormatInt(gameID, 10)+"/participations", nil, &participations); err != nil {
		return nil, err
	}
	return participations, nil
}

func (c *Client) GetGuests(ctx context.Context, gameID int64) ([]*models.GuestParticipation, error) {
	var guests []*models.GuestParticipation
	if err := c.do(ctx, http.MethodGet, "/api/v1/games/"+strconv.FormatInt(gameID, 10)+"/guests", nil, &guests); err != nil {
		return nil, err
	}
	return guests, nil
}

type kickResponse struct {
	Removed        bool                         `json:"removed"`
	Participations []*models.GameParticipation  `json:"participations"`
	Guests         []*models.GuestParticipation `json:"guests"`
}

func (c *Client) KickPlayer(ctx context.Context, gameID, telegramID int64) ([]*models.GameParticipation, []*models.GuestParticipation, bool, error) {
	path := fmt.Sprintf("/api/v1/games/%d/players/%d", gameID, telegramID)
	var resp kickResponse
	if err := c.do(ctx, http.MethodDelete, path, nil, &resp); err != nil {
		return nil, nil, false, err
	}
	return resp.Participations, resp.Guests, resp.Removed, nil
}

func (c *Client) KickGuestByID(ctx context.Context, gameID, guestID int64) ([]*models.GameParticipation, []*models.GuestParticipation, bool, error) {
	path := fmt.Sprintf("/api/v1/games/%d/guests/%d", gameID, guestID)
	var resp kickResponse
	if err := c.do(ctx, http.MethodDelete, path, nil, &resp); err != nil {
		return nil, nil, false, err
	}
	return resp.Participations, resp.Guests, resp.Removed, nil
}

// ── Groups ────────────────────────────────────────────────────────────────────

func (c *Client) UpsertGroup(ctx context.Context, chatID int64, title string, botIsAdmin bool) error {
	body := map[string]any{"title": title, "bot_is_admin": botIsAdmin}
	return c.do(ctx, http.MethodPut, "/api/v1/groups/"+strconv.FormatInt(chatID, 10), body, nil)
}

func (c *Client) RemoveGroup(ctx context.Context, chatID int64) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/groups/"+strconv.FormatInt(chatID, 10), nil, nil)
}

func (c *Client) GetGroups(ctx context.Context) ([]models.Group, error) {
	var groups []models.Group
	if err := c.do(ctx, http.MethodGet, "/api/v1/groups", nil, &groups); err != nil {
		return nil, err
	}
	return groups, nil
}

func (c *Client) GroupExists(ctx context.Context, chatID int64) (bool, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/api/v1/groups/"+strconv.FormatInt(chatID, 10), nil)
	if err != nil {
		return false, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, parseErrorBody(resp)
	}
	return true, nil
}

// GetGroupByID returns the group for the given chat ID, or nil if not found.
func (c *Client) GetGroupByID(ctx context.Context, chatID int64) (*models.Group, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/api/v1/groups/"+strconv.FormatInt(chatID, 10), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseErrorBody(resp)
	}
	var g models.Group
	if err := json.NewDecoder(resp.Body).Decode(&g); err != nil {
		return nil, fmt.Errorf("decode group response: %w", err)
	}
	return &g, nil
}

// SetGroupLanguage sets the language preference for a group.
func (c *Client) SetGroupLanguage(ctx context.Context, chatID int64, language string) error {
	body := map[string]string{"language": language}
	return c.do(ctx, http.MethodPatch, "/api/v1/groups/"+strconv.FormatInt(chatID, 10)+"/language", body, nil)
}

// ── Venues ────────────────────────────────────────────────────────────────────

type venueBody struct {
	GroupID   int64  `json:"group_id"`
	Name      string `json:"name"`
	Courts    string `json:"courts"`
	TimeSlots string `json:"time_slots"`
	Address   string `json:"address,omitempty"`
}

func (c *Client) CreateVenue(ctx context.Context, groupID int64, name, courts, timeSlots, address string) (*models.Venue, error) {
	body := venueBody{GroupID: groupID, Name: name, Courts: courts, TimeSlots: timeSlots, Address: address}
	var venue models.Venue
	if err := c.do(ctx, http.MethodPost, "/api/v1/venues", body, &venue); err != nil {
		return nil, err
	}
	return &venue, nil
}

func (c *Client) GetVenuesByGroup(ctx context.Context, groupID int64) ([]*models.Venue, error) {
	path := "/api/v1/venues?group_id=" + strconv.FormatInt(groupID, 10)
	var venues []*models.Venue
	if err := c.do(ctx, http.MethodGet, path, nil, &venues); err != nil {
		return nil, err
	}
	return venues, nil
}

func (c *Client) GetVenueByID(ctx context.Context, id int64) (*models.Venue, error) {
	var venue models.Venue
	if err := c.do(ctx, http.MethodGet, "/api/v1/venues/"+strconv.FormatInt(id, 10), nil, &venue); err != nil {
		return nil, err
	}
	return &venue, nil
}

func (c *Client) UpdateVenue(ctx context.Context, id, groupID int64, name, courts, timeSlots, address string) (*models.Venue, error) {
	body := venueBody{GroupID: groupID, Name: name, Courts: courts, TimeSlots: timeSlots, Address: address}
	var venue models.Venue
	if err := c.do(ctx, http.MethodPatch, "/api/v1/venues/"+strconv.FormatInt(id, 10), body, &venue); err != nil {
		return nil, err
	}
	return &venue, nil
}

func (c *Client) DeleteVenue(ctx context.Context, id, groupID int64) error {
	path := fmt.Sprintf("/api/v1/venues/%d?group_id=%d", id, groupID)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// ── Version ───────────────────────────────────────────────────────────────────

// GetVersion returns the version string reported by the management service.
func (c *Client) GetVersion(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"version"`
	}
	if err := c.do(ctx, http.MethodGet, "/version", nil, &v); err != nil {
		return "", err
	}
	return v.Version, nil
}

// ── Scheduler ─────────────────────────────────────────────────────────────────

// TriggerScheduledEvent fires the named event (day_before, day_after, weekly_reminder)
// on the management service. The job runs asynchronously on the server side.
func (c *Client) TriggerScheduledEvent(ctx context.Context, event string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/scheduler/trigger/"+event, nil, nil)
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

func (c *Client) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiSecret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// do executes an HTTP request and decodes the JSON response into out (if non-nil).
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return parseErrorBody(resp)
	}

	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response from %s %s: %w", method, path, err)
		}
	}
	return nil
}

// parseErrorBody reads the {"error": "..."} body from an error response.
func parseErrorBody(resp *http.Response) error {
	var errBody struct {
		Error string `json:"error"`
	}
	data, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(data, &errBody); err == nil && errBody.Error != "" {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, errBody.Error)
	}
	return fmt.Errorf("HTTP %d", resp.StatusCode)
}
