package personalassistanthttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// FolderDigestService is the "show me a folder" boundary. Every folder it
// looks at is chosen on the server: from a chip identifier or the native
// dialog. No handler here accepts a filesystem path (FR48).
type FolderDigestService interface {
	Current(ctx context.Context, userID string) (personalassistant.FolderDigestView, error)
	MarkFirstPromptShown(ctx context.Context, userID string) (personalassistant.FolderDigestView, error)
	ScanChip(ctx context.Context, userID, chip string) (personalassistant.FolderOfferView, error)
	ScanPicked(ctx context.Context, userID string) (*personalassistant.FolderOfferView, error)
	ScanPickedFile(ctx context.Context, userID string) (*personalassistant.FolderOfferView, error)
	Decide(ctx context.Context, userID, offerID string, input personalassistant.FolderDecisionInput) (personalassistant.FolderOfferView, error)
	Resolve(ctx context.Context, userID, offerID string, input personalassistant.FolderResolveInput) (personalassistant.FolderOfferView, error)
	ResolveJourney(ctx context.Context, userID, offerID string, input personalassistant.FolderJourneyInput) (personalassistant.FolderOfferView, error)
	ResolvePortfolio(ctx context.Context, userID, offerID string, input personalassistant.FolderResolveInput) (personalassistant.FolderOfferView, error)
	PortfolioProvider(ctx context.Context, userID, offerID string) (string, error)
	ProjectSelectionPath(ctx context.Context, userID, offerID string) (string, error)
}

// FolderProjectSelectionIssuer mints the existing project-connection picker's
// opaque token after the service has rechecked the confirmed folder offer.
type FolderProjectSelectionIssuer interface {
	Issue(path string) (string, error)
}

func (h *Handler) SetFolderProjectSelections(issuer FolderProjectSelectionIssuer) {
	if h != nil {
		h.folderProjectSelections = issuer
	}
}

// FolderHomeProviderPreview discloses a host-reviewed Home provider release
// before an install; no source URL or plugin identity comes from the browser.
type FolderHomeProviderPreview struct {
	Ready      bool   `json:"ready"`
	Installed  bool   `json:"installed"`
	PluginID   string `json:"plugin_id"`
	Version    string `json:"version,omitempty"`
	Source     string `json:"source,omitempty"`
	Disclosure any    `json:"disclosure,omitempty"`
}

type FolderHomeProviderSetup interface {
	Preview(ctx context.Context, providerKey string) (FolderHomeProviderPreview, error)
	Install(ctx context.Context, providerKey, reviewedVersion string) (FolderHomeProviderPreview, error)
}

func (h *Handler) SetFolderHomeProvider(setup FolderHomeProviderSetup) {
	if h != nil {
		h.folderHomeProvider = setup
	}
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

// PromptedFolderDigest consumes only the first-folder hand-over. It accepts no
// path or request body; a replay is idempotent and cannot scan a folder.
func (h *Handler) PromptedFolderDigest(w http.ResponseWriter, r *http.Request) {
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
			orihttp.BadRequest(w, "The folder prompt does not accept request data")
			return
		}
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	view, err := h.folderDigest.MarkFirstPromptShown(r.Context(), userID)
	if err != nil {
		writeFolderDigestError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"folder_digest": view})
}

type folderScanRequest struct {
	Chip   string `json:"chip"`
	Picker bool   `json:"picker"`
	File   bool   `json:"file"`
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
	if !decodeFolderDigestBody(w, r, &req, "chip", "picker", "file") {
		return
	}
	req.Chip = strings.TrimSpace(req.Chip)
	options := 0
	if req.Chip != "" {
		options++
	}
	if req.Picker {
		options++
	}
	if req.File {
		options++
	}
	if options != 1 {
		orihttp.BadRequest(w, "Choose one folder, file, or picker mode")
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
	if req.File {
		offer, err := h.folderDigest.ScanPickedFile(r.Context(), userID)
		if err != nil {
			writeFolderDigestError(w, err)
			return
		}
		if offer == nil {
			orihttp.Success(w, map[string]any{"cancelled": true})
		} else {
			orihttp.Success(w, map[string]any{"offer": offer})
		}
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
	// Create, with a project yes, has the assistant set the workspace up
	// itself (the card's confirmed plan) instead of the Create Workspace
	// modal reporting one.
	Create bool `json:"create"`
}

// DecideFolderDigest records yes, no, or later for one offer. A project yes
// with create also sets the workspace up and resolves the offer.
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
	if !decodeFolderDigestBody(w, r, &req, "decision", "choice", "request_id", "create") {
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	offer, err := h.folderDigest.Decide(r.Context(), userID, offerID, personalassistant.FolderDecisionInput{
		Decision: req.Decision, Choice: req.Choice, RequestID: req.RequestID, Create: req.Create,
	})
	if err != nil {
		writeFolderDigestError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"offer": offer})
}

// FolderProjectSelection passes the server-retained folder to the reviewed
// quest's existing project connection flow. No path is accepted from the
// browser; a lost path requires an explicit new folder pick.
func (h *Handler) FolderProjectSelection(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.folderDigest == nil || h.folderProjectSelections == nil {
		orihttp.ServiceUnavailable(w, "Project selection is unavailable")
		return
	}
	var body struct{}
	if !decodeFolderDigestBody(w, r, &body) {
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	path, err := h.folderDigest.ProjectSelectionPath(r.Context(), userID, strings.TrimSpace(r.PathValue("offerID")))
	if err != nil {
		writeFolderDigestError(w, err)
		return
	}
	token, err := h.folderProjectSelections.Issue(path)
	if err != nil {
		writeFolderDigestError(w, personalassistant.ErrFolderPathLost)
		return
	}
	orihttp.Success(w, map[string]any{"selection_token": token, "folder": filepath.Base(path)})
}

type folderHomeProviderRequest struct {
	Confirm         bool   `json:"confirm"`
	ReviewedVersion string `json:"reviewed_version"`
}

// SetupFolderHomeProvider is a consequence of a confirmed portfolio card.
// The host chooses and re-verifies the reviewed provider on both calls.
func (h *Handler) SetupFolderHomeProvider(w http.ResponseWriter, r *http.Request) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	if h == nil || h.folderDigest == nil || h.folderHomeProvider == nil {
		orihttp.ServiceUnavailable(w, "Home setup is unavailable")
		return
	}
	var req folderHomeProviderRequest
	if !decodeFolderDigestBody(w, r, &req, "confirm", "reviewed_version") {
		return
	}
	if !req.Confirm && req.ReviewedVersion != "" {
		orihttp.BadRequest(w, "Review the provider before confirming")
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	key, err := h.folderDigest.PortfolioProvider(r.Context(), userID, strings.TrimSpace(r.PathValue("offerID")))
	if err != nil {
		writeFolderDigestError(w, err)
		return
	}
	var preview FolderHomeProviderPreview
	if req.Confirm {
		preview, err = h.folderHomeProvider.Install(r.Context(), key, strings.TrimSpace(req.ReviewedVersion))
	} else {
		preview, err = h.folderHomeProvider.Preview(r.Context(), key)
	}
	if err != nil {
		writeFolderDigestError(w, err)
		return
	}
	orihttp.Success(w, map[string]any{"home_provider": preview})
}

type folderResolveRequest struct {
	WorkspaceID string `json:"workspace_id"`
	HomeID      string `json:"home_id"`
	RunID       string `json:"run_id"`
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
	if !decodeFolderDigestBody(w, r, &req, "workspace_id", "home_id", "run_id", "request_id") {
		return
	}
	proofs := 0
	for _, value := range []string{req.WorkspaceID, req.HomeID, req.RunID} {
		if strings.TrimSpace(value) != "" {
			proofs++
		}
	}
	if proofs != 1 {
		orihttp.BadRequest(w, "Provide one workspace, Home, or journey run")
		return
	}
	userID, ok := h.currentUserID(w, r)
	if !ok {
		return
	}
	var offer personalassistant.FolderOfferView
	var err error
	if req.RunID != "" {
		offer, err = h.folderDigest.ResolveJourney(r.Context(), userID, offerID, personalassistant.FolderJourneyInput{
			RunID: req.RunID, RequestID: req.RequestID,
		})
	} else if req.HomeID != "" {
		offer, err = h.folderDigest.ResolvePortfolio(r.Context(), userID, offerID, personalassistant.FolderResolveInput{HomeID: req.HomeID, RequestID: req.RequestID})
	} else {
		offer, err = h.folderDigest.Resolve(r.Context(), userID, offerID, personalassistant.FolderResolveInput{
			WorkspaceID: req.WorkspaceID, RequestID: req.RequestID,
		})
	}
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
	case errors.Is(err, personalassistant.ErrFolderTidyFailed):
		orihttp.ServiceUnavailable(w, "File Janitor could not be set up for that folder right now. Try again in a moment")
	case errors.Is(err, personalassistant.ErrFolderCreateFailed):
		orihttp.ServiceUnavailable(w, "The workspace could not be created right now. Adjust… creates it through the usual dialog")
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
