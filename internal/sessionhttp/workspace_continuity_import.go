package sessionhttp

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"sync"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// continuityImporter owns the confirmed modern Import Folder operation. It is
// a coordinator over existing domain stores, not a store of its own: receipts,
// install plans and outcomes live in the local continuity tables.
type continuityImporter struct {
	uploads *sessionfiles.Store
	// afterImport runs once a workspace is attached (e.g. to prepare its own
	// checkpoint here). It must not block.
	afterImport func()
	mu          sync.Mutex // one import operation at a time
}

// SetContinuityImport enables reviewed modern imports. uploads is the normal
// session-files store restored uploads are installed into; afterImport may
// be nil.
func (h *Handler) SetContinuityImport(uploads *sessionfiles.Store, afterImport func()) {
	h.continuity = &continuityImporter{uploads: uploads, afterImport: afterImport}
}

// SetContinuityAdmissionHook registers fn to run, without blocking, whenever a
// workspace's local admission changes here: once it is imported, and when its
// routines are turned on or off. Runtimes that cache per-workspace state at
// startup (trigger watches, webhook tokens) use it to catch up.
func (h *Handler) SetContinuityAdmissionHook(fn func(workspaceID string)) {
	h.continuityAdmissionChanged = fn
}

func (h *Handler) notifyContinuityAdmission(workspaceID string) {
	if h.continuityAdmissionChanged != nil {
		go h.continuityAdmissionChanged(workspaceID)
	}
}

type continuityImportRequest struct {
	Path              string `json:"path"`
	TreeDigest        string `json:"tree_digest"`
	DestinationDigest string `json:"destination_digest"`
	Action            string `json:"action"`
}

// continuityImportReport is the reopenable result of one operation. History
// and adoption are reported separately; missing history is never shown as a
// verified empty result.
type continuityImportReport struct {
	OperationID   string                   `json:"operation_id"`
	Status        string                   `json:"status"` // complete | interrupted | restoring
	Action        string                   `json:"action"`
	WorkspaceID   string                   `json:"workspace_id,omitempty"`
	WorkspaceSlug string                   `json:"workspace_slug,omitempty"`
	Adopted       bool                     `json:"adopted"`
	AssistantName string                   `json:"assistant_name,omitempty"`
	BackgroundOff bool                     `json:"background_off"`
	Members       []continuityMemberReport `json:"members"`
	Error         string                   `json:"error,omitempty"`
	Retryable     bool                     `json:"retryable,omitempty"`
}

type continuityMemberReport struct {
	WorkspaceID string                                 `json:"workspace_id"`
	Name        string                                 `json:"name,omitempty"`
	ParentID    string                                 `json:"parent_id,omitempty"`
	Disposition string                                 `json:"disposition"`
	State       string                                 `json:"state"`
	Components  []workspacecontinuity.ComponentOutcome `json:"components"`
}

func (h *Handler) handleContinuityImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = orihttp.RespondMethodNotAllowed(w)
		return
	}
	if h.continuity == nil || h.store == nil || h.store.DB() == nil || h.workspaceStore == nil {
		_ = orihttp.RespondJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false,
			"error": "Restoring a copied workspace is not available in this build."})
		return
	}
	var req continuityImportRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	report, status, err := h.runContinuityImport(r.Context(), req)
	h.respondContinuityImport(w, report, status, err)
}

// handleContinuityImportOperation serves GET {op} (reopen the report) and
// POST {op}/retry (resume an interrupted operation with the same receipt).
func (h *Handler) handleContinuityImportOperation(w http.ResponseWriter, r *http.Request, rest string) {
	if h.continuity == nil || h.store == nil || h.store.DB() == nil || h.workspaceStore == nil {
		_ = orihttp.RespondJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false,
			"error": "Restoring a copied workspace is not available in this build."})
		return
	}
	operationID, action, _ := strings.Cut(rest, "/")
	if !workspacecontinuity.ValidID(operationID) {
		_ = orihttp.RespondNotFound(w, "import not found")
		return
	}
	switch {
	case action == "" && r.Method == http.MethodGet:
		report, err := h.continuityImportReport(r.Context(), operationID)
		if err != nil {
			_ = orihttp.RespondNotFound(w, "import not found")
			return
		}
		orihttp.WriteJSON(w, map[string]any{"success": true, "import": report})
	case action == "retry" && r.Method == http.MethodPost:
		report, status, err := h.resumeContinuityImport(r.Context(), operationID)
		h.respondContinuityImport(w, report, status, err)
	default:
		_ = orihttp.RespondMethodNotAllowed(w)
	}
}

func (h *Handler) respondContinuityImport(w http.ResponseWriter, report *continuityImportReport, status int, err error) {
	if err != nil {
		message := continuityImportMessage(err)
		if report != nil {
			report.Error, report.Retryable = message, report.Status != "complete"
		}
		logger.Warn("Workspace continuity import did not finish", logger.Fields{"error": err.Error()})
		_ = orihttp.RespondJSON(w, status, map[string]any{"success": false, "error": message, "import": report})
		return
	}
	_ = orihttp.RespondJSON(w, status, map[string]any{"success": true, "import": report})
}

// errContinuityStale means the folder or this installation changed after
// the user reviewed it; a new review is required before anything is written.
var errContinuityStale = errors.New("continuity review is stale")

type continuityUserError struct{ message string }

func (e *continuityUserError) Error() string { return e.message }

func continuityImportMessage(err error) string {
	var user *continuityUserError
	switch {
	case errors.As(err, &user):
		return user.message
	case errors.Is(err, errContinuityStale), errors.Is(err, workspacecontinuity.ErrChanged):
		return "The folder or this Ori changed after you reviewed it. Review the folder again before importing."
	case errors.Is(err, workspacecontinuity.ErrConflict):
		return "Part of this folder conflicts with work already in Ori. Nothing existing was changed."
	case errors.Is(err, workspacecontinuity.ErrLimit):
		return "This folder is larger than a workspace copy can hold."
	default:
		return "The import stopped before it finished. Your existing work is unchanged; retry to continue from where it stopped."
	}
}

// analyzeContinuityDestination decides, in one read view of this
// installation, which actions the reviewed tree allows here. It never writes.
func analyzeContinuityDestination(ctx context.Context, q workspacecontinuity.Queryer, review *continuityImportReview) error {
	if review.Status != "review_required" || review.TreeDigest == "" {
		return nil
	}
	review.Actions, review.RecommendedAction, review.AdoptionBlocked, review.Conflict, review.AlreadyImported = nil, "", "", "", nil
	var operationID, status, action string
	err := q.QueryRowContext(ctx, `SELECT id,status,action FROM continuity_operations WHERE tree_digest=? AND owner_user_id=?`,
		review.TreeDigest, userprofile.LocalUserID).Scan(&operationID, &status, &action)
	switch {
	case err == nil && status == "complete":
		review.AlreadyImported = &continuityPriorImport{OperationID: operationID, WorkspaceID: review.WorkspaceID}
		_ = q.QueryRowContext(ctx, `SELECT COALESCE(folder_slug,'') FROM workspaces WHERE id=? AND deleted_at IS NULL`,
			review.WorkspaceID).Scan(&review.AlreadyImported.WorkspaceSlug)
		review.RecommendedAction = "open"
		review.ImportSupported = false
		return nil
	case err == nil:
		// The same reviewed tree was confirmed before and did not finish.
		review.Actions, review.RecommendedAction, review.ImportSupported = []string{action}, action, true
		review.AlreadyImported = &continuityPriorImport{OperationID: operationID}
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	for _, member := range review.members {
		var count int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces WHERE id=?`, member.Inspection.Manifest.WorkspaceID).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			review.Conflict, review.ImportSupported = "workspace_exists", false
			return nil
		}
	}
	var relationships int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM personal_assistant_state WHERE user_id=?`, userprofile.LocalUserID).Scan(&relationships); err != nil {
		return err
	}
	var designated sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT personal_workspace_id FROM users WHERE id=?`, userprofile.LocalUserID).Scan(&designated); err != nil {
		return err
	}
	review.ImportSupported = true
	switch candidates := len(review.AssistantCandidates); {
	case candidates == 0:
		review.Actions, review.RecommendedAction = []string{string(workspacecontinuity.WorkspaceOnly)}, string(workspacecontinuity.WorkspaceOnly)
	case candidates > 1:
		review.Actions, review.RecommendedAction = []string{string(workspacecontinuity.WorkspaceOnly)}, string(workspacecontinuity.WorkspaceOnly)
		review.AdoptionBlocked = "multiple_assistants"
	case relationships != 0 || strings.TrimSpace(designated.String) != "":
		review.Actions, review.RecommendedAction = []string{string(workspacecontinuity.WorkspaceOnly)}, string(workspacecontinuity.WorkspaceOnly)
		review.AdoptionBlocked = "existing_assistant"
	default:
		review.Actions = []string{string(workspacecontinuity.Continue), string(workspacecontinuity.WorkspaceOnly)}
		review.RecommendedAction = string(workspacecontinuity.Continue)
	}
	return nil
}
