package booking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/hutoroff/squash-bot/cmd/booking/eversports"
	bookingservice "github.com/hutoroff/squash-bot/cmd/booking/service"
	"github.com/hutoroff/squash-bot/cmd/booking/storage"
	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/jackc/pgx/v5"
)

// eversportsClient is the subset of *eversports.Client methods used by Handler.
// Defined as an interface to allow test doubles.
// Authentication is handled automatically by the client implementation.
type eversportsClient interface {
	GetMatchByID(ctx context.Context, matchID string) (*eversports.Booking, error)
	CancelMatch(ctx context.Context, matchID string) (*eversports.CancellationResult, error)
	CreateBooking(ctx context.Context, facilityUUID, courtUUID, sportUUID string, start, end time.Time) (*eversports.BookingResult, error)
	GetSlots(ctx context.Context, facilityID string, courtIDs []string, startDate string) ([]eversports.Slot, error)
	GetCourts(ctx context.Context, facilityID, facilitySlug, sportID, sportSlug, sportName, sportUUID, date string) ([]eversports.Court, error)
	GetFacility(ctx context.Context, slug string) (*eversports.Facility, error)
}

// Sport constants for squash (this service is squash-specific).
const (
	squashSportUUID = "b388b6e6-69de-11e8-bdc6-02bd505aa7b2"
	squashSportSlug = "squash"
	squashSportName = "Squash"
	squashSportID   = "496"
)

// Handler wires all HTTP routes for the booking service.
type Handler struct {
	logger       *slog.Logger
	version      string
	facilityID   string
	facilityUUID string
	facilitySlug string

	coordinator         ReliableCoordinator
	reliableStatus      OperationStatusReader
	reliableScope       bookingapi.Scope
	reliableIdentityKey []byte
	reliableEnabled     bool
	responseWait        time.Duration
	statusPollInterval  time.Duration

	// credClients caches per-credential Eversports clients keyed by email:password.
	// This avoids re-logging in on every booking request for the same account.
	credClients sync.Map // map[string]*eversports.Client
}

func NewHandler(logger *slog.Logger, version, facilityID, facilityUUID, facilitySlug string) *Handler {
	return &Handler{
		logger:       logger,
		version:      version,
		facilityID:   facilityID,
		facilityUUID: facilityUUID,
		facilitySlug: facilitySlug,
	}
}

type ReliableCoordinator interface {
	SubmitCreate(context.Context, bookingapi.ValidatedCreate, bookingapi.Credentials) (*storage.Admission, error)
	SubmitCancel(context.Context, bookingapi.ValidatedCancel, bookingapi.Credentials) (*storage.Admission, error)
}

type OperationStatusReader interface {
	GetStatus(context.Context, bookingapi.OperationID) (*bookingapi.OperationStatus, error)
	StatusFromOperation(context.Context, *storage.Operation) (*bookingapi.OperationStatus, error)
}

type ReliableAPIConfig struct {
	Coordinator  ReliableCoordinator
	Status       OperationStatusReader
	Scope        bookingapi.Scope
	IdentityKey  []byte
	ResponseWait time.Duration
	PollInterval time.Duration
}

// ConfigureReliableAPI enables v2 mutation/status routes and disables keyless
// legacy mutations. P6 owns constructing these dependencies during startup.
func (h *Handler) ConfigureReliableAPI(cfg ReliableAPIConfig) error {
	if cfg.Coordinator == nil || cfg.Status == nil {
		return fmt.Errorf("reliable booking coordinator and status reader are required")
	}
	if cfg.ResponseWait == 0 {
		cfg.ResponseWait = 20 * time.Second
	} else if cfg.ResponseWait < 0 {
		return fmt.Errorf("reliable booking response wait must be positive")
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 25 * time.Millisecond
	} else if cfg.PollInterval < 0 {
		return fmt.Errorf("reliable booking poll interval must be positive")
	}
	// Reuse contract validation so an incomplete trusted scope or identity key
	// cannot leave a handler that accepts requests but fails after admission.
	if _, err := bookingapi.ValidateCancel("018f8d2c-6f7a-7b01-9341-aaaaaaaaaaaa", cfg.Scope, cfg.IdentityKey, "config-check", bookingapi.CancelRequest{Email: "config@example.invalid", Password: "config-check"}); err != nil {
		return fmt.Errorf("invalid reliable booking API configuration: %w", err)
	}
	if h.facilityID != cfg.Scope.FacilityID || h.facilityUUID != cfg.Scope.FacilityUUID || h.facilitySlug != cfg.Scope.FacilitySlug {
		return fmt.Errorf("reliable booking scope does not match handler facility configuration")
	}
	h.coordinator = cfg.Coordinator
	h.reliableStatus = cfg.Status
	h.reliableScope = cfg.Scope
	h.reliableIdentityKey = append([]byte(nil), cfg.IdentityKey...)
	h.responseWait = cfg.ResponseWait
	h.statusPollInterval = cfg.PollInterval
	h.reliableEnabled = true
	return nil
}

// getOrCreateCredClient returns a cached eversportsClient for the given credentials,
// creating and storing one if none exists yet.
func (h *Handler) getOrCreateCredClient(email, password string) eversportsClient {
	key := email + ":" + password
	if v, ok := h.credClients.Load(key); ok {
		return v.(eversportsClient)
	}
	c := eversports.New(email, password, h.logger)
	h.credClients.Store(key, c)
	return c
}

// clientFromRequest returns a per-credential client when X-Eversports-Email and
// X-Eversports-Password headers are present, otherwise nil.
func (h *Handler) clientFromRequest(r *http.Request) eversportsClient {
	if email := r.Header.Get("X-Eversports-Email"); email != "" {
		if password := r.Header.Get("X-Eversports-Password"); password != "" {
			return h.getOrCreateCredClient(email, password)
		}
	}
	return nil
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /version", h.getVersion)

	mux.HandleFunc("GET /api/v1/eversports/matches", h.listMatches)
	mux.HandleFunc("POST /api/v1/eversports/matches", h.createMatch)
	mux.HandleFunc("GET /api/v1/eversports/matches/{id}", h.getMatch)
	mux.HandleFunc("DELETE /api/v1/eversports/matches/{id}", h.cancelMatch)
	mux.HandleFunc("GET /api/v1/eversports/courts", h.getCourts)
	mux.HandleFunc("GET /api/v1/eversports/facility", h.getFacility)

	mux.HandleFunc("GET /api/v2/eversports/matches", h.listMatches)
	mux.HandleFunc("POST /api/v2/eversports/matches", h.createMatchV2)
	mux.HandleFunc("GET /api/v2/eversports/matches/{id}", h.getMatch)
	mux.HandleFunc("DELETE /api/v2/eversports/matches/{id}", h.cancelMatchV2)
	mux.HandleFunc("GET /api/v2/eversports/courts", h.getCourts)
	mux.HandleFunc("GET /api/v2/eversports/facility", h.getFacility)
	mux.HandleFunc("GET /api/v2/eversports/operations/{id}", h.getOperationV2)
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok")) //nolint:errcheck
}

func (h *Handler) getVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": h.version})
}

func (h *Handler) getMatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "match id is required")
		return
	}

	es := h.clientFromRequest(r)
	if es == nil {
		writeError(w, http.StatusBadRequest, "X-Eversports-Email and X-Eversports-Password headers are required")
		return
	}

	match, err := es.GetMatchByID(r.Context(), id)
	if err != nil {
		h.logger.Error("eversports get match failed", "id", id, "err", err)
		writeError(w, http.StatusBadGateway, "get match failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, match)
}

// createMatchRequest is the JSON body expected by POST /api/v1/eversports/matches.
// Email and Password are optional: when present the booking is made using a
// per-credential Eversports client instead of the service-level default client.
type createMatchRequest struct {
	CourtUUID string `json:"courtUuid"`
	Start     string `json:"start"`    // RFC 3339, e.g. "2026-04-12T06:45:00Z"
	End       string `json:"end"`      // RFC 3339
	Email     string `json:"email"`    // optional: credential login
	Password  string `json:"password"` // optional: credential password
}

func (h *Handler) createMatch(w http.ResponseWriter, r *http.Request) {
	if h.reliableEnabled {
		h.writeContractError(w, http.StatusConflict, bookingapi.ErrorContractUpgradeRequired,
			"v1 booking mutations are disabled; use the keyed v2 contract", "", "", "")
		return
	}
	if h.facilityUUID == "" {
		writeError(w, http.StatusInternalServerError, "booking creation requires EVERSPORTS_FACILITY_UUID to be configured")
		return
	}

	var req createMatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.CourtUUID == "" {
		writeError(w, http.StatusBadRequest, "courtUuid is required")
		return
	}
	start, err := time.Parse(time.RFC3339, req.Start)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid start time (expected RFC 3339): "+err.Error())
		return
	}
	end, err := time.Parse(time.RFC3339, req.End)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid end time (expected RFC 3339): "+err.Error())
		return
	}
	if !end.After(start) {
		writeError(w, http.StatusBadRequest, "end must be after start")
		return
	}

	h.logger.Debug("booking: create match request",
		"facilityUuid", h.facilityUUID,
		"courtUuid", req.CourtUUID,
		"start_raw", req.Start,
		"end_raw", req.End,
		"start_parsed", start.Format(time.RFC3339),
		"end_parsed", end.Format(time.RFC3339),
		"start_offset", start.Format("-07:00"),
		"has_credentials", req.Email != "",
	)

	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password are required")
		return
	}
	es := h.getOrCreateCredClient(req.Email, req.Password)

	result, err := es.CreateBooking(r.Context(), h.facilityUUID, req.CourtUUID, squashSportUUID, start, end)
	if err != nil {
		h.logger.Error("eversports create booking failed",
			"facilityUuid", h.facilityUUID,
			"courtUuid", req.CourtUUID,
			"start", req.Start,
			"err", err,
		)
		writeError(w, http.StatusBadGateway, "create booking failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

// cancelMatchRequest is the optional JSON body for DELETE /api/v1/eversports/matches/{id}.
// When Email and Password are set, the cancellation uses a per-credential client.
type cancelMatchRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *Handler) cancelMatch(w http.ResponseWriter, r *http.Request) {
	if h.reliableEnabled {
		h.writeContractError(w, http.StatusConflict, bookingapi.ErrorContractUpgradeRequired,
			"v1 booking mutations are disabled; use the keyed v2 contract", "", "", "")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "match id is required")
		return
	}

	var req cancelMatchRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password are required")
		return
	}
	es := h.getOrCreateCredClient(req.Email, req.Password)

	result, err := es.CancelMatch(r.Context(), id)
	if err != nil {
		h.logger.Error("eversports cancel match failed", "id", id, "err", err)
		writeError(w, http.StatusBadGateway, "cancel match failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) createMatchV2(w http.ResponseWriter, r *http.Request) {
	if !h.requireReliableAPI(w) {
		return
	}
	operationID, ok := h.operationKey(w, r)
	if !ok {
		return
	}
	var request bookingapi.CreateRequest
	if !decodeStrictJSON(w, r, &request) {
		return
	}
	validated, err := bookingapi.ValidateCreate(string(operationID), h.reliableScope, h.reliableIdentityKey, request)
	if err != nil {
		h.writeContractError(w, http.StatusBadRequest, bookingapi.ErrorInvalidRequest, "invalid booking request", string(operationID), "", "")
		return
	}
	admission, err := h.coordinator.SubmitCreate(r.Context(), validated, bookingapi.Credentials{Email: request.Email, Password: request.Password})
	if err != nil {
		h.writeSubmissionError(w, operationID, err)
		return
	}
	h.writeAdmission(w, r, admission)
}

func (h *Handler) cancelMatchV2(w http.ResponseWriter, r *http.Request) {
	if !h.requireReliableAPI(w) {
		return
	}
	operationID, ok := h.operationKey(w, r)
	if !ok {
		return
	}
	var request bookingapi.CancelRequest
	if !decodeStrictJSON(w, r, &request) {
		return
	}
	validated, err := bookingapi.ValidateCancel(string(operationID), h.reliableScope, h.reliableIdentityKey, r.PathValue("id"), request)
	if err != nil {
		h.writeContractError(w, http.StatusBadRequest, bookingapi.ErrorInvalidRequest, "invalid cancellation request", string(operationID), "", "")
		return
	}
	admission, err := h.coordinator.SubmitCancel(r.Context(), validated, bookingapi.Credentials{Email: request.Email, Password: request.Password})
	if err != nil {
		h.writeSubmissionError(w, operationID, err)
		return
	}
	h.writeAdmission(w, r, admission)
}

func (h *Handler) getOperationV2(w http.ResponseWriter, r *http.Request) {
	if !h.requireReliableAPI(w) {
		return
	}
	operationID, err := bookingapi.ParseOperationID(r.PathValue("id"))
	if err != nil {
		h.writeContractError(w, http.StatusBadRequest, bookingapi.ErrorInvalidRequest, "invalid operation ID", "", "", "")
		return
	}
	status, err := h.reliableStatus.GetStatus(r.Context(), operationID)
	var expired *bookingservice.ResultExpiredError
	if errors.As(err, &expired) {
		writeJSON(w, http.StatusGone, status)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		h.writeContractError(w, http.StatusNotFound, bookingapi.ErrorOperationNotFound, "operation not found", string(operationID), "", bookingservice.OperationStatusURL(operationID))
		return
	}
	if err != nil {
		h.writeContractError(w, http.StatusServiceUnavailable, bookingapi.ErrorDependencyUnavailable, "operation status unavailable", string(operationID), "", bookingservice.OperationStatusURL(operationID))
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *Handler) writeAdmission(w http.ResponseWriter, r *http.Request, admission *storage.Admission) {
	if admission == nil || admission.Operation == nil {
		h.writeContractError(w, http.StatusServiceUnavailable, bookingapi.ErrorDependencyUnavailable, "operation acceptance unavailable", "", "", "")
		return
	}
	status, err := h.reliableStatus.StatusFromOperation(r.Context(), admission.Operation)
	var expired *bookingservice.ResultExpiredError
	if errors.As(err, &expired) {
		writeJSON(w, http.StatusGone, status)
		return
	}
	if err != nil {
		h.writeContractError(w, http.StatusConflict, bookingapi.ErrorOperationOutcomeUnknown, "operation accepted; inspect status", string(admission.Operation.OperationID), string(admission.Operation.Phase), bookingservice.OperationStatusURL(admission.Operation.OperationID))
		return
	}
	if status.State == bookingapi.StateAccepted || status.State == bookingapi.StateRunning {
		status = h.waitForTerminal(r.Context(), status)
	}
	h.writeMutationStatus(w, status)
}

func (h *Handler) waitForTerminal(requestCtx context.Context, initial *bookingapi.OperationStatus) *bookingapi.OperationStatus {
	ctx, cancel := context.WithTimeout(requestCtx, h.responseWait)
	defer cancel()
	status := initial
	ticker := time.NewTicker(h.statusPollInterval)
	defer ticker.Stop()
	for status.State == bookingapi.StateAccepted || status.State == bookingapi.StateRunning {
		select {
		case <-ctx.Done():
			return status
		case <-ticker.C:
			updated, err := h.reliableStatus.GetStatus(ctx, bookingapi.OperationID(status.OperationID))
			if err == nil {
				status = updated
			}
		}
	}
	return status
}

func (h *Handler) writeMutationStatus(w http.ResponseWriter, status *bookingapi.OperationStatus) {
	if status == nil {
		h.writeContractError(w, http.StatusServiceUnavailable, bookingapi.ErrorDependencyUnavailable, "operation status unavailable", "", "", "")
		return
	}
	w.Header().Set("Location", status.StatusURL)
	httpStatus := http.StatusAccepted
	switch status.State {
	case bookingapi.StateSucceeded:
		httpStatus = http.StatusOK
		if status.Kind == bookingapi.OperationCreate {
			httpStatus = http.StatusCreated
		}
	case bookingapi.StateRejectedNoEffect, bookingapi.StateNotDispatched:
		httpStatus = http.StatusUnprocessableEntity
	case bookingapi.StateOutcomeUnknown:
		httpStatus = http.StatusConflict
	}
	writeJSON(w, httpStatus, status)
}

func (h *Handler) writeSubmissionError(w http.ResponseWriter, operationID bookingapi.OperationID, err error) {
	var conflict *storage.ConflictError
	if errors.As(err, &conflict) {
		responseID, statusURL := string(operationID), ""
		if parsed, parseErr := bookingapi.ParseOperationID(conflict.ConflictingOperationID); parseErr == nil {
			responseID = string(parsed)
			statusURL = bookingservice.OperationStatusURL(parsed)
		}
		h.writeContractError(w, http.StatusConflict, conflict.Code, "booking operation conflicts with durable state", responseID, "", statusURL)
		return
	}
	var uncertain *bookingservice.AdmissionUncertainError
	if errors.As(err, &uncertain) {
		h.writeContractError(w, http.StatusConflict, bookingapi.ErrorOperationOutcomeUnknown, "operation acceptance is uncertain; inspect this operation key", string(operationID), "", bookingservice.OperationStatusURL(operationID))
		return
	}
	if errors.Is(err, bookingservice.ErrCoordinatorClosed) || errors.Is(err, bookingservice.ErrExecutionCapacity) || errors.Is(err, bookingservice.ErrSessionCapacity) {
		h.writeContractError(w, http.StatusServiceUnavailable, bookingapi.ErrorDependencyUnavailable, "booking service unavailable before admission", "", "", "")
		return
	}
	h.writeContractError(w, http.StatusServiceUnavailable, bookingapi.ErrorDependencyUnavailable, "booking dependency unavailable", "", "", "")
}

func (h *Handler) requireReliableAPI(w http.ResponseWriter) bool {
	if h.reliableEnabled {
		return true
	}
	h.writeContractError(w, http.StatusServiceUnavailable, bookingapi.ErrorDependencyUnavailable, "reliable booking API is not initialized", "", "", "")
	return false
}

func (h *Handler) operationKey(w http.ResponseWriter, r *http.Request) (bookingapi.OperationID, bool) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		h.writeContractError(w, http.StatusBadRequest, bookingapi.ErrorInvalidRequest, "exactly one Idempotency-Key header is required", "", "", "")
		return "", false
	}
	operationID, err := bookingapi.ParseOperationID(values[0])
	if err != nil {
		h.writeContractError(w, http.StatusBadRequest, bookingapi.ErrorInvalidRequest, "Idempotency-Key must be a canonical UUID", "", "", "")
		return "", false
	}
	return operationID, true
}

func (h *Handler) writeContractError(w http.ResponseWriter, status int, code bookingapi.ErrorCode, message, operationID, phase, statusURL string) {
	writeJSON(w, status, bookingapi.ErrorResponse{Code: code, Message: message, OperationID: operationID, Phase: phase, StatusURL: statusURL})
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeJSON(w, http.StatusBadRequest, bookingapi.ErrorResponse{Code: bookingapi.ErrorInvalidRequest, Message: "invalid JSON request"})
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, bookingapi.ErrorResponse{Code: bookingapi.ErrorInvalidRequest, Message: "request must contain one JSON value"})
		return false
	}
	return true
}

// listMatches returns court reservations for the given date from the
// Eversports /api/slot endpoint. Each item in the response is a time slot on a
// specific court; slots where booking != null are already reserved.
// Requires ?date=YYYY-MM-DD.
// Optional ?startTime=HHMM and ?endTime=HHMM restrict to a time window (inclusive).
// Optional ?my=true|false filters by whether the authenticated user owns the reservation.
// EVERSPORTS_FACILITY_ID, EVERSPORTS_FACILITY_SLUG, EVERSPORTS_SPORT_ID, and
// EVERSPORTS_SPORT_UUID must be configured (same requirements as /courts).
func (h *Handler) listMatches(w http.ResponseWriter, r *http.Request) {
	if h.facilityID == "" || h.facilitySlug == "" {
		writeError(w, http.StatusInternalServerError, "games endpoint requires EVERSPORTS_FACILITY_ID and EVERSPORTS_FACILITY_SLUG to be configured")
		return
	}

	q := r.URL.Query()

	date := q.Get("date")
	if date == "" {
		writeError(w, http.StatusBadRequest, "date query parameter is required (format: YYYY-MM-DD)")
		return
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		writeError(w, http.StatusBadRequest, "invalid date format — expected YYYY-MM-DD")
		return
	}

	var startTime, endTime string
	if s := q.Get("startTime"); s != "" {
		if _, err := parseHHMM(s); err != nil {
			writeError(w, http.StatusBadRequest, "invalid startTime: "+err.Error())
			return
		}
		startTime = s
	}
	if s := q.Get("endTime"); s != "" {
		if _, err := parseHHMM(s); err != nil {
			writeError(w, http.StatusBadRequest, "invalid endTime: "+err.Error())
			return
		}
		endTime = s
	}

	if startTime != "" && endTime != "" && startTime > endTime {
		writeError(w, http.StatusBadRequest, "startTime must not be later than endTime")
		return
	}

	var myFilter *bool
	if s := q.Get("my"); s != "" {
		switch s {
		case "true":
			v := true
			myFilter = &v
		case "false":
			v := false
			myFilter = &v
		default:
			writeError(w, http.StatusBadRequest, `invalid "my" parameter — expected "true" or "false"`)
			return
		}
	}

	es := h.clientFromRequest(r)
	if es == nil {
		writeError(w, http.StatusBadRequest, "X-Eversports-Email and X-Eversports-Password headers are required")
		return
	}

	courts, err := es.GetCourts(r.Context(), h.facilityID, h.facilitySlug, squashSportID, squashSportSlug, squashSportName, squashSportUUID, date)
	if err != nil {
		h.logger.Error("eversports get courts for games failed", "err", err)
		writeError(w, http.StatusBadGateway, "get courts failed: "+err.Error())
		return
	}
	courtIDs := make([]string, len(courts))
	courtUUIDByID := make(map[string]string, len(courts))
	for i, c := range courts {
		courtIDs[i] = c.ID
		courtUUIDByID[c.ID] = c.UUID
	}

	slots, err := es.GetSlots(r.Context(), h.facilityID, courtIDs, date)
	if err != nil {
		h.logger.Error("eversports get court bookings failed", "date", date, "err", err)
		writeError(w, http.StatusBadGateway, "get court bookings failed: "+err.Error())
		return
	}

	slots = filterSlots(slots, date, startTime, endTime, myFilter)

	// Enrich each slot with the court UUID so callers can book without a separate /courts call.
	type slotWithCourtUUID struct {
		eversports.Slot
		CourtUUID string `json:"courtUuid,omitempty"`
	}
	enriched := make([]slotWithCourtUUID, len(slots))
	for i, s := range slots {
		courtKey := strconv.Itoa(s.Court)
		enriched[i] = slotWithCourtUUID{
			Slot:      s,
			CourtUUID: courtUUIDByID[courtKey],
		}
	}

	writeJSON(w, http.StatusOK, enriched)
}

// parseHHMM validates and returns a 4-digit HHMM time string (e.g. "1830").
func parseHHMM(s string) (string, error) {
	if len(s) != 4 {
		return "", fmt.Errorf("expected 4 digits (HHMM), got %q", s)
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return "", fmt.Errorf("expected 4 digits (HHMM), got %q", s)
		}
	}
	h, _ := strconv.Atoi(s[:2])
	m, _ := strconv.Atoi(s[2:])
	if h > 23 || m > 59 {
		return "", fmt.Errorf("time out of range: %q", s)
	}
	return s, nil
}

// filterSlots applies all active filters to the slot list.
// date is always matched. Empty startTime/endTime means no time bound.
// nil myFilter means no ownership filter.
// Time comparison is lexicographic — valid because Start is zero-padded HHMM.
func filterSlots(slots []eversports.Slot, date, startTime, endTime string, myFilter *bool) []eversports.Slot {
	out := slots[:0]
	for _, s := range slots {
		if s.Date != date {
			continue
		}
		if startTime != "" && s.Start < startTime {
			continue
		}
		if endTime != "" && s.Start > endTime {
			continue
		}
		if myFilter != nil && s.IsUserBookingOwner != *myFilter {
			continue
		}
		out = append(out, s)
	}
	return out
}

// getCourts returns the list of courts at the configured facility by
// parsing the Eversports booking calendar HTML.
// Requires EVERSPORTS_FACILITY_ID and EVERSPORTS_FACILITY_SLUG to be configured.
// Optional ?date=YYYY-MM-DD; defaults to today.
func (h *Handler) getCourts(w http.ResponseWriter, r *http.Request) {
	if h.facilityID == "" || h.facilitySlug == "" {
		writeError(w, http.StatusInternalServerError, "courts endpoint requires EVERSPORTS_FACILITY_ID and EVERSPORTS_FACILITY_SLUG to be configured")
		return
	}
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	} else if _, err := time.Parse("2006-01-02", date); err != nil {
		writeError(w, http.StatusBadRequest, "invalid date format — expected YYYY-MM-DD")
		return
	}

	es := h.clientFromRequest(r)
	if es == nil {
		writeError(w, http.StatusBadRequest, "X-Eversports-Email and X-Eversports-Password headers are required")
		return
	}

	courts, err := es.GetCourts(r.Context(), h.facilityID, h.facilitySlug, squashSportID, squashSportSlug, squashSportName, squashSportUUID, date)
	if err != nil {
		h.logger.Error("eversports get courts failed", "err", err)
		writeError(w, http.StatusBadGateway, "get courts failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, courts)
}

// getFacility returns the venue profile for a given facility slug.
// Requires ?slug= query parameter.
func (h *Handler) getFacility(w http.ResponseWriter, r *http.Request) {
	slug := r.URL.Query().Get("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "slug query parameter is required")
		return
	}
	es := h.clientFromRequest(r)
	if es == nil {
		writeError(w, http.StatusBadRequest, "X-Eversports-Email and X-Eversports-Password headers are required")
		return
	}
	facility, err := es.GetFacility(r.Context(), slug)
	if err != nil {
		if errors.Is(err, eversports.ErrNotFound) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("facility %q not found", slug))
			return
		}
		h.logger.Error("eversports get facility failed", "slug", slug, "err", err)
		writeError(w, http.StatusBadGateway, "get facility failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, facility)
}
