package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// WorkspaceBuildPlacement uses the existing observation/source and build stores
// through a server adapter. The HTTP layer never receives a trusted root path.
type WorkspaceBuildPlacement interface {
	Handoff(context.Context, string, personalassistant.InboxPlacementInput, personalassistant.InboxPlacementSeed) (personalassistant.InboxPlacementResult, error)
	Acquire(context.Context, string, personalassistant.WorkspaceBuildSession) (func(), error)
}

func respondPlacementError(w http.ResponseWriter, err error) {
	code, message, status := "source_changed", "This source review is no longer current. Nothing new was created.", http.StatusConflict
	var details map[string]any
	var placement *personalassistant.InboxPlacementError
	if errors.As(err, &placement) {
		code = placement.Code
		switch code {
		case "build_conflict":
			message = "Another workspace draft is open or preparing. Finish or explicitly cancel it first; neither draft was overwritten."
			details = map[string]any{"build_id": placement.BuildID}
		case "selection_expired":
			message = "The selected folder is no longer held. Pick it again; nothing was created."
		case "handoff_incomplete":
			message = "The handoff could not be proven complete. Reopen this same proposal to reconcile it; do not create a replacement."
		}
	} else if errors.Is(err, personalassistant.ErrValidation) {
		code, message, status = "invalid_placement", "Use the current proposal and valid workspace name and goal.", http.StatusBadRequest
	} else if errors.Is(err, personalassistant.ErrFolderScanBusy) {
		code, message = "source_busy", "This conversation is changing. Wait for the current action before trying again."
	}
	respondBuildPreparationError(w, code, message, status, details)
}

func (h *Handler) handoffInboxBuild(w http.ResponseWriter, r *http.Request) {
	var input personalassistant.InboxPlacementInput
	if !decodeBuildBody(w, r, &input) {
		return
	}
	if err := input.Validate(); err != nil {
		respondPlacementError(w, err)
		return
	}
	owner, err := h.buildPreparationOwner(r.Context())
	if err != nil || h.workspaceBuild == nil || h.workspaceBuild.Placement == nil {
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	// Deliberately not workspaceBuildReadiness: this route has no inference.
	projection, err := h.workspaceBuild.Assistant(r.Context(), owner)
	if err != nil || projection == nil || (projection.State != personalassistant.APIStateActive && projection.State != personalassistant.APIStatePaused) {
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	result, err := h.workspaceBuild.Placement.Handoff(r.Context(), owner, input, h.seedInboxPlacementDraft)
	if err != nil {
		respondPlacementError(w, err)
		return
	}
	status := http.StatusCreated
	if result.Resumed {
		status = http.StatusOK
	}
	h.respondBuildSession(r.Context(), w, status, &result.Session, map[string]any{"resumed": result.Resumed})
}

func (h *Handler) seedInboxPlacementDraft(ctx context.Context, plan personalassistant.FolderSetupPlan, input personalassistant.InboxPlacementInput) (personalassistant.BuildDraft, error) {
	var draft personalassistant.BuildDraft
	inbox := plan.Intent.Inbox
	fresh, err := h.ReviewFileJanitorCreation(ctx)
	if err != nil || inbox == nil || len(fresh.Roles) != 1 || fresh.BlueprintID != inbox.BlueprintID || fresh.BlueprintVersion != inbox.BlueprintVersion || fresh.BlueprintDigest != inbox.BlueprintDigest || fresh.PlanRevision != inbox.PlanRevision {
		return draft, &personalassistant.InboxPlacementError{Code: "source_changed"}
	}
	role := fresh.Roles[0]
	if role.RoleID != inbox.Agent.RoleID || role.Name != inbox.Agent.Name || role.Action != inbox.Agent.Action || role.ConfigDigest != inbox.Agent.ConfigDigest {
		return draft, &personalassistant.InboxPlacementError{Code: "source_changed"}
	}
	name, brief := inbox.WorkspaceName, plan.BriefDefault
	if input.Name != nil {
		name = strings.TrimSpace(*input.Name)
	}
	if input.Brief != nil {
		brief = *input.Brief
	}
	brief, err = personalassistant.NormalizeFolderBrief(brief)
	if err != nil || name == "" || strings.ContainsAny(name, "/\\\x00\r\n") || h.workspaceNameTaken(ctx, name) {
		return draft, personalassistant.ErrValidation
	}
	if brief == "" {
		brief = plan.BriefDefault
	}
	mode := roleStaffingModeCreate
	if role.Action == "reuse" {
		mode = roleStaffingModeAssign
	}
	intent, err := json.Marshal(workspaceTeamIntent{Version: workspaceTeamIntentVersion, Mode: workspaceTeamModeStaffed, PlanRevision: fresh.PlanRevision})
	if err != nil {
		return draft, err
	}
	staffing, err := json.Marshal([]roleStaffingInput{{RoleID: role.RoleID, Mode: mode, Name: role.Name}})
	if err != nil {
		return draft, err
	}
	return personalassistant.BuildDraft{TemplateID: "file-janitor", Name: name, Mission: brief, Description: brief,
		TeamIntent: intent, RoleStaffing: staffing, ExistingAgentNames: []string{}}, nil
}

// The source lease is owned by the calling folder endpoint. Only an already
// bound placement draft can use the picker without generic model readiness.
func (h *Handler) workspaceBuildFolderReadiness(ctx context.Context, userID, id string) buildReadiness {
	deps := h.workspaceBuild
	if deps != nil && id != "" {
		doc, err := deps.Store.Read(ctx, userID)
		if err != nil {
			return buildReadiness{reason: buildReasonAssistantNotReady}
		}
		if build := doc.Session(id); build != nil && build.PlacementSource != nil {
			projection, err := deps.Assistant(ctx, userID)
			if err != nil || projection == nil || (projection.State != personalassistant.APIStateActive && projection.State != personalassistant.APIStatePaused) {
				return buildReadiness{reason: buildReasonAssistantNotReady}
			}
			target := build.PlacementSource.Target
			if target.UserID != userID || target.WorkspaceID != projection.HQWorkspaceID || target.AgentName != strings.ToLower(strings.TrimSpace(projection.GlobalAgentProfile)) {
				return buildReadiness{reason: buildReasonAssistantNotReady}
			}
			return buildReadiness{available: true, projection: projection}
		}
	}
	return h.workspaceBuildReadiness(ctx, userID)
}

func (h *Handler) validatePlacementFolder(ctx context.Context, target foldercontext.Target, id string, recognition *personalassistant.BuildFolderRecognition) error {
	if recognition == nil {
		return &personalassistant.FolderRootError{Message: "This folder could not be checked. Your current draft is unchanged; try the picker again."}
	}
	if recognition.Verdict == personalassistant.BuildFolderCollection || (recognition.Recognized() && recognition.Shape != "inbox") {
		return &personalassistant.FolderRootError{Message: "This folder describes a different kind of project. Choose an inbox-style folder; your current draft is unchanged."}
	}
	deps := h.workspaceBuild
	if deps.FileJanitor == nil {
		return errBuildFoldersOff
	}
	janitor, _ := deps.FileJanitor()
	if janitor == nil {
		return errBuildFoldersOff
	}
	root, err := deps.Folders.ResolveRoot(ctx, target, id)
	if err != nil {
		return err
	}
	_, err = janitor.ReviewSelectedSetup("", root, nil)
	return err
}

// acquirePlacementBuild fences every material endpoint while leaving ordinary
// builds on their established admission path. A source lease is held through
// claim acceptance, so detach/replace cannot race a stale confirmation.
func (h *Handler) acquirePlacementBuild(w http.ResponseWriter, r *http.Request, id, action string) (func(), bool) {
	noop := func() {}
	if h.workspaceBuild == nil {
		return noop, true
	}
	owner, err := h.buildPreparationOwner(r.Context())
	if err != nil {
		respondPlacementError(w, err)
		return nil, false
	}
	doc, err := h.workspaceBuild.Store.Read(r.Context(), owner)
	if err != nil {
		respondPlacementError(w, err)
		return nil, false
	}
	build := doc.Session(id)
	if build == nil || build.PlacementSource == nil {
		if action == "turns" {
			// Reservation/application recheck the durable acceptance fence under
			// the build-store lock. Do not hold a material-edit lease across
			// network inference: explicit edits must invalidate a late reply.
			if err := h.checkUnacceptedCreationBuild(r.Context(), owner, id); err != nil {
				var refusal errBuildTurn
				if errors.As(err, &refusal) {
					creationOperationError(w, refusal.status, refusal.code, "Read the original accepted operation; this build cannot start inference.")
				}
				return nil, false
			}
			return noop, true
		}
		return h.acquireCreationBuild(w, r, owner, id)
	}
	// Owner-scoped cancellation creates nothing and must remain reachable
	// after source detach/expiry. The abandon handler still refuses acceptance.
	if action == "abandon" {
		return noop, true
	}
	switch action {
	case "draft", "folder", "detach", "abandon", "pause", "janitor-review", "janitor-prepare", "janitor-defer":
	default:
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{"code": "placement_only", "error": "This proposal uses reviewed File Janitor setup, not inference or another setup engine."})
		return nil, false
	}
	if h.workspaceBuild.Placement == nil {
		respondPlacementError(w, errors.New("placement unavailable"))
		return nil, false
	}
	release, err := h.workspaceBuild.Placement.Acquire(r.Context(), owner, *build)
	if err != nil {
		respondPlacementError(w, err)
		return nil, false
	}
	return release, true
}
