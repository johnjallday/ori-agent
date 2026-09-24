package personalassistanthttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// FolderDigestService is the "show me a folder" boundary. Every folder it
// looks at is chosen on the server: from a chip identifier or the native
// dialog. No handler here accepts a filesystem path (FR48).
type FolderDigestService interface {
	Current(ctx context.Context, userID string) (personalassistant.FolderDigestView, error)
	ScanChip(ctx context.Context, userID, chip string) (personalassistant.FolderOfferView, error)
	ScanPicked(ctx context.Context, userID string) (*personalassistant.FolderOfferView, error)
	Decide(ctx context.Context, userID, offerID string, input personalassistant.FolderDecisionInput) (personalassistant.FolderOfferView, error)
	Resolve(ctx context.Context, userID, offerID string, input personalassistant.FolderResolveInput) (personalassistant.FolderOfferView, error)
}

// SetFolderDigest adds the folder-digest boundary.
func (h *Handler) SetFolderDigest(service FolderDigestService) {
	if h != nil {
		h.folderDigest = service
	}
}

// maxFolderDigestBodyBytes bounds every folder-digest request body; the
// largest legitimate body is a decision with a request id.
const maxFolderDigestBodyBytes = 4 * 1024

// GetFolderDigest returns the pending offer and the chooser's chips.
func (h *Handler) GetFolderDigest(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodGet) {
		return
	}
	if h == nil || h.folderDigest == nil {
		orihttp.ServiceUnavailable(w, "Show me a folder is unavailable")
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	view, err := h.folderDigest.Current(r.Context(), userID)
	if err != nil {
		writeFolderDigestError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"folder_digest": view})
}

type folderScanRequest struct {
	Chip   string `json:"chip"`
	Picker bool   `json:"picker"`
}

// ScanFolderDigest scans a known folder ({"chip": "downloads"}) or opens the
// native dialog ({"picker": true}). Any other field, a path above all, is
// refused before anything is looked at.
func (h *Handler) ScanFolderDigest(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.folderDigest == nil {
		orihttp.ServiceUnavailable(w, "Show me a folder is unavailable")
		return
	}
	var req folderScanRequest
	if !decodeFolderDigestBody(w, r, &req, "chip", "picker") {
		return
	}
	req.Chip = strings.TrimSpace(req.Chip)
	if (req.Chip == "") == !req.Picker {
		orihttp.BadRequest(w, "Choose one folder from the list, or the picker")
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	if req.Picker {
		h.scanPickedFolder(w, r, userID)
		return
	}
	offer, err := h.folderDigest.ScanChip(r.Context(), userID, req.Chip)
	if err != nil {
		writeFolderDigestError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"offer": offer})
}

// PickFolderDigest opens the native dialog with no request body at all.
func (h *Handler) PickFolderDigest(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.folderDigest == nil {
		orihttp.ServiceUnavailable(w, "Show me a folder is unavailable")
		return
	}
	if r.Body != nil {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32))
		if err != nil || strings.TrimSpace(string(body)) != "" {
			orihttp.BadRequest(w, "The picker does not accept request data")
			return
		}
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	h.scanPickedFolder(w, r, userID)
}

func (h *Handler) scanPickedFolder(w http.ResponseWriter, r *http.Request, userID string) {
	offer, err := h.folderDigest.ScanPicked(r.Context(), userID)
	if err != nil {
		writeFolderDigestError(w, err)
		return
	}
	if offer == nil {
		orihttp.Success(w, map[string]any{"cancelled": true})
		return
	}
	orihttp.Success(w, map[string]any{"offer": offer})
}

type folderDecideRequest struct {
	Decision  string `json:"decision"`
	Choice    string `json:"choice"`
	RequestID string `json:"request_id"`
}

// DecideFolderDigest records yes, no, or later for one offer.
func (h *Handler) DecideFolderDigest(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.folderDigest == nil {
		orihttp.ServiceUnavailable(w, "Show me a folder is unavailable")
		return
	}
	offerID := strings.TrimSpace(r.PathValue("offerID"))
	if offerID == "" {
		orihttp.BadRequest(w, "Offer id is required")
		return
	}
	var req folderDecideRequest
	if !decodeFolderDigestBody(w, r, &req, "decision", "choice", "request_id") {
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	offer, err := h.folderDigest.Decide(r.Context(), userID, offerID, personalassistant.FolderDecisionInput{
		Decision: req.Decision, Choice: req.Choice, RequestID: req.RequestID,
	})
	if err != nil {
		writeFolderDigestError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"offer": offer})
}

type folderResolveRequest struct {
	WorkspaceID string `json:"workspace_id"`
	RequestID   string `json:"request_id"`
}

// ResolveFolderDigest reports the workspace the Create Workspace modal made
// for a project offer; the server attaches the offer's folder to it. Only a
// workspace id and a request id are accepted — the folder comes from the
// offer the server already holds.
func (h *Handler) ResolveFolderDigest(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.folderDigest == nil {
		orihttp.ServiceUnavailable(w, "Show me a folder is unavailable")
		return
	}
	offerID := strings.TrimSpace(r.PathValue("offerID"))
	if offerID == "" {
		orihttp.BadRequest(w, "Offer id is required")
		return
	}
	var req folderResolveRequest
	if !decodeFolderDigestBody(w, r, &req, "workspace_id", "request_id") {
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	offer, err := h.folderDigest.Resolve(r.Context(), userID, offerID, personalassistant.FolderResolveInput{
		WorkspaceID: req.WorkspaceID, RequestID: req.RequestID,
	})
	if err != nil {
		writeFolderDigestError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"offer": offer})
}

// decodeFolderDigestBody reads one bounded JSON object and refuses any field
// outside allowed. The refusal is deliberate and named: a folder path from
// the browser is never accepted by this feature (FR48).
func decodeFolderDigestBody(w http.ResponseWriter, r *http.Request, target any, allowed ...string) bool {
	if r.Body == nil {
		orihttp.BadRequest(w, "A JSON body is required")
		return false
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxFolderDigestBodyBytes))
	if err != nil {
		orihttp.BadRequest(w, "Request body is too large")
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		orihttp.BadRequest(w, "Request must be one JSON object")
		return false
	}
	for key := range fields {
		known := false
		for _, name := range allowed {
			if key == name {
				known = true
				break
			}
		}
		if !known {
			if strings.Contains(strings.ToLower(key), "path") || strings.Contains(strings.ToLower(key), "folder") {
				orihttp.BadRequest(w, "This action does not accept a folder path; choose a folder from the list or the picker")
				return false
			}
			orihttp.BadRequest(w, "Unknown field: "+key)
			return false
		}
	}
	if err := json.Unmarshal(data, target); err != nil {
		orihttp.BadRequest(w, "Request fields have the wrong type")
		return false
	}
	return true
}

func writeFolderDigestError(w http.ResponseWriter, err error) {
	var rootErr *personalassistant.FolderRootError
	switch {
	case errors.As(err, &rootErr):
		_ = orihttp.RespondJSON(w, http.StatusBadRequest, map[string]any{"error": rootErr.Message, "repair": "choose_folder"})
	case errors.Is(err, personalassistant.ErrFolderChipUnknown):
		orihttp.BadRequest(w, "That is not one of the folders Ori can look at")
	case errors.Is(err, personalassistant.ErrFolderChipMissing):
		orihttp.BadRequest(w, "That folder is not on this computer")
	case errors.Is(err, personalassistant.ErrFolderOfferChoice):
		orihttp.BadRequest(w, "Say whether this is a project or a tidy")
	case errors.Is(err, personalassistant.ErrValidation):
		orihttp.BadRequest(w, "The decision is not valid")
	case errors.Is(err, personalassistant.ErrFolderOfferNotFound):
		orihttp.NotFound(w, "That offer is no longer here")
	case errors.Is(err, personalassistant.ErrFolderWorkspaceNotFound):
		orihttp.NotFound(w, "That workspace is no longer here")
	case errors.Is(err, personalassistant.ErrFolderWorkspaceRefused):
		orihttp.Conflict(w, "That workspace was not created for this offer, or already has a linked folder")
	case errors.Is(err, personalassistant.ErrFolderOutcomeUnavailable):
		orihttp.ServiceUnavailable(w, "The workspace could not be linked to the folder right now")
	case errors.Is(err, personalassistant.ErrFolderScanBusy):
		orihttp.Conflict(w, "Ori is still looking at a folder. Try again in a moment")
	case errors.Is(err, personalassistant.ErrFolderPickerUnavailable):
		orihttp.Conflict(w, "The folder dialog is unavailable here. Pick a folder from the list")
	case errors.Is(err, personalassistant.ErrFolderOfferDecided):
		orihttp.Conflict(w, "That offer was already answered")
	case errors.Is(err, personalassistant.ErrFolderPathLost):
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{"error": "Ori no longer has that folder open. Pick it again", "needs_pick": true})
	case errors.Is(err, personalassistant.ErrNeedsHQ):
		orihttp.Conflict(w, "Build Personal HQ before showing a folder")
	case errors.Is(err, personalassistant.ErrRepairNeeded), errors.Is(err, personalassistant.ErrConflict), errors.Is(err, personalassistant.ErrNotFound):
		orihttp.Conflict(w, "The assistant relationship changed. Refresh before showing a folder")
	default:
		orihttp.ServiceUnavailable(w, "Show me a folder is temporarily unavailable")
	}
}
