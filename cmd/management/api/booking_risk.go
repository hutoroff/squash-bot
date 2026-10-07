package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/jackc/pgx/v5"
)

// authorizeBookingRisk is never called by schedulers or ordinary manual
// booking. It requires a web-signed identity, management's live owner check,
// an explicit acknowledgement and a revision-bound parent decision.
func (h *Handler) authorizeBookingRisk(w http.ResponseWriter, r *http.Request) {
	if h.riskBooking == nil {
		writeError(w, http.StatusServiceUnavailable, "risk authorization is not configured")
		return
	}
	actorID, ok := h.riskActor(w, r)
	if !ok {
		return
	}
	parentID, err := bookingapi.ParseOperationID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid operation ID")
		return
	}
	var request struct {
		ExpectedRevision    int64  `json:"expected_revision"`
		CredentialID        int64  `json:"credential_id"`
		Reason              string `json:"reason"`
		AcceptDuplicateRisk bool   `json:"accept_duplicate_risk"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(&struct{}{}) != io.EOF || !request.AcceptDuplicateRisk {
		writeError(w, http.StatusBadRequest, "explicit duplicate-risk acknowledgement required")
		return
	}
	child, created, err := h.riskBooking.AuthorizeAndDispatch(r.Context(), parentID, request.ExpectedRevision, request.CredentialID, actorID, request.Reason)
	if err != nil {
		if child != nil {
			// A child key is durable even if its one authorized dispatch was
			// skipped or ambiguous. Return it for inspection; no new key.
			writeJSON(w, http.StatusAccepted, projectManagementBookingOperation(child))
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "booking operation not found")
			return
		}
		var conflict *storage.BookingIntentConflictError
		if errors.Is(err, service.ErrRiskNotEligible) || errors.As(err, &conflict) {
			writeError(w, http.StatusConflict, "duplicate-risk operation is not eligible")
			return
		}
		h.logger.Warn("duplicate-risk operation unavailable", "parent_operation_id", parentID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "duplicate-risk decision unavailable")
		return
	}
	if !created {
		writeJSON(w, http.StatusOK, projectManagementBookingOperation(child))
		return
	}
	writeJSON(w, http.StatusAccepted, projectManagementBookingOperation(child))
}
