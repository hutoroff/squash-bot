package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/hutoroff/squash-bot/cmd/management/service"
	"github.com/hutoroff/squash-bot/cmd/management/storage"
	"github.com/hutoroff/squash-bot/internal/discovery"
)

const maxDiscoveryRequestBytes = 64 << 10

func (h *Handler) registerServiceInstance(w http.ResponseWriter, r *http.Request) {
	if !h.requireServiceRegistry(w) {
		return
	}
	var descriptor discovery.Descriptor
	if err := decodeBoundedDiscoveryJSON(w, r, &descriptor); err != nil {
		writeError(w, http.StatusBadRequest, "invalid discovery descriptor")
		return
	}
	if descriptor.InstanceID != r.PathValue("instance_id") {
		writeError(w, http.StatusBadRequest, "instance ID does not match descriptor")
		return
	}
	lease, err := h.serviceRegistry.Register(r.Context(), descriptor)
	if err != nil {
		writeRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lease)
}

func (h *Handler) heartbeatServiceInstance(w http.ResponseWriter, r *http.Request) {
	if !h.requireServiceRegistry(w) {
		return
	}
	var request struct {
		State discovery.InstanceState `json:"state"`
	}
	if err := decodeBoundedDiscoveryJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid heartbeat")
		return
	}
	lease, err := h.serviceRegistry.Heartbeat(r.Context(), r.PathValue("instance_id"), request.State)
	if err != nil {
		writeRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lease)
}

func (h *Handler) drainServiceInstance(w http.ResponseWriter, r *http.Request) {
	if !h.requireServiceRegistry(w) {
		return
	}
	if err := h.serviceRegistry.Drain(r.Context(), r.PathValue("instance_id")); err != nil {
		writeRegistryError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deregisterServiceInstance(w http.ResponseWriter, r *http.Request) {
	if !h.requireServiceRegistry(w) {
		return
	}
	if err := h.serviceRegistry.Deregister(r.Context(), r.PathValue("instance_id")); err != nil {
		writeRegistryError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) resolveServiceInstances(w http.ResponseWriter, r *http.Request) {
	if !h.requireServiceRegistry(w) {
		return
	}
	query := r.URL.Query()
	major, err := strconv.Atoi(query.Get("major"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid contract major")
		return
	}
	minor := 0
	if raw := query.Get("minimum_minor"); raw != "" {
		minor, err = strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid minimum minor")
			return
		}
	}
	requirement := discovery.Requirement{
		ServiceKey: query.Get("service_key"), Contract: query.Get("contract"), Major: major,
		MinimumMinor: minor, RequiredCapabilities: query["capability"], Scope: map[string]string{},
	}
	for key, values := range query {
		if !strings.HasPrefix(key, "scope.") {
			continue
		}
		if len(values) != 1 {
			writeError(w, http.StatusBadRequest, "invalid scope selector")
			return
		}
		requirement.Scope[strings.TrimPrefix(key, "scope.")] = values[0]
	}
	if err := discovery.ValidateRequirement(requirement); err != nil {
		writeError(w, http.StatusBadRequest, "invalid discovery selector")
		return
	}
	endpoints, err := h.serviceRegistry.Resolve(r.Context(), requirement)
	if err != nil {
		writeRegistryError(w, err)
		return
	}
	if endpoints == nil {
		endpoints = []discovery.ResolvedEndpoint{}
	}
	writeJSON(w, http.StatusOK, endpoints)
}

func (h *Handler) getServiceCatalog(w http.ResponseWriter, r *http.Request) {
	if !h.requireServiceRegistry(w) {
		return
	}
	catalog, err := h.serviceRegistry.Catalog(r.Context())
	if err != nil {
		writeRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, catalog)
}

func (h *Handler) requireServiceRegistry(w http.ResponseWriter) bool {
	if h.serviceRegistry == nil {
		writeError(w, http.StatusServiceUnavailable, "service registry unavailable")
		return false
	}
	return true
}

func decodeBoundedDiscoveryJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxDiscoveryRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

func writeRegistryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrRegistryDescriptorRejected):
		writeError(w, http.StatusBadRequest, "service registry descriptor rejected")
	case errors.Is(err, storage.ErrRegistryConflict):
		writeError(w, http.StatusConflict, "service registry identity conflict")
	case errors.Is(err, storage.ErrRegistryNotFound):
		writeError(w, http.StatusNotFound, "service registry instance not found")
	case errors.Is(err, storage.ErrRegistryGone):
		writeError(w, http.StatusGone, "service registry instance is expired or terminal")
	case errors.Is(err, storage.ErrRegistryCapacity):
		writeError(w, http.StatusTooManyRequests, "service registry capacity exhausted")
	default:
		writeError(w, http.StatusServiceUnavailable, "service registry unavailable")
	}
}
