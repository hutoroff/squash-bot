// Package client provides an HTTP client for the management service.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hutoroff/squash-bot/internal/models"
)

// Client wraps all HTTP calls to the management service.
type Client struct {
	baseURL    string
	apiSecret  string
	httpClient *http.Client
}

// New creates a new Client targeting baseURL (e.g. "http://management:8080").
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

func (c *Client) CreateGame(ctx context.Context, chatID int64, gameDate time.Time, courts string, venueID *int64, actorTgID int64, actorDisplay string) (*models.Game, error) {
	body := map[string]any{
		"chat_id":           chatID,
		"game_date":         gameDate,
		"courts":            courts,
		"venue_id":          venueID,
		"actor_telegram_id": actorTgID,
		"actor_display":     actorDisplay,
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

func (c *Client) UpdateCourts(ctx context.Context, gameID, groupID int64, courts, actorDisplay string, actorTgID int64) error {
	body := map[string]any{
		"courts":            courts,
		"group_id":          groupID,
		"actor_telegram_id": actorTgID,
		"actor_display":     actorDisplay,
	}
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
	GroupID    int64  `json:"group_id"`
}

func (c *Client) Join(ctx context.Context, gameID, chatID, telegramID int64, username, firstName, lastName string) ([]*models.GameParticipation, error) {
	body := playerBody{TelegramID: telegramID, Username: username, FirstName: firstName, LastName: lastName, GroupID: chatID}
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

func (c *Client) Skip(ctx context.Context, gameID, chatID, telegramID int64, username, firstName, lastName string) ([]*models.GameParticipation, bool, error) {
	body := playerBody{TelegramID: telegramID, Username: username, FirstName: firstName, LastName: lastName, GroupID: chatID}
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

func (c *Client) AddGuest(ctx context.Context, gameID, chatID, telegramID int64, username, firstName, lastName string) (bool, []*models.GameParticipation, []*models.GuestParticipation, error) {
	body := playerBody{TelegramID: telegramID, Username: username, FirstName: firstName, LastName: lastName, GroupID: chatID}
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

func (c *Client) RemoveGuest(ctx context.Context, gameID, chatID, telegramID int64, username, firstName, lastName string) (bool, []*models.GameParticipation, []*models.GuestParticipation, error) {
	body := map[string]any{
		"telegram_id": telegramID,
		"group_id":    chatID,
		"username":    username,
		"first_name":  firstName,
		"last_name":   lastName,
	}
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

func (c *Client) KickPlayer(ctx context.Context, gameID, telegramID, groupID, actorTgID int64, actorDisplay string) ([]*models.GameParticipation, []*models.GuestParticipation, bool, error) {
	path := fmt.Sprintf("/api/v1/games/%d/players/%d?group_id=%d&actor_tg_id=%d&actor_display=%s",
		gameID, telegramID, groupID, actorTgID, url.QueryEscape(actorDisplay))
	var resp kickResponse
	if err := c.do(ctx, http.MethodDelete, path, nil, &resp); err != nil {
		return nil, nil, false, err
	}
	return resp.Participations, resp.Guests, resp.Removed, nil
}

func (c *Client) KickGuestByID(ctx context.Context, gameID, guestID, groupID, actorTgID int64, actorDisplay string) ([]*models.GameParticipation, []*models.GuestParticipation, bool, error) {
	path := fmt.Sprintf("/api/v1/games/%d/guests/%d?group_id=%d&actor_tg_id=%d&actor_display=%s",
		gameID, guestID, groupID, actorTgID, url.QueryEscape(actorDisplay))
	var resp kickResponse
	if err := c.do(ctx, http.MethodDelete, path, nil, &resp); err != nil {
		return nil, nil, false, err
	}
	return resp.Participations, resp.Guests, resp.Removed, nil
}

// ── Groups ────────────────────────────────────────────────────────────────────

func (c *Client) UpsertGroup(ctx context.Context, chatID int64, title string, botIsAdmin bool, actorTgID int64, actorDisplay string, isNewJoin bool) error {
	body := map[string]any{
		"title":             title,
		"bot_is_admin":      botIsAdmin,
		"is_new_join":       isNewJoin,
		"actor_telegram_id": actorTgID,
		"actor_display":     actorDisplay,
	}
	return c.do(ctx, http.MethodPut, "/api/v1/groups/"+strconv.FormatInt(chatID, 10), body, nil)
}

func (c *Client) RemoveGroup(ctx context.Context, chatID, actorTgID int64, actorDisplay, groupTitle string) error {
	path := fmt.Sprintf("/api/v1/groups/%d?actor_tg_id=%d&actor_display=%s&group_title=%s",
		chatID, actorTgID, url.QueryEscape(actorDisplay), url.QueryEscape(groupTitle))
	return c.do(ctx, http.MethodDelete, path, nil, nil)
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
func (c *Client) SetGroupLanguage(ctx context.Context, chatID int64, language string, actorTgID int64, actorDisplay string) error {
	body := map[string]any{
		"language":          language,
		"actor_telegram_id": actorTgID,
		"actor_display":     actorDisplay,
	}
	return c.do(ctx, http.MethodPatch, "/api/v1/groups/"+strconv.FormatInt(chatID, 10)+"/language", body, nil)
}

// SetGroupTimezone sets the IANA timezone for a group.
func (c *Client) SetGroupTimezone(ctx context.Context, chatID int64, timezone string, actorTgID int64, actorDisplay string) error {
	body := map[string]any{
		"timezone":          timezone,
		"actor_telegram_id": actorTgID,
		"actor_display":     actorDisplay,
	}
	return c.do(ctx, http.MethodPatch, "/api/v1/groups/"+strconv.FormatInt(chatID, 10)+"/timezone", body, nil)
}

// SetGroupChangelog sets the changelog_enabled flag for a group.
func (c *Client) SetGroupChangelog(ctx context.Context, chatID int64, enabled bool, actorTgID int64, actorDisplay string) error {
	body := map[string]any{
		"changelog_enabled": enabled,
		"actor_telegram_id": actorTgID,
		"actor_display":     actorDisplay,
	}
	return c.do(ctx, http.MethodPatch, "/api/v1/groups/"+strconv.FormatInt(chatID, 10)+"/changelog", body, nil)
}

func (c *Client) SetGroupAutoBookingAllowed(ctx context.Context, chatID int64, allowed bool, actorTgID int64, actorDisplay string) error {
	body := map[string]any{
		"enabled":           allowed,
		"actor_telegram_id": actorTgID,
		"actor_display":     actorDisplay,
	}
	return c.do(ctx, http.MethodPatch, "/api/v1/groups/"+strconv.FormatInt(chatID, 10)+"/auto-booking-allowed", body, nil)
}

// ── Venues ────────────────────────────────────────────────────────────────────

type venueBody struct {
	GroupID                int64  `json:"group_id"`
	Name                   string `json:"name"`
	Courts                 string `json:"courts"`
	TimeSlots              string `json:"time_slots"`
	Address                string `json:"address,omitempty"`
	GracePeriodHours       int    `json:"grace_period_hours"`
	GameDays               string `json:"game_days"`
	BookingOpensDays       int    `json:"booking_opens_days"`
	PreferredGameTimes     string `json:"preferred_game_times"`
	AutoBookingCourts      string `json:"auto_booking_courts"`
	AutoBookingEnabled     bool   `json:"auto_booking_enabled"`
	AutoBookingCourtsCount int    `json:"auto_booking_courts_count"`
	ActorTelegramID        int64  `json:"actor_telegram_id,omitempty"`
	ActorDisplay           string `json:"actor_display,omitempty"`
}

func (c *Client) CreateVenue(ctx context.Context, groupID int64, name, courts, timeSlots, address string, gracePeriodHours int, gameDays string, bookingOpensDays int, preferredGameTimes, autoBookingCourts string, autoBookingEnabled bool, autoBookingCourtsCount int, actorTgID int64, actorDisplay string) (*models.Venue, error) {
	body := venueBody{
		GroupID: groupID, Name: name, Courts: courts, TimeSlots: timeSlots, Address: address,
		GracePeriodHours: gracePeriodHours, GameDays: gameDays, BookingOpensDays: bookingOpensDays,
		PreferredGameTimes: preferredGameTimes, AutoBookingCourts: autoBookingCourts,
		AutoBookingEnabled: autoBookingEnabled, AutoBookingCourtsCount: autoBookingCourtsCount,
		ActorTelegramID: actorTgID, ActorDisplay: actorDisplay,
	}
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

func (c *Client) UpdateVenue(ctx context.Context, id, groupID int64, name, courts, timeSlots, address string, gracePeriodHours int, gameDays string, bookingOpensDays int, preferredGameTimes, autoBookingCourts string, autoBookingEnabled bool, autoBookingCourtsCount int, actorTgID int64, actorDisplay string) (*models.Venue, error) {
	body := venueBody{
		GroupID: groupID, Name: name, Courts: courts, TimeSlots: timeSlots, Address: address,
		GracePeriodHours: gracePeriodHours, GameDays: gameDays, BookingOpensDays: bookingOpensDays,
		PreferredGameTimes: preferredGameTimes, AutoBookingCourts: autoBookingCourts,
		AutoBookingEnabled: autoBookingEnabled, AutoBookingCourtsCount: autoBookingCourtsCount,
		ActorTelegramID: actorTgID, ActorDisplay: actorDisplay,
	}
	var venue models.Venue
	if err := c.do(ctx, http.MethodPatch, "/api/v1/venues/"+strconv.FormatInt(id, 10), body, &venue); err != nil {
		return nil, err
	}
	return &venue, nil
}

func (c *Client) DeleteVenue(ctx context.Context, id, groupID, actorTgID int64, actorDisplay string) error {
	path := fmt.Sprintf("/api/v1/venues/%d?group_id=%d&actor_tg_id=%d&actor_display=%s",
		id, groupID, actorTgID, url.QueryEscape(actorDisplay))
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// ── Venue credentials ─────────────────────────────────────────────────────────

func (c *Client) AddVenueCredential(ctx context.Context, venueID, groupID int64, login, password string, priority, maxCourts int, actorTgID int64, actorDisplay string) (*models.VenueCredential, error) {
	body := map[string]any{
		"group_id":          groupID,
		"login":             login,
		"password":          password,
		"priority":          priority,
		"max_courts":        maxCourts,
		"actor_telegram_id": actorTgID,
		"actor_display":     actorDisplay,
	}
	var cred models.VenueCredential
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/venues/%d/credentials", venueID), body, &cred); err != nil {
		return nil, err
	}
	return &cred, nil
}

func (c *Client) ListVenueCredentials(ctx context.Context, venueID, groupID int64) ([]*models.VenueCredential, error) {
	path := fmt.Sprintf("/api/v1/venues/%d/credentials?group_id=%d", venueID, groupID)
	var creds []*models.VenueCredential
	if err := c.do(ctx, http.MethodGet, path, nil, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

func (c *Client) DeleteVenueCredential(ctx context.Context, venueID, credentialID, groupID, actorTgID int64, actorDisplay string) error {
	path := fmt.Sprintf("/api/v1/venues/%d/credentials/%d?group_id=%d&actor_tg_id=%d&actor_display=%s",
		venueID, credentialID, groupID, actorTgID, url.QueryEscape(actorDisplay))
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

func (c *Client) ListVenueCredentialPriorities(ctx context.Context, venueID, groupID int64) ([]int, error) {
	path := fmt.Sprintf("/api/v1/venues/%d/credentials/priorities?group_id=%d", venueID, groupID)
	var priorities []int
	if err := c.do(ctx, http.MethodGet, path, nil, &priorities); err != nil {
		return nil, err
	}
	return priorities, nil
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

// CourtBookingInfo is a slim DTO for an active court booking returned by ListActiveCourtBookings.
type CourtBookingInfo struct {
	CourtLabel string `json:"court_label"`
	GameTime   string `json:"game_time"`
	MatchID    string `json:"match_id"`
}

// CancelFailure describes a court whose booking cancellation failed.
type CancelFailure struct {
	Court  string `json:"court"`
	Reason string `json:"reason"`
}

// ListActiveCourtBookings returns active Eversports bookings for the given court labels on a game.
func (c *Client) ListActiveCourtBookings(ctx context.Context, gameID int64, courts []string) ([]CourtBookingInfo, error) {
	path := fmt.Sprintf("/api/v1/games/%d/active-court-bookings?courts=%s",
		gameID, url.QueryEscape(strings.Join(courts, ",")))
	var infos []CourtBookingInfo
	if err := c.do(ctx, http.MethodGet, path, nil, &infos); err != nil {
		return nil, err
	}
	return infos, nil
}

// UpdateCourtsAndCancelBookings cancels active bookings for removed courts then persists the new courts list.
// On partial failure, failed contains per-court errors and courts are still updated.
func (c *Client) UpdateCourtsAndCancelBookings(ctx context.Context, gameID, groupID int64, newCourts, actorDisplay string, actorTgID int64) (canceledLabels []string, failed []CancelFailure, err error) {
	body := map[string]any{
		"courts":            newCourts,
		"group_id":          groupID,
		"actor_telegram_id": actorTgID,
		"actor_display":     actorDisplay,
		"cancel_bookings":   true,
	}
	req, reqErr := c.newRequest(ctx, http.MethodPatch, "/api/v1/games/"+strconv.FormatInt(gameID, 10)+"/courts", body)
	if reqErr != nil {
		return nil, nil, reqErr
	}
	resp, doErr := c.httpClient.Do(req)
	if doErr != nil {
		return nil, nil, fmt.Errorf("PATCH /courts: %w", doErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, nil, parseErrorBody(resp)
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil, nil
	}
	var partial struct {
		Canceled []string        `json:"canceled"`
		Failed   []CancelFailure `json:"failed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&partial); err != nil {
		return nil, nil, fmt.Errorf("decode partial response: %w", err)
	}
	return partial.Canceled, partial.Failed, nil
}

// ErrAlreadyPublished is returned by PublishGame when the management service responds with HTTP 409.
var ErrAlreadyPublished = errors.New("already published")

// ErrAutoBookingNotAvailable is returned by BookGameCourts when the management service responds with HTTP 409.
var ErrAutoBookingNotAvailable = errors.New("auto-booking not available")

// BookGameCourtsResult is the result of booking courts for a game on demand.
type BookGameCourtsResult struct {
	Requested    int                   `json:"requested"`
	BookedCount  int                   `json:"booked_count"`
	BookedLabels []string              `json:"booked_labels"`
	Failures     []BookingCourtsFailure `json:"failures"`
}

// BookingCourtsFailure describes a single booking attempt that failed.
type BookingCourtsFailure struct {
	Reason string `json:"reason"`
}

// BookingReadiness describes whether a venue is ready to auto-book courts.
type BookingReadiness struct {
	Ready     bool   `json:"ready"`
	MaxCourts int    `json:"max_courts"`
	Reason    string `json:"reason"`
}

// PublishGame sends the game announcement and pins it.
// Returns ErrAlreadyPublished if the game is already published (HTTP 409).
func (c *Client) PublishGame(ctx context.Context, gameID, actorTgID int64, actorDisplay string) (*models.Game, error) {
	body := map[string]any{
		"actor_telegram_id": actorTgID,
		"actor_display":     actorDisplay,
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/api/v1/games/"+strconv.FormatInt(gameID, 10)+"/publish", body)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST /publish: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		return nil, ErrAlreadyPublished
	}
	if resp.StatusCode >= 400 {
		return nil, parseErrorBody(resp)
	}

	var game models.Game
	if err := json.NewDecoder(resp.Body).Decode(&game); err != nil {
		return nil, fmt.Errorf("decode publish response: %w", err)
	}
	return &game, nil
}

// BookGameCourts calls POST /api/v1/games/{id}/book-courts.
// Returns ErrAutoBookingNotAvailable when the server responds with HTTP 409.
func (c *Client) BookGameCourts(ctx context.Context, gameID, groupID, actorTgID int64, actorDisplay string, count int) (*BookGameCourtsResult, error) {
	body := map[string]any{
		"count":             count,
		"group_id":          groupID,
		"actor_telegram_id": actorTgID,
		"actor_display":     actorDisplay,
	}
	req, err := c.newRequest(ctx, http.MethodPost, fmt.Sprintf("/api/v1/games/%d/book-courts", gameID), body)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST /book-courts: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		return nil, ErrAutoBookingNotAvailable
	}
	if resp.StatusCode >= 400 {
		return nil, parseErrorBody(resp)
	}
	var result BookGameCourtsResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode book-courts response: %w", err)
	}
	return &result, nil
}

// GetVenueBookingReadiness calls GET /api/v1/venues/{id}/booking-readiness.
func (c *Client) GetVenueBookingReadiness(ctx context.Context, venueID, groupID int64) (*BookingReadiness, error) {
	path := fmt.Sprintf("/api/v1/venues/%d/booking-readiness?group_id=%d", venueID, groupID)
	var r BookingReadiness
	if err := c.do(ctx, http.MethodGet, path, nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
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

// HTTPError is a typed error returned by the management service client when the
// server responds with a non-2xx status. Callers can use errors.As to inspect
// the StatusCode and act on specific codes (e.g. 409 Conflict).
type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

// parseErrorBody reads the {"error": "..."} body from an error response.
func parseErrorBody(resp *http.Response) error {
	var errBody struct {
		Error string `json:"error"`
	}
	data, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(data, &errBody)
	return &HTTPError{StatusCode: resp.StatusCode, Message: errBody.Error}
}

// GetPlayerByTelegramID fetches a player by Telegram user ID.
// Returns a nil *models.Player if not found (HTTP 404).
func (c *Client) GetPlayerByTelegramID(ctx context.Context, telegramID int64) (*models.Player, error) {
	var p models.Player
	if err := c.do(ctx, http.MethodGet, "/api/v1/players/"+strconv.FormatInt(telegramID, 10), nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ── Game Results ──────────────────────────────────────────────────────────────

// GameResultDTO is the client-side representation of a game result.
type GameResultDTO struct {
	ID                int64   `json:"id"`
	GameID            int64   `json:"game_id"`
	GroupID           int64   `json:"group_id"`
	AuthorID          int64   `json:"author_id"`
	OpponentID        int64   `json:"opponent_id"`
	WinnerID          *int64  `json:"winner_id,omitempty"`
	Score             string  `json:"score"`
	Status            string  `json:"status"`
	SubmittedAt       string  `json:"submitted_at"`
	DecidedAt         *string `json:"decided_at,omitempty"`
	ApprovalChatID    *int64  `json:"approval_chat_id,omitempty"`
	ApprovalMessageID *int    `json:"approval_message_id,omitempty"`
	AutoApproveAt     *string `json:"auto_approve_at,omitempty"`
}

// ErrGameResultNotPending is returned when trying to act on a non-pending result.
var ErrGameResultNotPending = errors.New("game result is not pending")

func (c *Client) SubmitGameResult(ctx context.Context, gameID, authorTgID, opponentPlayerID int64, winnerPlayerID *int64, score, actorDisplay string) (*GameResultDTO, error) {
	body := map[string]any{
		"game_id":            gameID,
		"author_telegram_id": authorTgID,
		"opponent_player_id": opponentPlayerID,
		"winner_player_id":   winnerPlayerID,
		"score":              score,
		"actor_display":      actorDisplay,
	}
	var result GameResultDTO
	if err := c.do(ctx, http.MethodPost, "/api/v1/game-results", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetGameResult(ctx context.Context, id int64) (*GameResultDTO, error) {
	var result GameResultDTO
	if err := c.do(ctx, http.MethodGet, "/api/v1/game-results/"+strconv.FormatInt(id, 10), nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) SetGameResultApprovalMessage(ctx context.Context, id, chatID int64, messageID int) error {
	body := map[string]any{"chat_id": chatID, "message_id": messageID}
	return c.do(ctx, http.MethodPost, "/api/v1/game-results/"+strconv.FormatInt(id, 10)+"/approval-message", body, nil)
}

func (c *Client) ApproveGameResult(ctx context.Context, id, actorTgID int64, actorDisplay string) (*GameResultDTO, error) {
	return c.resultDecision(ctx, id, actorTgID, actorDisplay, "approve")
}

func (c *Client) RejectGameResult(ctx context.Context, id, actorTgID int64, actorDisplay string) (*GameResultDTO, error) {
	return c.resultDecision(ctx, id, actorTgID, actorDisplay, "reject")
}

func (c *Client) CancelGameResult(ctx context.Context, id, actorTgID int64, actorDisplay string) (*GameResultDTO, error) {
	return c.resultDecision(ctx, id, actorTgID, actorDisplay, "cancel")
}

func (c *Client) resultDecision(ctx context.Context, id, actorTgID int64, actorDisplay, action string) (*GameResultDTO, error) {
	body := map[string]any{
		"actor_telegram_id": actorTgID,
		"actor_display":     actorDisplay,
	}
	path := fmt.Sprintf("/api/v1/game-results/%d/%s", id, action)
	req, err := c.newRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		return nil, ErrGameResultNotPending
	}
	if resp.StatusCode >= 400 {
		return nil, parseErrorBody(resp)
	}
	var result GameResultDTO
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &result, nil
}

func (c *Client) GetRecentCompletedGames(ctx context.Context, playerTgID, groupID int64, days int) ([]models.PlayerGame, error) {
	path := fmt.Sprintf("/api/v1/players/%d/recent-completed-games?group_id=%d&days=%d",
		playerTgID, groupID, days)
	var games []models.PlayerGame
	if err := c.do(ctx, http.MethodGet, path, nil, &games); err != nil {
		return nil, err
	}
	return games, nil
}
