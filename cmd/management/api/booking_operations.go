package api

import (
	"errors"
	"net/http"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/jackc/pgx/v5"
)

func (h *Handler) getBookingMetrics(w http.ResponseWriter, r *http.Request) {
	if h.bookingMetrics == nil {
		writeError(w, http.StatusServiceUnavailable, "reliable booking integration is not configured")
		return
	}
	metrics, err := h.bookingMetrics.GetMetrics(r.Context())
	if err != nil {
		h.logger.Error("get management booking metrics", "err", err)
		writeError(w, http.StatusInternalServerError, "booking metrics unavailable")
		return
	}
	writeJSON(w, http.StatusOK, metrics)
}

type managementBookingOperationResponse struct {
	OperationID   bookingapi.OperationID                 `json:"operation_id"`
	IntentID      int64                                  `json:"intent_id"`
	ParentID      bookingapi.OperationID                 `json:"parent_operation_id,omitempty"`
	RootID        bookingapi.OperationID                 `json:"root_operation_id,omitempty"`
	Kind          bookingapi.OperationKind               `json:"kind"`
	State         models.ManagementBookingOperationState `json:"state"`
	Phase         bookingapi.OperationPhase              `json:"phase"`
	Outcome       bookingapi.OutcomeCategory             `json:"outcome"`
	Code          bookingapi.ErrorCode                   `json:"code,omitempty"`
	Warnings      []string                               `json:"warnings,omitempty"`
	ResultExpired bool                                   `json:"result_expired,omitempty"`
	CourtUUID     string                                 `json:"court_uuid,omitempty"`
	CourtLabel    string                                 `json:"court_label,omitempty"`
	TargetMatchID string                                 `json:"target_match_id,omitempty"`
	BookingUUID   string                                 `json:"booking_uuid,omitempty"`
	BookingID     *int64                                 `json:"booking_id,omitempty"`
	PaymentID     *int64                                 `json:"payment_id,omitempty"`
	MatchID       string                                 `json:"match_id,omitempty"`
	ProviderState string                                 `json:"provider_state,omitempty"`
	Revision      int64                                  `json:"revision"`
}

func (h *Handler) getBookingOperation(w http.ResponseWriter, r *http.Request) {
	if h.bookingIntents == nil {
		writeError(w, http.StatusServiceUnavailable, "reliable booking integration is not configured")
		return
	}
	operationID, err := bookingapi.ParseOperationID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid operation ID")
		return
	}
	operation, err := h.bookingIntents.GetOperation(r.Context(), operationID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "booking operation not found")
		return
	}
	if err != nil {
		h.logger.Error("get management booking operation", "operation_id", operationID, "err", err)
		writeError(w, http.StatusInternalServerError, "booking operation unavailable")
		return
	}
	writeJSON(w, http.StatusOK, projectManagementBookingOperation(operation))
}

func (h *Handler) reconcileBookingOperation(w http.ResponseWriter, r *http.Request) {
	if h.bookingIntents == nil || h.reliableBooking == nil || h.venueCredService == nil {
		writeError(w, http.StatusServiceUnavailable, "reliable booking integration is not configured")
		return
	}
	operationID, err := bookingapi.ParseOperationID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid operation ID")
		return
	}
	operation, err := h.bookingIntents.GetOperation(r.Context(), operationID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "booking operation not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "booking operation unavailable")
		return
	}
	if operation.State != models.ManagementBookingOperationOutcomeUnknown || operation.CredentialID == nil || !h.bookingOperationUsesCurrentScope(r, operation) {
		writeError(w, http.StatusConflict, "booking operation is not eligible for reconciliation")
		return
	}
	credential, err := h.venueCredService.GetDecryptedByID(r.Context(), *operation.CredentialID)
	if err != nil {
		writeError(w, http.StatusConflict, "booking credential unavailable")
		return
	}
	operation, err = h.reliableBooking.Reconcile(r.Context(), operationID, bookingapi.Credentials{Email: credential.Login, Password: credential.Password})
	if err != nil {
		h.logger.Warn("management booking reconciliation unresolved", "operation_id", operationID, "err", err)
		writeError(w, http.StatusConflict, "booking reconciliation did not prove a terminal outcome")
		return
	}
	writeJSON(w, http.StatusOK, projectManagementBookingOperation(operation))
}

func (h *Handler) resolveBookingOperation(w http.ResponseWriter, r *http.Request) {
	if h.bookingIntents == nil || h.reliableBooking == nil || h.venueCredService == nil {
		writeError(w, http.StatusServiceUnavailable, "reliable booking integration is not configured")
		return
	}
	operationID, err := bookingapi.ParseOperationID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid operation ID")
		return
	}
	var request struct {
		EventKey         string `json:"event_key"`
		ExpectedRevision int64  `json:"expected_revision"`
		MatchID          string `json:"match_id"`
		Actor            string `json:"actor"`
		Reason           string `json:"reason"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid resolution request")
		return
	}
	eventKey, err := bookingapi.ParseOperationID(request.EventKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid resolution event key")
		return
	}
	operation, err := h.bookingIntents.GetOperation(r.Context(), operationID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "booking operation not found")
		return
	}
	if err != nil || operation.State != models.ManagementBookingOperationOutcomeUnknown || operation.CredentialID == nil || !h.bookingOperationUsesCurrentScope(r, operation) {
		writeError(w, http.StatusConflict, "booking operation is not eligible for resolution")
		return
	}
	credential, err := h.venueCredService.GetDecryptedByID(r.Context(), *operation.CredentialID)
	if err != nil {
		writeError(w, http.StatusConflict, "booking credential unavailable")
		return
	}
	operation, err = h.reliableBooking.Resolve(r.Context(), operationID, eventKey, bookingapi.ResolutionRequest{
		ExpectedRevision: request.ExpectedRevision, MatchID: request.MatchID, Actor: request.Actor, Reason: request.Reason,
	}, bookingapi.Credentials{Email: credential.Login, Password: credential.Password})
	if err != nil {
		h.logger.Warn("management booking resolution rejected", "operation_id", operationID, "err", err)
		writeError(w, http.StatusConflict, "booking resolution was not verified")
		return
	}
	writeJSON(w, http.StatusOK, projectManagementBookingOperation(operation))
}

func (h *Handler) bookingOperationUsesCurrentScope(r *http.Request, operation *models.ManagementBookingOperation) bool {
	intent, err := h.bookingIntents.GetIntent(r.Context(), operation.IntentID)
	if err != nil {
		h.logger.Warn("read management booking operation origin", "operation_id", operation.OperationID, "err", err)
		return false
	}
	return intent.Scope == h.bookingScope
}

func projectManagementBookingOperation(operation *models.ManagementBookingOperation) managementBookingOperationResponse {
	return managementBookingOperationResponse{
		OperationID: operation.OperationID, IntentID: operation.IntentID, ParentID: operation.ParentOperationID, RootID: operation.RootOperationID, Kind: operation.Kind,
		State: operation.State, Phase: operation.Phase, Outcome: operation.Outcome, Code: operation.Code,
		Warnings: operation.Warnings, ResultExpired: operation.ResultExpired,
		CourtUUID: operation.CourtUUID, CourtLabel: operation.CourtLabel, TargetMatchID: operation.TargetMatchID,
		BookingUUID: operation.BookingUUID, BookingID: operation.BookingID, PaymentID: operation.PaymentID,
		MatchID: operation.MatchID, ProviderState: operation.ProviderState, Revision: operation.Revision,
	}
}
