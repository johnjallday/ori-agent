package personalassistanthttp

import (
	"errors"
	"mime"
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// recoveryResolveRequest names only the fix the user chose and the evidence
// they reviewed. The fix's target comes from the server's own diagnosis.
type recoveryResolveRequest struct {
	FixID          string `json:"fix_id"`
	EvidenceDigest string `json:"evidence_digest"`
}

func (h *Handler) recoveryResolver() personalassistant.RelationshipRecoveryResolver {
	if h == nil || h.recovery == nil {
		return nil
	}
	resolver, _ := h.recovery.(personalassistant.RelationshipRecoveryResolver)
	return resolver
}

// GetRepairDiagnosis explains why the assistant cannot be reconnected as it
// stands, and which fixes are safe. It never writes.
func (h *Handler) GetRepairDiagnosis(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	resolver := h.recoveryResolver()
	if resolver == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "personal assistant recovery is unavailable")
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	diagnosis, err := resolver.Diagnose(r.Context(), userID)
	if err != nil {
		writeResolveError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"diagnosis": diagnosis})
}

// ResolveRepair applies one reviewed fix, then reconnects the assistant when
// the records agree. Otherwise it returns the next diagnosis.
func (h *Handler) ResolveRepair(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	resolver := h.recoveryResolver()
	if resolver == nil || h.service == nil || h.provider == nil {
		orihttp.ServiceUnavailable(w, "personal assistant recovery is unavailable")
		return
	}
	// JSON only: a page on another site cannot send it without a CORS preflight.
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		writeRecoveryError(w, http.StatusUnsupportedMediaType, "invalid_recovery_request",
			"The recovery request must be JSON.")
		return
	}
	var body recoveryResolveRequest
	if err := decodeBoundedRequest(w, r, &body); err != nil {
		writeRecoveryError(w, http.StatusBadRequest, "invalid_recovery_request",
			"The recovery request is invalid.")
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	result, err := resolver.Resolve(r.Context(), userID, body.FixID, body.EvidenceDigest)
	if err != nil {
		writeResolveError(w, err)
		return
	}
	reconnected := result.State != nil
	if reconnected && result.State.Status.HasOwnedProfile() && h.onHired != nil {
		h.onHired()
	}
	projection, err := h.service.Get(r.Context(), userID)
	if err != nil {
		orihttp.ServiceUnavailable(w, "the fix was saved; reload to see where things stand")
		return
	}
	orihttp.Success(w, map[string]any{
		"personal_assistant": projection,
		"applied":            result.Applied,
		"reconnected":        reconnected,
		"diagnosis":          result.Diagnosis,
	})
}

func writeResolveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, personalassistant.ErrValidation):
		writeRecoveryError(w, http.StatusBadRequest, "invalid_recovery_request",
			"That fix is not available for these records. Review them again.")
	case errors.Is(err, personalassistant.ErrNotFound):
		writeRecoveryError(w, http.StatusConflict, "recovery_not_available",
			"There are no assistant records left to fix.")
	case errors.Is(err, personalassistant.ErrConflict):
		writeRecoveryError(w, http.StatusConflict, "recovery_conflict",
			"The assistant records changed. Review them again before applying a fix.")
	default:
		orihttp.ServiceUnavailable(w, "personal assistant recovery is temporarily unavailable")
	}
}
