package webserver

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// VenuesHandler proxies the venue and venue-credential endpoints, scoped to a
// group. Every handler starts with authorizeGroup and forces group_id to the
// path's chatID, so a caller can never reach another group's venues.
type VenuesHandler struct {
	mgmtClient
}

// NewVenuesHandler creates a VenuesHandler.
func NewVenuesHandler(auth *AuthHandler, mgmtURL, mgmtSecret string) *VenuesHandler {
	return &VenuesHandler{newMgmtClient(auth, mgmtURL, mgmtSecret)}
}

// venueScope authorizes the group and parses {venueID}. Callers that only need
// the group use authorizeGroup directly.
func (v *VenuesHandler) venueScope(w http.ResponseWriter, r *http.Request) (claims *JWTClaims, chatID, venueID int64, ok bool) {
	claims, chatID, ok = v.authorizeGroup(w, r)
	if !ok {
		return nil, 0, 0, false
	}
	venueID, err := strconv.ParseInt(r.PathValue("venueID"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid venue id")
		return nil, 0, 0, false
	}
	// GET /api/v1/venues/{id} is the one management read that is not group-scoped,
	// so confirm ownership here before any per-venue call is proxied.
	var venue struct {
		GroupID int64 `json:"group_id"`
	}
	status, err := v.get(r, fmt.Sprintf("/api/v1/venues/%d", venueID), &venue)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "upstream unavailable")
		return nil, 0, 0, false
	}
	if status != http.StatusOK || venue.GroupID != chatID {
		writeAPIError(w, http.StatusNotFound, "venue not found")
		return nil, 0, 0, false
	}
	return claims, chatID, venueID, true
}

// groupQuery builds "?group_id=<chatID>" plus any extra params.
func groupQuery(chatID int64, extra url.Values) string {
	q := url.Values{"group_id": {strconv.FormatInt(chatID, 10)}}
	for k, vs := range extra {
		q[k] = vs
	}
	return "?" + q.Encode()
}

// handleListVenues handles GET /api/groups/{chatID}/venues.
func (v *VenuesHandler) handleListVenues(w http.ResponseWriter, r *http.Request) {
	_, chatID, ok := v.authorizeGroup(w, r)
	if !ok {
		return
	}
	v.proxy(w, r, http.MethodGet, "/api/v1/venues"+groupQuery(chatID, nil), nil)
}

// handleCreateVenue handles POST /api/groups/{chatID}/venues.
func (v *VenuesHandler) handleCreateVenue(w http.ResponseWriter, r *http.Request) {
	claims, chatID, ok := v.authorizeGroup(w, r)
	if !ok {
		return
	}
	body, ok := decodeWithActor(w, r, claims, map[string]any{"group_id": chatID})
	if !ok {
		return
	}
	v.proxy(w, r, http.MethodPost, "/api/v1/venues", body)
}

// handleGetVenue handles GET /api/groups/{chatID}/venues/{venueID}.
func (v *VenuesHandler) handleGetVenue(w http.ResponseWriter, r *http.Request) {
	_, _, venueID, ok := v.venueScope(w, r)
	if !ok {
		return
	}
	v.proxy(w, r, http.MethodGet, fmt.Sprintf("/api/v1/venues/%d", venueID), nil)
}

// handleUpdateVenue handles PATCH /api/groups/{chatID}/venues/{venueID}.
func (v *VenuesHandler) handleUpdateVenue(w http.ResponseWriter, r *http.Request) {
	claims, chatID, venueID, ok := v.venueScope(w, r)
	if !ok {
		return
	}
	body, ok := decodeWithActor(w, r, claims, map[string]any{"group_id": chatID})
	if !ok {
		return
	}
	v.proxy(w, r, http.MethodPatch, fmt.Sprintf("/api/v1/venues/%d", venueID), body)
}

// handleDeleteVenue handles DELETE /api/groups/{chatID}/venues/{venueID}.
func (v *VenuesHandler) handleDeleteVenue(w http.ResponseWriter, r *http.Request) {
	claims, chatID, venueID, ok := v.venueScope(w, r)
	if !ok {
		return
	}
	v.proxy(w, r, http.MethodDelete, fmt.Sprintf("/api/v1/venues/%d%s", venueID, groupQuery(chatID, actorQuery(claims))), nil)
}

// handleBookingReadiness handles GET /api/groups/{chatID}/venues/{venueID}/booking-readiness.
func (v *VenuesHandler) handleBookingReadiness(w http.ResponseWriter, r *http.Request) {
	_, chatID, venueID, ok := v.venueScope(w, r)
	if !ok {
		return
	}
	v.proxy(w, r, http.MethodGet, fmt.Sprintf("/api/v1/venues/%d/booking-readiness%s", venueID, groupQuery(chatID, nil)), nil)
}

// handleListCredentials handles GET /api/groups/{chatID}/venues/{venueID}/credentials.
func (v *VenuesHandler) handleListCredentials(w http.ResponseWriter, r *http.Request) {
	_, chatID, venueID, ok := v.venueScope(w, r)
	if !ok {
		return
	}
	v.proxy(w, r, http.MethodGet, fmt.Sprintf("/api/v1/venues/%d/credentials%s", venueID, groupQuery(chatID, nil)), nil)
}

// handleAddCredential handles POST /api/groups/{chatID}/venues/{venueID}/credentials.
func (v *VenuesHandler) handleAddCredential(w http.ResponseWriter, r *http.Request) {
	claims, chatID, venueID, ok := v.venueScope(w, r)
	if !ok {
		return
	}
	body, ok := decodeWithActor(w, r, claims, map[string]any{"group_id": chatID})
	if !ok {
		return
	}
	v.proxy(w, r, http.MethodPost, fmt.Sprintf("/api/v1/venues/%d/credentials", venueID), body)
}

// handleDeleteCredential handles DELETE /api/groups/{chatID}/venues/{venueID}/credentials/{cid}.
func (v *VenuesHandler) handleDeleteCredential(w http.ResponseWriter, r *http.Request) {
	claims, chatID, venueID, ok := v.venueScope(w, r)
	if !ok {
		return
	}
	credID, err := strconv.ParseInt(r.PathValue("cid"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid credential id")
		return
	}
	v.proxy(w, r, http.MethodDelete,
		fmt.Sprintf("/api/v1/venues/%d/credentials/%d%s", venueID, credID, groupQuery(chatID, actorQuery(claims))), nil)
}

// handleCredentialPriorities handles GET /api/groups/{chatID}/venues/{venueID}/credentials/priorities.
func (v *VenuesHandler) handleCredentialPriorities(w http.ResponseWriter, r *http.Request) {
	_, chatID, venueID, ok := v.venueScope(w, r)
	if !ok {
		return
	}
	v.proxy(w, r, http.MethodGet, fmt.Sprintf("/api/v1/venues/%d/credentials/priorities%s", venueID, groupQuery(chatID, nil)), nil)
}
