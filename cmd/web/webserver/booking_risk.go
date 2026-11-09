package webserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
)

// handleAuthorizeBookingRisk is an operator-only API (no UI). Management, not
// this proxy, owns the live server-owner check and the booking policy.
func (u *UsersHandler) handleAuthorizeBookingRisk(w http.ResponseWriter, r *http.Request) {
	if _, ok := u.claims(w, r); !ok {
		return
	}
	// Lax cookies alone do not guard same-site cross-origin POSTs. Require a
	// browser-originated request to the exact origin reached by the operator.
	origin, err := url.Parse(r.Header.Get("Origin"))
	scheme := "http"
	if isSecureRequest(r) {
		scheme = "https"
	}
	if err != nil || origin.Scheme != scheme || origin.Host != r.Host || origin.User != nil || origin.RawQuery != "" || origin.Path != "" {
		writeAPIError(w, http.StatusForbidden, "same-origin request required")
		return
	}
	operationID, err := bookingapi.ParseOperationID(r.PathValue("id"))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid booking operation")
		return
	}
	var payload struct {
		ExpectedRevision    int64  `json:"expected_revision"`
		CredentialID        int64  `json:"credential_id"`
		Reason              string `json:"reason"`
		AcceptDuplicateRisk bool   `json:"accept_duplicate_risk"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeAPIError(w, http.StatusBadRequest, "invalid risk request")
		return
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid risk request")
		return
	}
	u.proxyWithSession(w, r, http.MethodPost, "/api/v1/booking/operations/"+string(operationID)+"/authorize-risk", body, cookie.Value)
}
