package sessionhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// Set it up: a recognized folder's plan runs as Home's own one-card setup
// journey, on the folder the build holds.
//
//	GET  /api/workspaces/build-sessions/{id}        the session, read fresh (polled while it runs)
//	POST /api/workspaces/build-sessions/{id}/setup  run the plan the user confirmed
//
// The build is a conversation review of its own (personalassistant
// BuildReviewTarget): Set it up makes or reuses the canonical offer, claims
// the build for the run, and starts Home's StartSetup on it with the digest
// the user confirmed. The run outlives the request; the folder digest tells the
// build when it stops or finishes (WorkspaceBuildSetupSettled), and the build
// then reopens with the reason, or closes with what was made. The build never
// posts the wizard's create on this path.

type buildSetupRequest struct {
	Version    int64  `json:"version"`
	PlanDigest string `json:"plan_digest"`
	// EntryName answers a run that stopped to ask which project file to use;
	// it must be one of the run's own candidates.
	EntryName string `json:"entry_name,omitempty"`
}

// Fixed host lines for a setup run.
const (
	buildCopySetupReviewPending = "Another folder setup is waiting for an answer on Home. Finish or close it there, then Set it up."
	buildCopySetupDeclined      = "Setup suggestions for this folder were declined earlier, so Set it up is not available for it."
	buildCopySetupUnavailable   = "Set it up is not available for this folder right now. The form still works."
	buildCopySetupReceiptNew    = "Created %s and %s inside it. Linked folder “%s”."
	buildCopySetupReceiptJoined = "Added %s to your %s. Linked folder “%s”."
	buildCopySetupReceiptAlone  = "Created %s. Linked folder “%s”."
)

// buildSetupStopCopy says why a run stopped, in the user's words. What
// finished is kept; Retry continues the same run.
var buildSetupStopCopy = map[string]string{
	personalassistant.FolderStopPlanChanged:      "What Set it up would do has changed, so it stopped before going further. Review the plan, then Retry.",
	personalassistant.FolderStopNeedsPick:        "Ori no longer holds the attached folder. Add the folder again, then Retry.",
	personalassistant.FolderStopNeedsChoice:      "The folder has more than one project file. Choose the one to set up.",
	personalassistant.FolderStopNeedsModel:       "The workspace and folder are set up. Its agent needs a model before it can start.",
	personalassistant.FolderStopInstallFailed:    "A plugin could not be installed or enabled, so nothing after it ran. Retry continues from there.",
	personalassistant.FolderStopInterrupted:      "Set it up was interrupted before it finished. Retry continues from there.",
	personalassistant.FolderStopConsentStale:     "The workspace and folder are set up. The Home's shared assistant was approved for an older version of this blueprint, so it was not added.",
	personalassistant.FolderStopAssistantMissing: "The workspace and folder are set up. The Home's shared assistant is gone, so none was added.",
}

func buildSetupStopText(reason string) string {
	if text := buildSetupStopCopy[reason]; text != "" {
		return text
	}
	return "A step did not finish. Retry continues from there; what finished is kept."
}

// buildSetupRunView is a setup run as the pane draws it: the plan's lines
// with their states, and why it stopped.
type buildSetupRunView struct {
	Status          string                             `json:"status"`
	StopReason      string                             `json:"stop_reason,omitempty"`
	Lines           []personalassistant.FolderPlanLine `json:"lines"`
	EntryCandidates []string                           `json:"entry_candidates,omitempty"`
}

// buildReviewGate is the build's lease on its own reviews: one setup request
// at a time per build, refused rather than queued.
type buildReviewGate struct{ active sync.Map }

func (g *buildReviewGate) enter(target foldercontext.Target) (func(), bool) {
	if _, busy := g.active.LoadOrStore(target, true); busy {
		return func() {}, false
	}
	return func() { g.active.Delete(target) }, true
}

// WorkspaceBuildReviewAuthority is the guard and lease the folder service
// asks about a review a build owns (FolderObservationService.ConfigureBuildReviews).
func (h *Handler) WorkspaceBuildReviewAuthority() (func(context.Context, personalassistant.FolderOffer) error, func(foldercontext.Target) (func(), bool)) {
	return h.workspaceBuildReviewGuard, h.buildReviews.enter
}

// workspaceBuildReviewGuard vouches for a build review only while its build
// still stands behind it: the same user, assistant and HQ, the build still
// open or preparing on the very observation the offer names, and, once the
// offer is decided, the build's own claim on that offer.
func (h *Handler) workspaceBuildReviewGuard(ctx context.Context, offer personalassistant.FolderOffer) error {
	review := offer.ConversationReview
	sessionID := personalassistant.BuildSessionOf(offer)
	deps := h.workspaceBuild
	if review == nil || sessionID == "" || deps == nil {
		return personalassistant.ErrBuildSetupNotOwned
	}
	userID := review.Target.UserID
	doc, err := deps.Store.Read(ctx, userID)
	if err != nil {
		return personalassistant.ErrBuildSetupNotOwned
	}
	session := doc.Session(sessionID)
	if session == nil || (session.Status != personalassistant.WorkspaceBuildOpen && session.Status != personalassistant.WorkspaceBuildPreparing) ||
		session.Folder == nil || session.Folder.Observation.ID != review.ObservationID {
		return personalassistant.ErrBuildSetupNotOwned
	}
	projection, err := deps.Assistant(ctx, userID)
	if err != nil || projection == nil || personalassistant.BuildReviewTarget(buildFolderTarget(userID, projection, sessionID), sessionID) != review.Target {
		return personalassistant.ErrBuildSetupNotOwned
	}
	if offer.DecidedAt != nil && (session.Setup == nil || session.Setup.OfferID != offer.ID) {
		return personalassistant.ErrBuildSetupNotOwned
	}
	return nil
}

func (h *Handler) getWorkspaceBuildSession(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	deps := h.workspaceBuild
	if deps == nil {
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	doc, err := deps.Store.Read(ctx, h.workspaceBuildUserID(ctx))
	if err != nil {
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	session := doc.Session(id)
	if session == nil {
		_ = orihttp.RespondJSON(w, http.StatusNotFound, map[string]any{"error": buildErrorText("not_found"), "code": "not_found"})
		return
	}
	h.respondBuildSession(ctx, w, http.StatusOK, session, nil)
}

// readBuildSetupRun reads the run a build's claim names, or nil.
func (h *Handler) readBuildSetupRun(ctx context.Context, userID string, projection *personalassistant.Projection, session *personalassistant.WorkspaceBuildSession) *buildSetupRunView {
	deps := h.workspaceBuild
	if deps == nil || deps.Folders == nil || session == nil || session.Setup == nil ||
		(session.Status != personalassistant.WorkspaceBuildOpen && session.Status != personalassistant.WorkspaceBuildPreparing) {
		return nil
	}
	if projection == nil {
		read, err := deps.Assistant(ctx, userID)
		if err != nil || read == nil {
			return nil
		}
		projection = read
	}
	review := personalassistant.BuildReviewTarget(buildFolderTarget(userID, projection, session.ID), session.ID)
	offer, err := deps.Folders.ReadSetup(ctx, review, session.Setup.OfferID)
	if err != nil || offer == nil || offer.Setup == nil {
		return nil
	}
	return &buildSetupRunView{
		Status: offer.Setup.Status, StopReason: offer.Setup.StopReason,
		Lines:           append([]personalassistant.FolderPlanLine(nil), offer.Setup.Lines...),
		EntryCandidates: append([]string(nil), offer.Setup.EntryCandidates...),
	}
}

func respondBuildSetupRefusal(w http.ResponseWriter, status int, code, message string) {
	_ = orihttp.RespondJSON(w, status, map[string]any{"error": message, "code": code})
}

// postWorkspaceBuildSetup is Set it up. The first press runs the plan the
// user confirmed (its digest must be the plan the server would show now); a
// later press, after the run stopped, continues that same run.
func (h *Handler) postWorkspaceBuildSetup(w http.ResponseWriter, r *http.Request, id string) {
	var req buildSetupRequest
	if !decodeBuildBody(w, r, &req) {
		return
	}
	req.PlanDigest, req.EntryName = strings.TrimSpace(req.PlanDigest), strings.TrimSpace(req.EntryName)
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
	doc, err := deps.Store.Read(ctx, userID)
	if err != nil {
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	current := doc.Session(id)
	if current == nil {
		respondBuildSetupRefusal(w, http.StatusNotFound, "not_found", buildErrorText("not_found"))
		return
	}
	retry := current.Setup != nil
	run := h.readBuildSetupRun(ctx, userID, readiness.projection, current)
	switch {
	case current.Status == personalassistant.WorkspaceBuildOpen:
	case current.Status == personalassistant.WorkspaceBuildPreparing && retry && run != nil && run.Status == personalassistant.FolderSetupStopped:
		// The run was lost under a preparing build (the server restarted).
	default:
		respondBuildSetupRefusal(w, http.StatusConflict, "closed", buildErrorText("closed"))
		return
	}
	if current.Version != req.Version {
		respondBuildSetupRefusal(w, http.StatusConflict, "version", buildErrorText("version"))
		return
	}
	if current.Folder == nil || !current.Folder.Recognition.Recognized() {
		respondBuildSetupRefusal(w, http.StatusConflict, "no_plan", buildCopySetupUnavailable)
		return
	}
	// An unnamed draft is named after its folder, as the plan the user saw
	// says and as Home's card does.
	name := strings.TrimSpace(current.Draft.Name)
	digest := req.PlanDigest
	if retry {
		// A Retry continues the run the user confirmed, on its own digest.
		digest = current.Setup.PlanDigest
	} else {
		plan, planErr := h.readBuildPlan(ctx, userID, readiness.projection, current)
		switch {
		case planErr != nil:
			respondCreateWorkspaceAttachError(w, errBuildFolderLost)
			return
		case plan == nil || !plan.Ready:
			respondBuildSetupRefusal(w, http.StatusConflict, "no_plan", buildCopySetupUnavailable)
			return
		case plan.Digest != digest:
			h.respondBuildSession(ctx, w, http.StatusConflict, current, map[string]any{"code": "plan_changed", "error": buildSetupStopCopy[personalassistant.FolderStopPlanChanged]})
			return
		}
	}
	draft := buildFolderTarget(userID, readiness.projection, id)
	offer, err := deps.Folders.ReviewSetup(ctx, draft, id, current.Folder.Observation.ID, current.Folder.Recognition.CandidateID, name)
	if err != nil {
		logger.Info("Workspace build setup not reviewed", logger.Fields{"session_id": id, "error": err})
		switch {
		case errors.Is(err, personalassistant.ErrFolderReviewPending):
			respondBuildSetupRefusal(w, http.StatusConflict, "review_pending", buildCopySetupReviewPending)
		case errors.Is(err, personalassistant.ErrFolderOfferDecided):
			respondBuildSetupRefusal(w, http.StatusConflict, "declined", buildCopySetupDeclined)
		case errors.Is(err, personalassistant.ErrFolderSelection), errors.Is(err, personalassistant.ErrFolderPathLost):
			respondCreateWorkspaceAttachError(w, errBuildFolderLost)
		default:
			respondBuildUnavailable(w, buildReasonAssistantNotReady)
		}
		return
	}
	// The build is claimed for the run before it starts: once the offer is
	// decided, the guard vouches for it only through this claim.
	requestID := uuid.NewString()
	claimed, err := deps.Store.Mutate(ctx, userID, func(doc *personalassistant.WorkspaceBuildDocument) error {
		session := doc.Session(id)
		if session == nil {
			return errBuildTurn{http.StatusNotFound, "not_found"}
		}
		if session.Version != req.Version || session.Folder == nil || session.Folder.Observation.ID != current.Folder.Observation.ID {
			return errBuildTurn{http.StatusConflict, "version"}
		}
		now := deps.Store.Now()
		session.Status = personalassistant.WorkspaceBuildPreparing
		session.Setup = &personalassistant.BuildSetupClaim{OfferID: offer.ID, PlanDigest: digest, RequestID: requestID, StartedAt: now}
		session.Folder.Recognition.OfferID = offer.ID
		session.PendingQuestion = nil
		session.CreateNow = false
		session.Touch(now)
		return nil
	})
	if err != nil {
		var turnErr errBuildTurn
		if errors.As(err, &turnErr) {
			respondBuildSetupRefusal(w, turnErr.status, turnErr.code, buildErrorText(turnErr.code))
			return
		}
		logger.Warn("Workspace build setup could not be claimed", logger.Fields{"session_id": id, "error": err})
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	if _, err := deps.Folders.StartSetup(ctx, userID, offer.ID, digest, requestID, req.EntryName); err != nil {
		reason := personalassistant.FolderStopFailed
		switch {
		case errors.Is(err, personalassistant.ErrFolderPlanChanged):
			reason = personalassistant.FolderStopPlanChanged
		case errors.Is(err, personalassistant.ErrFolderSelection), errors.Is(err, personalassistant.ErrFolderPathLost):
			reason = personalassistant.FolderStopNeedsPick
		}
		logger.Info("Workspace build setup did not start", logger.Fields{"session_id": id, "reason": reason, "error": err})
		reopened := h.reopenWorkspaceBuildSetup(ctx, userID, id, offer.ID, reason)
		if reopened == nil {
			reopened = claimed.Session(id)
		}
		h.respondBuildSession(ctx, w, http.StatusConflict, reopened, map[string]any{"code": reason, "error": buildSetupStopText(reason)})
		return
	}
	logger.Info("Workspace build setup started", logger.Fields{"session_id": id, "offer_id": offer.ID, "retry": retry})
	fresh, err := deps.Store.Read(ctx, userID)
	if err != nil || fresh.Session(id) == nil {
		h.respondBuildSession(ctx, w, http.StatusOK, claimed.Session(id), nil)
		return
	}
	h.respondBuildSession(ctx, w, http.StatusOK, fresh.Session(id), nil)
}

// reopenWorkspaceBuildSetup returns a build whose run stopped (or never
// started) to open, says why as a host line, and keeps its claim so Retry
// continues the same run. It returns the reopened session, or nil.
func (h *Handler) reopenWorkspaceBuildSetup(ctx context.Context, userID, sessionID, offerID, reason string) *personalassistant.WorkspaceBuildSession {
	deps := h.workspaceBuild
	var result *personalassistant.WorkspaceBuildSession
	_, err := deps.Store.Mutate(ctx, userID, func(doc *personalassistant.WorkspaceBuildDocument) error {
		session := doc.Session(sessionID)
		if session == nil || session.Status != personalassistant.WorkspaceBuildPreparing || session.Setup == nil || session.Setup.OfferID != offerID {
			return errBuildTurn{http.StatusConflict, "closed"}
		}
		now := deps.Store.Now()
		session.Status = personalassistant.WorkspaceBuildOpen
		session.Setup.StopReason = reason
		session.Append(personalassistant.BuildTranscriptEntry{Role: personalassistant.BuildRoleForm, Text: buildSetupStopText(reason), Host: true, At: now})
		session.Touch(now)
		copied := cloneBuildSession(*session)
		result = &copied
		return nil
	})
	if err != nil {
		logger.Info("Workspace build setup not reopened", logger.Fields{"session_id": sessionID, "reason": err.Error()})
		return nil
	}
	return result
}

// WorkspaceBuildSetupSettled hears the folder digest settle a run a build
// started: a stop reopens the build with the reason; a resolved offer closes it
// with what was made. Offers no build owns are ignored. Wired as a
// FolderDigestService setup observer; it runs on the run's own goroutine.
func (h *Handler) WorkspaceBuildSetupSettled(ctx context.Context, userID string, offer personalassistant.FolderOffer) {
	sessionID := personalassistant.BuildSessionOf(offer)
	if h == nil || h.workspaceBuild == nil || sessionID == "" {
		return
	}
	switch {
	case offer.Status == personalassistant.FolderOfferResolved && offer.Outcome != nil && strings.TrimSpace(offer.Outcome.WorkspaceID) != "":
		h.finishWorkspaceBuildSetup(ctx, userID, sessionID, offer)
	case offer.Setup != nil && offer.Setup.Status == personalassistant.FolderSetupStopped:
		h.reopenWorkspaceBuildSetup(ctx, userID, sessionID, offer.ID, offer.Setup.StopReason)
	}
}

// buildSetupReceipt says what a finished run made, from the offer the run
// resolved, never from the model: the Home it made or joined, the workspace,
// and the linked folder.
func buildSetupReceipt(offer personalassistant.FolderOffer, workspaceName string) (receipt, homeID string) {
	folder := strings.TrimSpace(offer.Subject.Name)
	home := offer.Outcome.Parent
	if home == nil || strings.TrimSpace(home.Name) == "" || home.Status == "standalone" {
		return fmt.Sprintf(buildCopySetupReceiptAlone, workspaceName, folder), ""
	}
	created := offer.Setup != nil && offer.Setup.Intent.Destination != nil && offer.Setup.Intent.Destination.Status == "new"
	if created {
		return fmt.Sprintf(buildCopySetupReceiptNew, home.Name, workspaceName, folder), home.WorkspaceID
	}
	return fmt.Sprintf(buildCopySetupReceiptJoined, workspaceName, home.Name, folder), home.WorkspaceID
}

// buildSetupWorkspaceName is the name the run gave the project workspace: its
// own receipt row, else the draft's name, else the folder's (the journey names
// an unnamed workspace after its folder).
func buildSetupWorkspaceName(offer personalassistant.FolderOffer, draftName string) string {
	name := firstNonEmpty(draftName, offer.Subject.Name)
	for _, row := range offer.Outcome.Receipt {
		if row.Kind == "workspace" && strings.TrimSpace(row.Name) != "" {
			name = strings.TrimSpace(row.Name)
		}
	}
	return name
}

// finishWorkspaceBuildSetup closes the build a run finished, the way a create
// closes it (finishWorkspaceBuild): created, with the project workspace and the
// Home, so a later create can never reopen it. The receipt is written from the
// resolved offer; the draft's description, goal and extra teammate are then
// given to the workspace the run made; and "How this was set up" is written
// from the plan the user confirmed and the run's own receipts.
func (h *Handler) finishWorkspaceBuildSetup(ctx context.Context, userID, sessionID string, offer personalassistant.FolderOffer) {
	deps := h.workspaceBuild
	workspaceID := strings.TrimSpace(offer.Outcome.WorkspaceID)
	var finished personalassistant.WorkspaceBuildSession
	_, err := deps.Store.Mutate(ctx, userID, func(doc *personalassistant.WorkspaceBuildDocument) error {
		session := doc.Session(sessionID)
		if session == nil || session.Setup == nil || session.Setup.OfferID != offer.ID ||
			(session.Status != personalassistant.WorkspaceBuildPreparing && session.Status != personalassistant.WorkspaceBuildOpen) {
			return errBuildTurn{http.StatusConflict, "closed"}
		}
		now := deps.Store.Now()
		name := buildSetupWorkspaceName(offer, session.Draft.Name)
		receipt, homeID := buildSetupReceipt(offer, name)
		// An unnamed build keeps the name the run gave it: the Map, Today and
		// the summary say it.
		if strings.TrimSpace(session.Draft.Name) == "" {
			session.Draft.Name = name
		}
		session.Append(personalassistant.BuildTranscriptEntry{Role: personalassistant.BuildRoleForm, Text: receipt, Host: true, At: now})
		session.Status = personalassistant.WorkspaceBuildCreated
		session.CreatedWorkspaceID = workspaceID
		session.HomeWorkspaceID = homeID
		session.SetupReceipt = receipt
		session.PendingQuestion = nil
		session.CreateNow = false
		session.Touch(now)
		finished = cloneBuildSession(*session)
		session.Settle()
		return nil
	})
	if err != nil {
		logger.Info("Workspace build setup not recorded", logger.Fields{"session_id": sessionID, "offer_id": offer.ID, "reason": err.Error()})
		return
	}
	logger.Info("Workspace build set up", logger.Fields{"session_id": sessionID, "workspace_id": workspaceID, "home_id": finished.HomeWorkspaceID})
	decisions := h.applyBuildSetupDetails(workspaceID, finished)
	h.applyCreateWorkspaceGoal(workspaceID, finished.Draft.Mission)
	store := h.taskMutationStore()
	if store == nil {
		return
	}
	summary := &agentworkspace.BuildSummary{
		AssistantName: finished.Assistant.DisplayName, SessionID: finished.ID, CreatedAt: finished.UpdatedAt,
		TurnCount: finished.TurnCount, UserRequest: truncateRunes(finished.FirstRequest, personalassistant.WorkspaceBuildMaxFirstRequest),
	}
	if offer.Setup != nil {
		for _, line := range offer.Setup.Lines {
			if line.State == personalassistant.FolderLineDone && strings.TrimSpace(line.Name) != "" {
				summary.Decisions = append(summary.Decisions, agentworkspace.BuildDecision{Section: "setup", Text: line.Name})
			}
		}
	}
	summary.Decisions = append(summary.Decisions, decisions...)
	summary.Decisions = append(summary.Decisions, agentworkspace.BuildDecision{Section: "setup", Text: finished.SetupReceipt})
	if err := store.Update(workspaceID, func(w *agentworkspace.Workspace) error {
		provenance := w.GetTemplateProvenance()
		if provenance == nil {
			provenance = &agentworkspace.TemplateProvenance{}
		}
		provenance.BuildSummary = summary
		w.SetTemplateProvenance(provenance)
		return nil
	}); err != nil {
		logger.Warn("Workspace build setup summary could not be recorded", logger.Fields{"workspace_id": workspaceID, "error": err})
	}
}

// applyBuildSetupDetails gives the workspace a run made what the build's
// draft added to the plan: its description and the one extra saved teammate.
// Best effort, like the goal: the workspace stands either way. It returns the
// decisions it carried out.
func (h *Handler) applyBuildSetupDetails(workspaceID string, finished personalassistant.WorkspaceBuildSession) []agentworkspace.BuildDecision {
	store := h.taskMutationStore()
	description := strings.TrimSpace(finished.Draft.Description)
	teammate := ""
	if finished.TeamPatch != nil && len(finished.TeamPatch.SavedAgents) == 1 {
		teammate = strings.TrimSpace(finished.TeamPatch.SavedAgents[0])
	}
	if store == nil || (description == "" && teammate == "") {
		return nil
	}
	var decisions []agentworkspace.BuildDecision
	if err := store.Update(workspaceID, func(w *agentworkspace.Workspace) error {
		decisions = nil
		if description != "" {
			w.Description = description
		}
		if teammate != "" {
			if err := w.AddAgent(teammate); err == nil || errors.Is(err, agentworkspace.ErrAgentAlreadyInWorkspace) {
				decisions = append(decisions, agentworkspace.BuildDecision{Section: "team", Text: fmt.Sprintf("Added %s as a teammate.", teammate)})
			}
		}
		w.UpdatedAt = time.Now()
		return nil
	}); err != nil {
		logger.Warn("Workspace build setup details could not be applied", logger.Fields{"workspace_id": workspaceID, "error": err})
		return nil
	}
	return decisions
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
