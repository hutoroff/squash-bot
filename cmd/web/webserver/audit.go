package webserver

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// AuditHandler proxies GET /api/audit to the management service,
// injecting the caller's Telegram ID from the JWT session.
type AuditHandler struct {
	auth       *AuthHandler
	mgmtURL    string
	mgmtSecret string
	httpClient *http.Client
}

// NewAuditHandler creates an AuditHandler.
func NewAuditHandler(auth *AuthHandler, mgmtURL, mgmtSecret string) *AuditHandler {
	return &AuditHandler{
		auth:       auth,
		mgmtURL:    mgmtURL,
		mgmtSecret: mgmtSecret,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// handleListAuditEvents handles GET /api/audit.
// It requires an authenticated session; the caller's TelegramID is injected
// into X-Caller-Tg-Id so the management service can apply visibility rules.
// Supported query params: limit, before_id, event_type, from, to (passed through).
func (a *AuditHandler) handleListAuditEvents(w http.ResponseWriter, r *http.Request) {
	claims, err := a.auth.claimsFromRequest(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`)) //nolint:errcheck
		return
	}

	upstream := fmt.Sprintf("%s/api/v1/audit", a.mgmtURL)
	if raw := r.URL.RawQuery; raw != "" {
		upstream += "?" + raw
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstream, nil)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal error"}`)) //nolint:errcheck
		return
	}
	req.Header.Set("Authorization", "Bearer "+a.mgmtSecret)
	req.Header.Set("X-Caller-Tg-Id", strconv.FormatInt(claims.TelegramID, 10))

	resp, err := a.httpClient.Do(req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"upstream unavailable"}`)) //nolint:errcheck
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
}
