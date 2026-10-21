package server

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacesettings"
)

// softwareClaims serializes software setups by owner and directory. Two offers
// about one checkout (two tabs, a card and a conversation, a click repeated
// under a different offer) take turns, and the second reads what the first
// saved before it decides anything. It is a lock, not the record: what makes a
// repeat safe across a restart is that every pass starts from saved state.
var softwareClaims = struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}{locks: map[string]*sync.Mutex{}}

func claimSoftwareProject(userID, identity string) (release func()) {
	key := userID + "\x00" + identity
	softwareClaims.mu.Lock()
	lock, found := softwareClaims.locks[key]
	if !found {
		lock = &sync.Mutex{}
		softwareClaims.locks[key] = lock
	}
	softwareClaims.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}

// softwareWorkflowPatch is the workflow a newly set up software project starts
// with: the software profile's planning-first style, with planning documents
// kept in Ori. Nothing in it starts work on its own, and nothing in it writes
// to the project; the user can change either later in the workspace's settings.
func softwareWorkflowPatch() map[string]any {
	return map[string]any{
		"profile":  projecttemplates.BehaviorProfileSoftwareProject,
		"preset":   "planner",
		"planning": map[string]any{"write_prd": false, "write_task_list": false},
	}
}

// softwareWorkflowSignature names the workflow softwareWorkflowPatch produces,
// read from the effective policy rather than from the patch, so a review is
// tied to what the settings really do. ok is false when those settings would
// start approved work on their own, skip confirmation, or write planning
// documents into the project: a setup is not offered on such defaults.
func softwareWorkflowSignature() (signature string, ok bool) {
	shared, _ := workspacesettings.ApplyPatch(nil, softwareWorkflowPatch())
	return softwareWorkflowOf(shared)
}

// softwareWorkflowOf reads the workflow a workspace's saved settings produce.
func softwareWorkflowOf(shared map[string]any) (signature string, ok bool) {
	settings := workspacesettings.Extract(shared)
	policy := workspacesettings.BuildEffectivePolicy(settings, workspacesettings.WorkspaceCapabilities{HasFolder: true, IsRepository: true})
	approval, _ := policy.Control(workspacesettings.ControlPlanApproval)
	confirm, _ := policy.Control(workspacesettings.ControlDestructiveConfirm)
	ok = policy.PlanningEnabled && approval.Active() && confirm.Active() &&
		settings.Planning.DefaultExecutionMode != "auto" && !policy.Artifacts.WritePRD && !policy.Artifacts.WriteTaskList
	return strings.Join([]string{settings.Profile, settings.Preset, settings.Planning.DefaultExecutionMode, "documents_in_ori"}, "/"), ok
}

// softwareProgress reports a run's lines to the card as each step moves.
type softwareProgress struct {
	ctx    context.Context
	lines  []personalassistant.FolderPlanLine
	update func(context.Context, personalassistant.FolderSetupUpdate) error
}

func newSoftwareProgress(ctx context.Context, req personalassistant.FolderSetupRequest) *softwareProgress {
	lines := append([]personalassistant.FolderPlanLine(nil), req.Plan.Lines...)
	for i := range lines {
		if lines[i].State == "" || lines[i].State == personalassistant.FolderLineFailed || lines[i].State == personalassistant.FolderLineWorking {
			lines[i].State = personalassistant.FolderLineWaiting
		}
	}
	return &softwareProgress{ctx: ctx, lines: lines, update: req.Update}
}

func (p *softwareProgress) line(kind string) *personalassistant.FolderPlanLine {
	for i := range p.lines {
		if p.lines[i].Kind == kind {
			return &p.lines[i]
		}
	}
	return &personalassistant.FolderPlanLine{}
}

func (p *softwareProgress) send(status, reason string) error {
	return p.update(p.ctx, personalassistant.FolderSetupUpdate{
		Lines: append([]personalassistant.FolderPlanLine(nil), p.lines...), Status: status, StopReason: reason,
	})
}

func (p *softwareProgress) working(kind string) error {
	p.line(kind).State = personalassistant.FolderLineWorking
	return p.send(personalassistant.FolderSetupRunning, "")
}

func (p *softwareProgress) done(kind string) { p.line(kind).State = personalassistant.FolderLineDone }

// stop ends the run at a step. Lines already done stay done: what finished is
// saved, and the next pass starts from it.
func (p *softwareProgress) stop(kind, reason string) error {
	if kind != "" {
		p.line(kind).State = personalassistant.FolderLineFailed
	}
	return p.send(personalassistant.FolderSetupStopped, reason)
}

// softwareTarget is the workspace a run acts on, decided from saved state.
type softwareTarget struct {
	workspaceID string
	// ours is a workspace this offer created, now or in an earlier pass.
	ours bool
	// adopted is a workspace another setup already made for this exact folder.
	// It is opened as it is; nothing in it is changed.
	adopted bool
}

// errSoftwareChanged means saved state is no longer what the review described.
// The run stops and the user reviews again; nothing is done on a stale promise.
var errSoftwareChanged = errors.New("software setup: saved state differs from the review")

// softwareTargetFor reads which workspace the reviewed intent applies to now.
func (h *folderSetupHost) softwareTargetFor(req personalassistant.FolderSetupRequest, intent *personalassistant.FolderSoftwareIntent) (softwareTarget, error) {
	b := h.builder
	linked, err := softwareExistingProjects(b.workspaceFileStore, req.UserID, req.Path, intent.SubjectIdentity)
	if err != nil {
		return softwareTarget{}, err
	}
	if intent.Outcome == personalassistant.FolderSoftwareReuse {
		// The one workspace the review named must still be the only one here.
		if len(linked) != 1 || linked[0].WorkspaceID != intent.WorkspaceID || linked[0].Replaced {
			return softwareTarget{}, errSoftwareChanged
		}
		return softwareTarget{workspaceID: intent.WorkspaceID}, nil
	}
	own := folderWorkspaceCreator{linker: folderWorkspaceLinker{files: b.workspaceFileStore}}.existingForOffer(req.UserID, req.Offer.ID)
	if own != "" {
		for _, other := range linked {
			if other.WorkspaceID != own {
				return softwareTarget{}, errSoftwareChanged
			}
		}
		return softwareTarget{workspaceID: own, ours: true}, nil
	}
	switch {
	case len(linked) == 0:
		return softwareTarget{}, nil
	case len(linked) == 1 && linked[0].Software && !linked[0].Replaced:
		return softwareTarget{workspaceID: linked[0].WorkspaceID, adopted: true}, nil
	default:
		return softwareTarget{}, errSoftwareChanged
	}
}

// softwareRun carries out a reviewed software setup: the workspace, the folder
// linked where it is, the assistant, the workflow, and one assessment the user
// starts. Every pass begins from saved state, so a repeated click, a second
// offer about the same folder, or a pass after a restart continues what is
// there instead of making it again. It runs nothing in the project, calls no
// model and writes nothing into the folder.
func (h *folderSetupHost) softwareRun(ctx context.Context, req personalassistant.FolderSetupRequest) error {
	b := h.builder
	intent := req.Plan.Intent.Software
	row, builtin := softwareRow(req.Offer)
	if !h.SoftwareSetupAvailable() || !builtin || intent == nil || intent.Validate() != nil || req.Software == nil || req.Update == nil {
		return errSetupUnavailable
	}
	progress := newSoftwareProgress(ctx, req)
	changed := func(kind string, cause error) error {
		logger.Warn("Software project setup stopped: saved state differs from the review", logger.Fields{
			"offer_id": req.Offer.ID, "step": kind, "error": cause.Error(),
		})
		return progress.stop(kind, personalassistant.FolderStopPlanChanged)
	}
	failed := func(kind string, cause error) error {
		logger.Warn("Software project setup stopped", logger.Fields{"offer_id": req.Offer.ID, "step": kind, "error": cause.Error()})
		return progress.stop(kind, personalassistant.FolderStopFailed)
	}

	// The folder the service holds must be the directory that was reviewed.
	if strings.TrimSpace(req.Path) == "" || req.Software.Identity != intent.SubjectIdentity || req.Offer.Subject.Key != intent.SubjectKey {
		return changed("", errSoftwareChanged)
	}
	release := claimSoftwareProject(req.UserID, intent.SubjectIdentity)
	defer release()

	target, err := h.softwareTargetFor(req, intent)
	if errors.Is(err, errSoftwareChanged) {
		return changed(personalassistant.FolderPlanWorkspace, err)
	}
	if err != nil {
		return failed(personalassistant.FolderPlanWorkspace, err)
	}
	if target.adopted {
		return h.softwareAdopt(progress, target)
	}

	// 1. The workspace.
	if err := progress.working(personalassistant.FolderPlanWorkspace); err != nil {
		return err
	}
	if target.workspaceID == "" {
		review, stop, err := h.softwareRoster(row, intent)
		if stop != "" {
			if stop == personalassistant.FolderStopNeedsModel {
				progress.line(personalassistant.FolderPlanWorkspace).State = personalassistant.FolderLineWaiting
				progress.line(personalassistant.FolderPlanAgents).Detail = "It has no model yet. Nothing is created until you choose one."
				return progress.stop(personalassistant.FolderPlanAgents, stop)
			}
			return changed(personalassistant.FolderPlanAgents, err)
		}
		parentID := ""
		if destination := req.Plan.Destination; destination != nil && destination.Status == "existing" {
			if err := h.ValidateSetupDestination(ctx, req); err != nil {
				return changed(personalassistant.FolderPlanWorkspace, err)
			}
			parentID = destination.WorkspaceID
		}
		// The brief the user gave the hire is the new workspace's goal, in
		// place of the blueprint's.
		id, err := b.sessionHandler.CreateFolderOfferWorkspace(ctx, sessionhttp.FolderOfferWorkspaceRequest{
			Name: intent.WorkspaceName, TemplateID: intent.Template, OfferID: req.Offer.ID, ParentID: parentID, Roster: &review,
			Mission: req.Brief,
		})
		if errors.Is(err, sessionhttp.ErrFolderOfferRosterChanged) {
			return changed(personalassistant.FolderPlanAgents, err)
		}
		if err != nil {
			return failed(personalassistant.FolderPlanWorkspace, err)
		}
		target = softwareTarget{workspaceID: id, ours: true}
	}
	ws, err := h.softwareWorkspace(target.workspaceID, req.UserID)
	if err != nil {
		return changed(personalassistant.FolderPlanWorkspace, err)
	}
	progress.done(personalassistant.FolderPlanWorkspace)

	// 2. The folder, linked where it is, and the record of which folder it was.
	if err := progress.working(personalassistant.FolderPlanFolder); err != nil {
		return err
	}
	if !softwareDirectoryIs(req.Path, intent.SubjectIdentity) {
		return progress.stop(personalassistant.FolderPlanFolder, personalassistant.FolderStopNeedsPick)
	}
	binding := workspace.SoftwareProjectBinding{
		DirectoryIdentity: intent.SubjectIdentity, Recipe: req.Plan.Intent.Recipe, RecipeVersion: req.Plan.Intent.RecipeVersion,
		BoundAt: time.Now().UTC(),
	}
	if review := req.Software.Review; review != nil && review.Git != nil {
		binding.RepositoryKey, binding.Remote = review.Git.RepositoryKey, review.Git.Remote
	}
	linker := folderWorkspaceLinker{files: b.workspaceFileStore, sessions: b.sessionStore, tasks: b.sessionHandler}
	_, err = linker.link(ctx, personalassistant.FolderLinkRequest{
		UserID: req.UserID, WorkspaceID: ws.ID, OfferID: req.Offer.ID,
		Name: strings.TrimSpace(req.Offer.Subject.Name), Path: req.Path, Shape: folderdigest.Shape(req.Offer.Subject.Shape),
	}, folderLinkOptions{
		reviewedWorkspace: !target.ours, noFirstTask: true,
		record: func(shared map[string]any, directoryID string) (map[string]any, error) {
			workflow := ""
			if target.ours {
				workflow = intent.Workflow
			}
			return recordSoftwareProject(shared, directoryID, binding, workflow)
		},
	})
	if errors.Is(err, personalassistant.ErrFolderWorkspaceRefused) || errors.Is(err, personalassistant.ErrFolderWorkspaceNotFound) {
		return changed(personalassistant.FolderPlanFolder, err)
	}
	if err != nil {
		return failed(personalassistant.FolderPlanFolder, err)
	}
	progress.done(personalassistant.FolderPlanFolder)

	// 3. The assistant.
	if err := progress.working(personalassistant.FolderPlanAgents); err != nil {
		return err
	}
	if !target.ours && intent.AssistantAction != personalassistant.FolderSoftwareKeep {
		review, stop, rosterErr := h.softwareRoster(row, intent)
		entry := ""
		if current, readErr := h.softwareWorkspace(ws.ID, req.UserID); readErr == nil {
			entry = strings.TrimSpace(current.EntryAgentName())
		}
		// An assistant this setup attached in an earlier pass is already the
		// entry agent; the roster then reads "reuse" and is not a change.
		if entry == "" && stop != "" {
			if stop == personalassistant.FolderStopNeedsModel {
				progress.line(personalassistant.FolderPlanAgents).Detail = "It has no model yet. Choose one, then continue."
				return progress.stop(personalassistant.FolderPlanAgents, stop)
			}
			return changed(personalassistant.FolderPlanAgents, rosterErr)
		}
		if entry != "" {
			review = sessionhttp.FolderOfferRosterReview{Assistant: intent.Assistant, Action: intent.AssistantAction}
		}
		_, err := b.sessionHandler.AttachFolderOfferAssistant(ctx, ws.ID, row.Blueprint.BlueprintID, review)
		if errors.Is(err, sessionhttp.ErrFolderOfferRosterChanged) || errors.Is(err, sessionhttp.ErrFolderOfferRoster) {
			return changed(personalassistant.FolderPlanAgents, err)
		}
		if err != nil {
			return failed(personalassistant.FolderPlanAgents, err)
		}
	}
	if ws, err = h.softwareWorkspace(ws.ID, req.UserID); err != nil {
		return failed(personalassistant.FolderPlanAgents, err)
	}
	if entry := strings.TrimSpace(ws.EntryAgentName()); !strings.EqualFold(entry, strings.TrimSpace(intent.Assistant)) {
		return changed(personalassistant.FolderPlanAgents, errSoftwareChanged)
	}
	progress.done(personalassistant.FolderPlanAgents)

	// 4. The workflow. A new workspace's planning-first settings were written,
	// and checked against the reviewed ones, in the same save as the folder
	// link; a reused workspace keeps the user's own. Nothing more is changed.
	progress.done(personalassistant.FolderPlanMode)

	// 5. One assessment, seeded and left for the user to start.
	if err := progress.working(personalassistant.FolderPlanTask); err != nil {
		return err
	}
	if intent.Assessment == personalassistant.FolderSoftwareSeed {
		description, details := personalassistant.FolderSoftwareAssessmentTask()
		if _, _, err := b.sessionHandler.SeedFolderOfferAssessment(ws.ID, sessionhttp.FolderOfferAssessment{
			Recipe: workspace.FirstTaskRecipeSoftwareAssessment, RecipeVersion: 1, Description: description, Details: details,
		}); err != nil {
			return failed(personalassistant.FolderPlanTask, err)
		}
	}
	progress.done(personalassistant.FolderPlanTask)
	return progress.send(personalassistant.FolderSetupDone, "")
}

// softwareAdopt finishes a run whose folder another setup already covered. The
// lines say so; nothing is created and nothing in that workspace is changed.
func (h *folderSetupHost) softwareAdopt(progress *softwareProgress, target softwareTarget) error {
	ws, err := h.builder.workspaceFileStore.Get(target.workspaceID)
	if err != nil || ws == nil {
		return progress.stop(personalassistant.FolderPlanWorkspace, personalassistant.FolderStopFailed)
	}
	for i := range progress.lines {
		line := &progress.lines[i]
		line.State = personalassistant.FolderLineDone
		switch line.Kind {
		case personalassistant.FolderPlanWorkspace:
			line.Name, line.Detail = "Uses your existing workspace "+ws.Name, "This folder was already set up. No second workspace was made."
		case personalassistant.FolderPlanFolder:
			line.Detail = "Already linked where it is. Nothing was changed."
		case personalassistant.FolderPlanAgents:
			line.Name, line.Detail = "Keeps that workspace's assistant", "Nothing was added or changed."
		case personalassistant.FolderPlanMode:
			line.Name, line.Detail = "Keeps that workspace's settings", "Setup ran no code and changed nothing in the project."
		case personalassistant.FolderPlanTask:
			line.Name, line.Detail = "Keeps what that workspace already has", "Nothing was queued and nothing was started."
		}
	}
	return progress.send(personalassistant.FolderSetupDone, "")
}

// softwareRoster re-reads the assistant the review promised, before anything is
// created. stop is empty when the roster is the reviewed one; needs_model when
// the assistant still has no model to run on; plan_changed otherwise. A model
// the user chose after a review that had none is the repair that review asked
// for, not a change to it.
func (h *folderSetupHost) softwareRoster(row folderdigest.CapabilityRow, intent *personalassistant.FolderSoftwareIntent) (sessionhttp.FolderOfferRosterReview, string, error) {
	roster, err := h.builder.sessionHandler.FolderOfferRoster(row.Blueprint.BlueprintID, intent.Assistant)
	if err != nil {
		return sessionhttp.FolderOfferRosterReview{}, personalassistant.FolderStopPlanChanged, err
	}
	review := sessionhttp.FolderOfferRosterReview{PlanRevision: roster.PlanRevision, Assistant: roster.Assistant, Action: roster.Action}
	if roster.Assistant != intent.Assistant || roster.Action != intent.AssistantAction ||
		(intent.TemplateRevision != "" && roster.TemplateRevision != intent.TemplateRevision) {
		return review, personalassistant.FolderStopPlanChanged, errSoftwareChanged
	}
	if strings.TrimSpace(roster.Model) == "" {
		return review, personalassistant.FolderStopNeedsModel, nil
	}
	if intent.Model != "" && (roster.PlanRevision != intent.RosterRevision || roster.Model != intent.Model || roster.Provider != intent.Provider) {
		return review, personalassistant.FolderStopPlanChanged, errSoftwareChanged
	}
	return review, "", nil
}

// softwareWorkspace reads the user's own ordinary, active workspace from its
// saved record (workspace.json), which is the copy every other reader is
// hydrated from and the only one that carries where the workspace came from.
func (h *folderSetupHost) softwareWorkspace(workspaceID, userID string) (*workspace.Workspace, error) {
	ws, err := h.builder.workspaceFileStore.Get(workspaceID)
	if err != nil || ws == nil || ws.Kind == "group" || ws.GetStatus() != workspace.StatusActive ||
		!folderWorkspaceOwnedBy(ws, userID) || strings.TrimSpace(ws.FolderSlug) == "" {
		return nil, errSoftwareChanged
	}
	return ws, nil
}

// softwareDirectoryIs reports whether the directory at path is still the one
// the review was about.
func softwareDirectoryIs(path, identity string) bool {
	info, err := os.Lstat(path) // #nosec G304 G703 -- the canonical path of a server-held selection, never browser input
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	witness, err := projectlibrary.DirectoryIdentity(info)
	return err == nil && witness == identity
}

// recordSoftwareProject writes what a software setup records beside the link:
// which directory was confirmed and, for a workspace this setup created, the
// planning-first workflow (workflow is its reviewed signature; empty leaves the
// workspace's settings alone). It is applied to both copies of the workspace
// and again on a replay, so it keeps what an earlier pass already recorded: the
// time of the first binding, and settings the user may have changed since.
//
// The workflow is checked as it is written: the settings must produce the
// effective policy that was reviewed, or nothing is recorded at all.
func recordSoftwareProject(shared map[string]any, directoryID string, binding workspace.SoftwareProjectBinding, workflow string) (map[string]any, error) {
	if shared == nil {
		shared = map[string]any{}
	}
	binding.DirectoryID = directoryID
	previous, err := workspace.GetSoftwareProjectBinding(shared)
	if err != nil {
		return nil, err
	}
	first := previous == nil
	if previous != nil {
		if previous.DirectoryID != directoryID || previous.DirectoryIdentity != binding.DirectoryIdentity {
			return nil, errSoftwareChanged
		}
		binding.BoundAt = previous.BoundAt
	}
	if binding.Validate() != nil {
		// A remote that does not fit the record is left out; the binding is
		// about the directory, and the remote is only ever a label.
		binding.Remote, binding.RepositoryKey = "", ""
	}
	if first && workflow != "" {
		shared, _ = workspacesettings.ApplyPatch(shared, softwareWorkflowPatch())
		if signature, ok := softwareWorkflowOf(shared); !ok || signature != workflow {
			return nil, errSoftwareChanged
		}
	}
	if err := workspace.SetSoftwareProjectBinding(shared, binding); err != nil {
		return nil, err
	}
	return shared, nil
}

// softwareProjectVerifier re-reads a finished software setup from saved state.
// It answers only for the user's own active workspace whose primary linked
// folder is this exact directory and which records that directory's witness;
// the receipt it returns is what that workspace has, not what the plan said.
func (h *folderSetupHost) softwareProject(ctx context.Context, userID string, offer personalassistant.FolderOffer, folderPath, identity string) (personalassistant.FolderCreateResult, error) {
	b := h.builder
	if b == nil || b.workspaceFileStore == nil || b.workspaceStore == nil || offer.Setup == nil || offer.Setup.Intent.Software == nil {
		return personalassistant.FolderCreateResult{}, errSetupUnavailable
	}
	intent := offer.Setup.Intent.Software
	if identity != intent.SubjectIdentity {
		return personalassistant.FolderCreateResult{}, errSoftwareChanged
	}
	linked, err := softwareExistingProjects(b.workspaceFileStore, userID, folderPath, identity)
	if err != nil {
		return personalassistant.FolderCreateResult{}, err
	}
	if len(linked) != 1 || !linked[0].Software || linked[0].Replaced {
		return personalassistant.FolderCreateResult{}, errSoftwareChanged
	}
	ws, err := h.softwareWorkspace(linked[0].WorkspaceID, userID)
	if err != nil {
		return personalassistant.FolderCreateResult{}, err
	}
	if intent.Outcome == personalassistant.FolderSoftwareReuse && ws.ID != intent.WorkspaceID {
		return personalassistant.FolderCreateResult{}, errSoftwareChanged
	}
	folder, bound, err := workspace.SoftwareProjectFolder(ws, projectlibrary.DirectoryIdentity)
	if !bound || err != nil || !sameCanonicalDirectory(folder, folderPath) {
		return personalassistant.FolderCreateResult{}, errSoftwareChanged
	}
	created, _ := ws.SharedData[projecttemplates.FolderOfferIDKey].(string)
	ours := strings.TrimSpace(created) == offer.ID && intent.Outcome == personalassistant.FolderSoftwareCreate
	return personalassistant.FolderCreateResult{
		WorkspaceID: ws.ID, Route: "/workspaces/" + ws.FolderSlug, Created: ours,
		Receipt: h.softwareReceipt(ctx, ws, intent, ours),
	}, nil
}

// softwareReceipt lists what the workspace has now. Created and reused are told
// apart by what this setup did; everything else is read from the workspace.
func (h *folderSetupHost) softwareReceipt(ctx context.Context, ws *workspace.Workspace, intent *personalassistant.FolderSoftwareIntent, ours bool) []personalassistant.FolderReceiptRow {
	b := h.builder
	rows := []personalassistant.FolderReceiptRow{{
		Kind: "workspace", Name: ws.Name, Route: "/workspaces/" + ws.FolderSlug, Detail: "created",
	}}
	if !ours {
		rows[0].Detail = "your existing workspace; no second one was made"
	}
	if primary, _ := ws.SharedData[projecttemplates.PrimaryDirectoryIDKey].(string); strings.TrimSpace(primary) != "" {
		if ref, err := ws.GetDirectoryReference(primary); err == nil && ref != nil {
			rows = append(rows, personalassistant.FolderReceiptRow{Kind: "folder", Name: ref.Name, Detail: "linked where it is, read-only"})
		}
	}
	if ours {
		if provenance := ws.GetTemplateProvenance(); provenance != nil && provenance.TemplateName != "" {
			detail := "its settings were changed after setup"
			if _, ok := softwareWorkflowOf(ws.SharedData); ok {
				detail = "planning first; approved work waits for you, and planning documents stay in Ori"
			}
			rows = append(rows, personalassistant.FolderReceiptRow{Kind: "blueprint", Name: provenance.TemplateName, Detail: detail})
		}
	}
	if entry := strings.TrimSpace(ws.EntryAgentName()); entry != "" {
		// This setup put an assistant in place only in a workspace it created or
		// was reviewed to complete; in any other it found one and left it.
		detail := "this workspace's assistant, kept as it is"
		if acted := ours || intent.Outcome == personalassistant.FolderSoftwareReuse; acted && strings.EqualFold(entry, strings.TrimSpace(intent.Assistant)) {
			switch intent.AssistantAction {
			case personalassistant.FolderSoftwareCreate:
				detail = "development assistant, added"
			case personalassistant.FolderSoftwareReuse:
				detail = "your saved agent, used as it is"
			}
		}
		if _, model, found := b.sessionHandler.SavedAgentModel(entry); found && strings.TrimSpace(model) == "" {
			detail += "; it has no model yet"
		}
		rows = append(rows, personalassistant.FolderReceiptRow{Kind: "agent", Name: entry, Detail: detail})
	} else {
		rows = append(rows, personalassistant.FolderReceiptRow{Kind: "agent", Name: "No assistant yet", Detail: "open the workspace to add one"})
	}
	if view, ok := b.folderFirstTaskView(ctx, ws.ID, b.assistantModelConfigured(ctx)); ok {
		// The row says where the task stands: one that cannot start yet says
		// what it waits for, never that it is ready.
		rows = append(rows, personalassistant.FolderReceiptRow{Kind: "task", Name: view.Description, Detail: view.Detail})
	}
	return rows
}

// SoftwareSetupAvailable says whether this host can carry out a reviewed
// software setup and then read it back.
func (h *folderSetupHost) SoftwareSetupAvailable() bool {
	if h == nil || h.builder == nil {
		return false
	}
	b := h.builder
	_, workflow := softwareWorkflowSignature()
	return workflow && b.sessionHandler != nil && b.workspaceFileStore != nil && b.workspaceStore != nil && b.sessionStore != nil
}
