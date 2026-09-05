package webserver

import (
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// FeatureFlagsHandler forwards authenticated identity; management checks live
// owner authority for reads and writes, including every group override.
type FeatureFlagsHandler struct{ mgmtClient }

func NewFeatureFlagsHandler(auth *AuthHandler, mgmtURL, secret string) *FeatureFlagsHandler {
	return &FeatureFlagsHandler{newMgmtClient(auth, mgmtURL, secret)}
}
func (h *Handler) SetFeatureFlags(flags *FeatureFlagsHandler) { h.featureFlags = flags }

func (f *FeatureFlagsHandler) handle(w http.ResponseWriter, r *http.Request) {
	claims, ok := f.claims(w, r)
	if !ok {
		return
	}
	query := url.Values{}
	if values, exists := r.URL.Query()["group_id"]; exists {
		if len(values) != 1 {
			writeAPIError(w, http.StatusBadRequest, "invalid group id")
			return
		}
		id, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || id == 0 {
			writeAPIError(w, http.StatusBadRequest, "invalid group id")
			return
		}
		query.Set("group_id", strconv.FormatInt(id, 10))
	}
	path := "/api/v1/feature-flags"
	if r.Method == http.MethodPatch {
		path += "/" + url.PathEscape(r.PathValue("key"))
	}
	if len(query) != 0 {
		path += "?" + query.Encode()
	}
	if r.Method == http.MethodPatch {
		body, ok := decodeWithActor(w, r, claims, nil)
		if !ok {
			return
		}
		f.proxy(w, r, http.MethodPatch, path, body)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, f.mgmtURL+path, nil)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}
	req.Header.Set("Authorization", "Bearer "+f.mgmtSecret)
	req.Header.Set("X-Caller-User-Id", strconv.FormatInt(claims.UserID, 10))
	resp, err := f.httpClient.Do(req)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "upstream unavailable")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
}
