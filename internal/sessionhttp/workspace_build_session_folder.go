package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// A build's attached folder: the pane's own Add folder. It is the chooser and
// the path-free observation the assistant's composer already uses (names,
// kinds, counts, markers; never contents), kept on the session so the pane
// shows it and the model reads it, and linked to the workspace at Create.
//
//	GET  /api/workspaces/build-sessions/folder-choices       what can be chosen
//	POST /api/workspaces/build-sessions/{id}/folder          attach one
//	POST /api/workspaces/build-sessions/{id}/folder/detach   remove it
//
// The browser never sends a path. It names a folder by a known-folder chip,
// by the picker (a dialog the server opens), or by the selection token the
// folder-picker endpoint issued for a typed path it checked. The server holds
// the folder's root for the observation it returns, for as long as the
// composer holds one (foldercontext.SelectionTTL); the session keeps only the
// observation, which can never revive that root.

// WorkspaceBuildFolders observes a folder for a build, says what the folder
// digest recognized in it, and resolves it again at Create
// (personalassistant.FolderObservationService in production).
type WorkspaceBuildFolders interface {
	Choices(ctx context.Context, userID string) (personalassistant.FolderDigestView, error)
	Observe(ctx context.Context, target foldercontext.Target, mode, choice string) (*foldercontext.Observation, error)
	ResolveRoot(ctx context.Context, target foldercontext.Target, id string) (string, error)
	Recognize(ctx context.Context, target foldercontext.Target, id string) (*personalassistant.BuildFolderRecognition, error)
	// Plan reads the setup plan Home's card would show for the recognized
	// project, named as the draft names the workspace. It creates nothing.
	Plan(ctx context.Context, target foldercontext.Target, observationID, candidateID, workspaceName string) (*personalassistant.BuildFolderPlan, error)
	// ReviewSetup makes or reuses the canonical offer Set it up runs on, for
	// the build's own review (draft is the build's selection target);
	// StartSetup runs it on the confirmed digest; ReadSetup projects the offer
	// and its run for the build that owns it.
	ReviewSetup(ctx context.Context, draft foldercontext.Target, sessionID, observationID, candidateID, workspaceName string) (personalassistant.FolderOfferView, error)
	StartSetup(ctx context.Context, userID, offerID, planDigest, requestID, entryName, brief string) (personalassistant.FolderOfferView, error)
	ReadSetup(ctx context.Context, review foldercontext.Target, offerID string) (*personalassistant.FolderOfferView, error)
}

// Fixed copy the host writes into the transcript for the folder. Both are
// form lines: the user's own act, said in plain words, which the model hears
// on its next turn the way it hears a renamed field.
const (
	buildCopyFolderAttached = "You attached the folder “%s”. It will be linked to the workspace when you create it."
	buildCopyFolderRemoved  = "You removed the attached folder."
	// buildCopyFolderNeeds is a host line: what a recognized folder's setup
	// needs, from the plan, said once per set of states.
	buildCopyFolderNeeds = "Needs: %s"
)

// buildNeedsLine is the Needs line for a plan: each plugin and where it
// stands. "" when the plan needs no plugin at all.
func buildNeedsLine(plan *personalassistant.BuildFolderPlan) string {
	if plan == nil || len(plan.Needs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(plan.Needs))
	for _, need := range plan.Needs {
		word := need.State
		if word == personalassistant.FolderInstallReady {
			word = "installed"
		}
		parts = append(parts, need.Name+" ("+word+")")
	}
	return fmt.Sprintf(buildCopyFolderNeeds, strings.Join(parts, " · "))
}

// noteBuildNeeds writes the Needs line for plan into the transcript as a host
// line, when it differs from the one last said. The plan must be for the
// session's current folder.
func noteBuildNeeds(session *personalassistant.WorkspaceBuildSession, plan *personalassistant.BuildFolderPlan, now time.Time) {
	if session.Folder == nil || !session.Folder.Recognition.Recognized() {
		return
	}
	line := buildNeedsLine(plan)
	if line == "" || line == session.Folder.Recognition.NeedsSaid {
		return
	}
	session.Folder.Recognition.NeedsSaid = line
	session.Append(personalassistant.BuildTranscriptEntry{Role: personalassistant.BuildRoleForm, Text: line, Host: true, At: now})
}

// workspaceBuildPlan reads the setup plan for a build's recognized folder: a
// single project with a reviewed capability, while the build is open or
// preparing and the server still holds the pick. Anything else has none.
// projection may be nil; it is then read.
func (h *Handler) workspaceBuildPlan(ctx context.Context, userID string, projection *personalassistant.Projection, session *personalassistant.WorkspaceBuildSession) *personalassistant.BuildFolderPlan {
	plan, err := h.readBuildPlan(ctx, userID, projection, session)
	if err != nil {
		logger.Info("Workspace build plan not read", logger.Fields{"session_id": session.ID, "error": err})
		return nil
	}
	return plan
}

// readBuildPlan is workspaceBuildPlan with the reason a recognized folder's
// plan could not be read: the server no longer holds the pick, most often.
// (nil, nil) is a build that has no plan to read at all.
func (h *Handler) readBuildPlan(ctx context.Context, userID string, projection *personalassistant.Projection, session *personalassistant.WorkspaceBuildSession) (*personalassistant.BuildFolderPlan, error) {
	deps := h.workspaceBuild
	if deps == nil || deps.Folders == nil || session == nil || session.Folder == nil {
		return nil, nil
	}
	recognition := session.Folder.Recognition
	if !recognition.Recognized() || recognition.IntegrationKey == "" ||
		(session.Status != personalassistant.WorkspaceBuildOpen && session.Status != personalassistant.WorkspaceBuildPreparing) {
		return nil, nil
	}
	if projection == nil {
		read, err := deps.Assistant(ctx, userID)
		if err != nil || read == nil {
			return nil, nil
		}
		projection = read
	}
	target := buildFolderTarget(userID, projection, session.ID)
	if recognition.OfferID != "" {
		// Set it up moved the held pick to the build's own review.
		target = personalassistant.BuildReviewTarget(target, session.ID)
	}
	return deps.Folders.Plan(ctx, target, session.Folder.Observation.ID, recognition.CandidateID, strings.TrimSpace(session.Draft.Name))
}

// buildReasonFolderAttached refuses the model's folder question once a folder
// is attached: there is nothing left to ask.
const buildReasonFolderAttached = "a folder is already attached; it is linked at Create"

// errBuildFoldersOff is the folder seam being unwired for this server.
var errBuildFoldersOff = errors.New("workspace build: folders are not available")

type buildFolderRequest struct {
	// RequestID identifies this tab's pending picker for scoped cancellation.
	// It is not authority; version/owner and the held selection remain required.
	RequestID string `json:"request_id,omitempty"`
	Mode      string `json:"mode"`
	Chip      string `json:"chip,omitempty"`
	// SelectionToken goes with mode "selection": the receipt the folder-picker
	// endpoint issued for a typed path it checked. Never a path.
	SelectionToken string `json:"selection_token,omitempty"`
	Version        int64  `json:"version"`
}

type buildFolderDetachRequest struct {
	Version int64 `json:"version"`
}

// buildFolderTarget scopes a build's selections the way the composer scopes a
// draft conversation's: to this user, the assistant's HQ and profile, and the
// build itself as the draft. Observe and ResolveRoot must see the same target.
// The draft id carries a prefix no composer draft can have (the composer's
// must parse as a UUID), so a page cannot reach a build's held selection
// through the composer's own folder endpoints.
func buildFolderTarget(userID string, projection *personalassistant.Projection, sessionID string) foldercontext.Target {
	target := foldercontext.Target{UserID: strings.TrimSpace(userID), DraftID: "build:" + strings.TrimSpace(sessionID)}
	if projection != nil {
		target.WorkspaceID = strings.TrimSpace(projection.HQWorkspaceID)
		target.AgentName = strings.ToLower(strings.TrimSpace(projection.GlobalAgentProfile))
	}
	return target
}

func (h *Handler) getWorkspaceBuildFolderChoices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := h.workspaceBuildUserID(ctx)
	readiness := h.workspaceBuildReadiness(ctx, userID)
	if !readiness.available {
		respondBuildUnavailable(w, readiness.reason)
		return
	}
	deps := h.workspaceBuild
	if deps.Folders == nil {
		_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	view, err := deps.Folders.Choices(ctx, userID)
	if err != nil {
		respondBuildFolderError(w, err)
		return
	}
	chips := view.Chips
	if chips == nil {
		chips = []personalassistant.FolderChip{}
	}
	_ = orihttp.RespondJSON(w, http.StatusOK, map[string]any{
		"available": true, "chips": chips, "picker_available": view.PickerAvailable, "picker_note": view.PickerNote,
	})
}

// respondBuildFolderError says, in the user's words, why a folder was not
// attached. Every refusal leaves the draft and any attached folder as they
// were, and the code lets the pane tell a folder refusal from a build one.
func respondBuildFolderError(w http.ResponseWriter, err error) {
	message := "That folder could not be used. Pick again; your draft and any folder already attached are unchanged."
	var rootErr *personalassistant.FolderRootError
	switch {
	case errors.Is(err, errBuildFoldersOff):
		message = "Folders can’t be added to this build right now."
	case errors.Is(err, personalassistant.ErrFolderScanBusy):
		message = "A folder is still being looked at. Wait for it to finish."
	case errors.Is(err, personalassistant.ErrFolderPickerUnavailable):
		message = "The folder dialog is unavailable here. Choose a listed folder or paste a path."
	case errors.Is(err, personalassistant.ErrFolderSelectionLimit):
		message = "Too many folders were looked at recently. Try again in a few minutes."
	case errors.As(err, &rootErr):
		message = rootErr.Message
	}
	_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{"error": message, "code": "folder"})
}

// postWorkspaceBuildFolder attaches a folder to an open build. The build must
// be current when the choice is made; the pick itself then stands even if the
// build moved on while a dialog was open, because the user made it.
func (h *Handler) postWorkspaceBuildFolder(w http.ResponseWriter, r *http.Request, id string) {
	var req buildFolderRequest
	if !decodeBuildBody(w, r, &req) {
		return
	}
	if req.RequestID != "" && !validBuildOperationID(req.RequestID) {
		_ = orihttp.RespondBadRequest(w, "Invalid folder request identity")
		return
	}
	req.Mode, req.Chip, req.SelectionToken = strings.TrimSpace(req.Mode), strings.TrimSpace(req.Chip), strings.TrimSpace(req.SelectionToken)
	choice := ""
	switch req.Mode {
	case personalassistant.FolderObserveChip:
		choice = req.Chip
		if choice == "" || req.SelectionToken != "" {
			_ = orihttp.RespondBadRequest(w, "Choose a folder with the folder controls")
			return
		}
	case personalassistant.FolderObservePicker:
		if req.Chip != "" || req.SelectionToken != "" {
			_ = orihttp.RespondBadRequest(w, "Choose a folder with the folder controls")
			return
		}
	case personalassistant.FolderObserveSelection:
		choice = req.SelectionToken
		if choice == "" || req.Chip != "" {
			_ = orihttp.RespondBadRequest(w, "Choose a folder with the folder controls")
			return
		}
	default:
		_ = orihttp.RespondBadRequest(w, "Choose a folder with the folder controls")
		return
	}
	ctx := r.Context()
	userID := h.workspaceBuildUserID(ctx)
	readiness := h.workspaceBuildReadiness(ctx, userID)
	if !readiness.available {
		respondBuildUnavailable(w, readiness.reason)
		return
	}
	deps := h.workspaceBuild
	if deps.Folders == nil {
		respondBuildFolderError(w, errBuildFoldersOff)
		return
	}
	// The build is checked before the folder is looked at (a dialog can stay
	// open for a while), so a stale caller is refused before it opens one.
	doc, err := deps.Store.Read(ctx, userID)
	if err != nil {
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	current := doc.Session(id)
	switch {
	case current == nil:
		_ = orihttp.RespondJSON(w, http.StatusNotFound, map[string]any{"error": buildErrorText("not_found"), "code": "not_found"})
		return
	case current.Status != personalassistant.WorkspaceBuildOpen:
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{"error": buildErrorText("closed"), "code": "closed"})
		return
	case current.Version != req.Version:
		_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{"error": buildErrorText("version"), "code": "version"})
		return
	}
	claimID, err := h.claimBuildAttachment(ctx, userID, id, req.Version, req.RequestID, true)
	if err != nil {
		respondBuildMutationError(w, err, id, "claimed")
		return
	}
	defer h.releaseBuildAttachment(ctx, userID, id, claimID, true)
	target := buildFolderTarget(userID, readiness.projection, current.ID)
	observation, err := deps.Folders.Observe(ctx, target, req.Mode, choice)
	if err != nil {
		logger.Info("Workspace build folder refused", logger.Fields{"session_id": id, "mode": req.Mode, "error": err})
		respondBuildFolderError(w, err)
		return
	}
	if observation == nil {
		// The dialog was cancelled: nothing changes, and the pane hears so.
		h.releaseBuildAttachment(ctx, userID, id, claimID, true)
		if latest, readErr := deps.Store.Read(ctx, userID); readErr == nil {
			current = latest.Session(id)
		}
		h.respondBuildSession(r.Context(), w, http.StatusOK, current, map[string]any{"cancelled": true})
		return
	}
	// What the folder digest recognized in the same scan. A folder that cannot
	// be recognized right now is still attached; the pane then says nothing
	// about its kind rather than guess.
	recognition, err := deps.Folders.Recognize(ctx, target, observation.ID)
	if err != nil {
		logger.Info("Workspace build folder not recognized", logger.Fields{"session_id": id, "error": err})
		recognition = nil
	}
	// What the recognized folder's setup needs, said once the folder is in.
	probe := *current
	probe.Folder = &personalassistant.BuildFolder{Observation: *observation, Recognition: recognition}
	plan := h.workspaceBuildPlan(ctx, userID, readiness.projection, &probe)
	var result personalassistant.WorkspaceBuildSession
	_, err = deps.Store.Mutate(ctx, userID, func(doc *personalassistant.WorkspaceBuildDocument) error {
		build := doc.Session(id)
		if build == nil {
			return errBuildTurn{http.StatusNotFound, "not_found"}
		}
		if build.Status != personalassistant.WorkspaceBuildOpen {
			return errBuildTurn{http.StatusConflict, "closed"}
		}
		if build.FolderRequestID != claimID {
			return errBuildTurn{http.StatusConflict, "version"}
		}
		now := deps.Store.Now()
		build.FolderRequestID = ""
		build.InvalidateBuildInference(false)
		build.Folder = &personalassistant.BuildFolder{Observation: *observation, Recognition: recognition}
		build.Proposal = nil
		if genericBuildFolder(build) && build.Setup == nil {
			build.Proposal = &personalassistant.BuildProposal{ID: uuid.NewString(), FolderID: observation.ID, ContextVersion: build.ContextVersion, Status: "pending"}
		}
		build.Append(personalassistant.BuildTranscriptEntry{
			Role: personalassistant.BuildRoleForm, Text: fmt.Sprintf(buildCopyFolderAttached, observation.Folder), At: now,
		})
		noteBuildNeeds(build, plan, now)
		build.Applied = nil
		build.Touch(now)
		result = *build
		return nil
	})
	if err != nil {
		var turnErr errBuildTurn
		if errors.As(err, &turnErr) {
			_ = orihttp.RespondJSON(w, turnErr.status, map[string]any{"error": buildErrorText(turnErr.code), "code": turnErr.code})
			return
		}
		logger.Warn("Workspace build folder could not be saved", logger.Fields{"session_id": id, "error": err})
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	logger.Info("Workspace build folder attached", logger.Fields{"session_id": id, "mode": req.Mode, "files": observation.Files})
	h.respondBuildSession(r.Context(), w, http.StatusOK, &result, nil)
}

// postWorkspaceBuildFolderDetach removes the attached folder. Removing twice
// is harmless; the second time says nothing in the transcript.
func (h *Handler) postWorkspaceBuildFolderDetach(w http.ResponseWriter, r *http.Request, id string) {
	var req buildFolderDetachRequest
	if !decodeBuildBody(w, r, &req) {
		return
	}
	ctx := r.Context()
	deps := h.workspaceBuild
	if deps == nil {
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	var result personalassistant.WorkspaceBuildSession
	_, err := deps.Store.Mutate(ctx, h.workspaceBuildUserID(ctx), func(doc *personalassistant.WorkspaceBuildDocument) error {
		build := doc.Session(id)
		if build == nil {
			return errBuildTurn{http.StatusNotFound, "not_found"}
		}
		if build.Status != personalassistant.WorkspaceBuildOpen {
			return errBuildTurn{http.StatusConflict, "closed"}
		}
		if build.Version != req.Version {
			return errBuildTurn{http.StatusConflict, "version"}
		}
		if build.Folder != nil || build.FolderRequestID != "" {
			now := deps.Store.Now()
			build.InvalidateBuildInference(false)
			build.FolderRequestID = ""
			build.Proposal = nil
			build.Folder = nil
			build.Append(personalassistant.BuildTranscriptEntry{Role: personalassistant.BuildRoleForm, Text: buildCopyFolderRemoved, At: now})
			build.Applied = nil
			build.Touch(now)
		}
		result = *build
		return nil
	})
	if err != nil {
		var turnErr errBuildTurn
		if errors.As(err, &turnErr) {
			_ = orihttp.RespondJSON(w, turnErr.status, map[string]any{"error": buildErrorText(turnErr.code), "code": turnErr.code})
			return
		}
		logger.Warn("Workspace build folder could not be removed", logger.Fields{"session_id": id, "error": err})
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	logger.Info("Workspace build folder removed", logger.Fields{"session_id": id})
	h.respondBuildSession(r.Context(), w, http.StatusOK, &result, nil)
}

// buildFolderPromptJSON is the attached folder as the model sees it: the
// observation's names, kinds, counts, markers, dates, and coverage, with no
// ids — the projection the assistant's composer sends for a folder turn,
// without a discussion focus. JSON's HTML escaping keeps a name from closing
// the reference-data delimiter.
func buildFolderPromptJSON(observation foldercontext.Observation) (string, error) {
	type project struct {
		Name        string `json:"name"`
		Files       int    `json:"files"`
		Marker      string `json:"marker,omitempty"`
		Root        bool   `json:"root,omitempty"`
		LastChanged string `json:"last_changed,omitempty"`
	}
	projects := make([]project, 0, len(observation.Projects))
	for _, item := range observation.Projects {
		projects = append(projects, project{item.Name, item.Files, item.Marker, item.Root, item.LastChanged})
	}
	// A parent index denotes only a recorded relationship, never a path or id.
	type entry struct {
		Name   string `json:"name"`
		Kind   string `json:"kind"`
		Parent int    `json:"parent"`
	}
	type treeCoverage struct {
		Omitted int `json:"omitted"`
	}
	var entries []entry
	var tree *treeCoverage
	if observation.Tree != nil {
		indexes := map[string]int{"": -1}
		for index, node := range observation.Tree.Nodes {
			entries = append(entries, entry{node.Name, node.Kind, indexes[node.ParentID]})
			indexes[node.ID] = index
		}
		tree = &treeCoverage{Omitted: observation.Tree.Omitted}
	}
	data, err := json.Marshal(struct {
		Folder          string                 `json:"folder"`
		ScannedAt       string                 `json:"scanned_at"`
		Files           int                    `json:"files"`
		Entries         int                    `json:"entries"`
		Kinds           []foldercontext.Kind   `json:"kinds"`
		Projects        []project              `json:"projects"`
		Coverage        foldercontext.Coverage `json:"coverage"`
		ObservedEntries []entry                `json:"observed_entries,omitempty"`
		TreeCoverage    *treeCoverage          `json:"tree_coverage,omitempty"`
	}{
		observation.Folder, observation.ScannedAt.UTC().Format("2006-01-02T15:04:05Z"), observation.Files, observation.Entries,
		observation.Kinds, projects, observation.Coverage, entries, tree,
	})
	if err != nil || len(data) > foldercontext.MaxBytes+1024 {
		return "", foldercontext.ErrInvalid
	}
	return string(data), nil
}

// createWorkspaceBuildFolderPlan is a build's attached folder, resolved to the
// server's own root before anything is created so a lost pick refuses the
// create and leaves nothing behind.
type createWorkspaceBuildFolderPlan struct {
	sessionID   string
	name        string
	root        string
	referenceID string
}

// Why a build folder refused a create. The pick the server held for the
// folder lasts as long as the composer's (30 minutes); the session's snapshot
// cannot stand in for it.
var (
	errBuildFolderLost = &createWorkspaceAttachError{
		status:  http.StatusConflict,
		kind:    "build_folder",
		message: "The folder attached in the conversation is no longer available to Ori — a pick lasts 30 minutes. Add the folder again, or remove it, then create the workspace.",
	}
	errBuildFolderNotLinked = &createWorkspaceAttachError{
		status:  http.StatusConflict,
		kind:    "build_folder",
		message: "The attached folder could not be linked, so the workspace was not created. Check that the folder still exists, then try again.",
	}
	errBuildFolderWorkspaceUnavailable = &createWorkspaceAttachError{
		status:  http.StatusInternalServerError,
		kind:    "build_folder",
		message: "The workspace folder could not be created, so the attached folder was not linked and the workspace was not created.",
	}
	// errBuildFolderSetItUp refuses the wizard's create for a folder Ori sets
	// up itself: the plan, not the form, decides that workspace.
	errBuildFolderSetItUp = &createWorkspaceAttachError{
		status:  http.StatusConflict,
		kind:    "build_folder",
		message: "Ori sets this folder up from the plan in the conversation: use Set it up there. To create the workspace yourself instead, remove the folder first.",
	}
)

// planCreateWorkspaceBuildFolder resolves the build's attached folder, if the
// create names a build that has one. A create that names no build, an unknown
// or settled build, or a build without a folder is never refused here; a
// folder the server no longer holds is.
func (h *Handler) planCreateWorkspaceBuildFolder(ctx context.Context, req createWorkspaceRequest, kind session.WorkspaceKind) (*createWorkspaceBuildFolderPlan, *createWorkspaceAttachError) {
	sessionID := strings.TrimSpace(req.BuildSessionID)
	deps := h.workspaceBuild
	if sessionID == "" || deps == nil || deps.Folders == nil || kind == session.WorkspaceKindGroup {
		return nil, nil
	}
	userID := h.workspaceBuildUserID(ctx)
	doc, err := deps.Store.Read(ctx, userID)
	if err != nil {
		logger.Info("Workspace build not read for a create", logger.Fields{"session_id": sessionID, "error": err})
		return nil, nil
	}
	build := doc.Session(sessionID)
	if build == nil || build.Status != personalassistant.WorkspaceBuildOpen || build.Folder == nil {
		return nil, nil
	}
	projection, err := deps.Assistant(ctx, userID)
	if err != nil || projection == nil {
		return nil, errBuildFolderLost
	}
	// A recognized folder whose plan Set it up can run, or already started,
	// is never also created by the wizard.
	if build.Setup != nil {
		return nil, errBuildFolderSetItUp
	}
	if plan, _ := h.readBuildPlan(ctx, userID, projection, build); plan != nil && plan.Ready {
		return nil, errBuildFolderSetItUp
	}
	target := buildFolderTarget(userID, projection, build.ID)
	root, err := deps.Folders.ResolveRoot(ctx, target, build.Folder.Observation.ID)
	if err != nil {
		logger.Info("Workspace build folder is no longer held for a create", logger.Fields{"session_id": sessionID, "error": err})
		return nil, errBuildFolderLost
	}
	return &createWorkspaceBuildFolderPlan{sessionID: build.ID, name: build.Folder.Observation.Folder, root: root}, nil
}

// recordCreateWorkspaceBuildFolder links the build's attached folder to the
// new workspace as a linked directory, in place: nothing is copied, moved, or
// changed in it. It becomes the workspace's primary directory unless the
// create already gave it one outside the workspace folder (an attached
// project); a scaffold the blueprint just made inside the workspace folder is
// superseded, as it is when Home's folder offer links a shown folder.
func (h *Handler) recordCreateWorkspaceBuildFolder(ctx context.Context, ws *session.Workspace, folderWS *agentworkspace.Workspace, workspaceFolder string, plan *createWorkspaceBuildFolderPlan) error {
	if h.workspaceStore == nil || ws == nil || folderWS == nil || plan == nil {
		return errBuildFolderWorkspaceUnavailable
	}
	// AttachLinkedDirectory makes the link the primary; a primary the create
	// already set outside the workspace folder (an attached project) is put
	// back afterwards, while a scaffold inside it is superseded.
	previous, _ := folderWS.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string)
	dirID, err := projecttemplates.AttachLinkedDirectory(folderWS, plan.name, plan.root)
	if err != nil {
		logger.Warn("Workspace build folder could not be linked", logger.Fields{"workspace_id": ws.ID, "error": err})
		return errBuildFolderNotLinked
	}
	plan.referenceID = dirID
	primary := true
	if previous = strings.TrimSpace(previous); previous != "" && previous != dirID {
		if ref, err := folderWS.GetDirectoryReference(previous); err == nil && ref != nil && !pathInsideFolder(workspaceFolder, ref.Path) {
			projecttemplates.SetPrimaryDirectoryID(folderWS.SharedData, previous)
			primary = false
		}
	}
	now := time.Now()
	folderWS.UpdatedAt = now
	if err := h.workspaceStore.Save(folderWS); err != nil {
		logger.Error("Failed to persist a build's linked folder", logger.Fields{"id": ws.ID, "error": err})
		return errBuildFolderWorkspaceUnavailable
	}
	refsJSON, err := json.Marshal(folderWS.DirectoryReferences)
	if err != nil {
		return errBuildFolderWorkspaceUnavailable
	}
	ws.DirectoryReferencesJSON = refsJSON
	ws.SharedData = folderWS.SharedData
	ws.UpdatedAt = now
	// Required, as for an attached project: later create steps read-modify-write
	// through the session row, and a row without the link would erase it.
	if err := h.store.UpdateWorkspace(ctx, ws); err != nil {
		logger.Error("Failed to sync a build's linked folder to the session store", logger.Fields{"id": ws.ID, "error": err})
		return errBuildFolderWorkspaceUnavailable
	}
	logger.Info("Workspace build folder linked", logger.Fields{"workspace_id": ws.ID, "directory_id": dirID, "primary": primary})
	return nil
}

// pathInsideFolder reports whether child is parent or lies under it.
func pathInsideFolder(parent, child string) bool {
	parent, child = filepath.Clean(strings.TrimSpace(parent)), filepath.Clean(strings.TrimSpace(child))
	if parent == "" || parent == "." || child == "" {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
