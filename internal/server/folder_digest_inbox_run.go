package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/assistantsetup"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// inboxNamespace scopes the ids an inbox run derives from its run id.
var inboxNamespace = uuid.MustParse("6b1f0d8e-4f7a-4c3e-9a51-2d7c1e0b9f42")

// inboxOperations are every id one inbox run acts under. They are derived from
// the run id, which is saved on the offer before the first write, so a repeated
// click or a pass after a restart acts under the same ids: the reviewed creator
// observes the workspace it made, the grant replays its receipt, and the scan
// returns its recorded result instead of scanning again.
type inboxOperations struct {
	run, workspace, profile, create, folder, monitoring, scan string
}

// scanAttempt is the scan operation of one attempt at the first batch: the
// run's own scan first, then one per Retry from the report.
func (ids inboxOperations) scanAttempt(attempt int) string {
	if attempt <= 0 {
		return ids.scan
	}
	return fmt.Sprintf("%s-retry-%d", ids.scan, attempt)
}

func inboxOperationIDs(runID string) inboxOperations {
	derive := func(part string) string { return uuid.NewSHA1(inboxNamespace, []byte(runID+":"+part)).String() }
	return inboxOperations{
		run: runID, workspace: derive("workspace"), profile: derive("profile"),
		create: runID + ":create", folder: runID + ":folder", monitoring: runID + ":monitoring", scan: runID + ":scan",
	}
}

// errInboxChanged means saved state is no longer what the review described.
var errInboxChanged = errors.New("inbox setup: saved state differs from the review")

// inboxRun carries out a reviewed inbox-folder setup, in the order the card's
// lines show it: File Curator and the File Janitor workspace, the folder granted
// where it is (metadata only, paused, Filed/ created), the manual-scan choice,
// and one metadata-only scan as the first look. Before any write it refuses a
// folder another File Janitor already tidies and a root File Janitor refuses.
// Every pass starts from saved state. It calls no model and moves no file.
func (h *folderSetupHost) inboxRun(ctx context.Context, req personalassistant.FolderSetupRequest) error {
	deps, available := h.inboxDeps()
	intent := req.Plan.Intent.Inbox
	row, inbox := inboxRow(req.Offer)
	if !available || !inbox || intent == nil || intent.Validate() != nil || req.Update == nil ||
		req.Plan.Intent.Recipe != row.Builtin.Recipe || req.Plan.Intent.RecipeVersion != row.Builtin.Version {
		return errSetupUnavailable
	}
	progress := newSoftwareProgress(ctx, req)
	changed := func(kind string, cause error) error {
		logger.Warn("Inbox folder setup stopped: saved state differs from the review", logger.Fields{
			"offer_id": req.Offer.ID, "step": kind, "error": cause.Error(),
		})
		return progress.stop(kind, personalassistant.FolderStopPlanChanged)
	}
	failed := func(kind string, cause error) error {
		logger.Warn("Inbox folder setup stopped", logger.Fields{"offer_id": req.Offer.ID, "step": kind, "error": cause.Error()})
		return progress.stop(kind, personalassistant.FolderStopFailed)
	}
	// A refusal is said on the line it stops, in File Janitor's own words.
	refused := func(kind, message string) error {
		progress.line(kind).Detail = message
		return progress.stop(kind, personalassistant.FolderStopPlanChanged)
	}

	// The folder the service holds must be the directory that was reviewed.
	if strings.TrimSpace(req.Path) == "" || req.Offer.Subject.Key != intent.SubjectKey {
		return changed("", errInboxChanged)
	}
	if !softwareDirectoryIs(req.Path, intent.SubjectIdentity) {
		return progress.stop(personalassistant.FolderPlanFolder, personalassistant.FolderStopNeedsPick)
	}
	release := claimSoftwareProject(req.UserID, intent.SubjectIdentity)
	defer release()

	// The run's identity is saved before anything is written.
	runID := ""
	if req.Offer.Setup != nil {
		runID = strings.TrimSpace(req.Offer.Setup.RunID)
	}
	if runID == "" {
		runID = uuid.NewString()
		if err := req.Update(ctx, personalassistant.FolderSetupUpdate{
			Lines: append([]personalassistant.FolderPlanLine(nil), progress.lines...), Status: personalassistant.FolderSetupRunning, RunID: runID,
		}); err != nil {
			return err
		}
	}
	ids := inboxOperationIDs(runID)

	// 0. Refusals, before any write. A File Janitor workspace for this folder (or
	// one overlapping it) already tidies it; a root File Janitor refuses is not
	// one it can be given.
	if owner, found := deps.janitor.FolderOwner(req.Path); found && owner != nil && owner.WorkspaceID != ids.workspace {
		return refused(personalassistant.FolderPlanWorkspace, "File Curator already tidies this folder in another workspace. Nothing was created.")
	}
	if _, err := deps.janitor.ReviewSelectedSetup(ids.workspace, req.Path, nil); err != nil {
		return refused(personalassistant.FolderPlanFolder, inboxRefusal(err))
	}

	// 1. File Curator and the workspace, through the reviewed creator. The brief
	// is the workspace goal; a saved File Curator is assigned, never made twice.
	progress.line(personalassistant.FolderPlanAgents).State = personalassistant.FolderLineWorking
	if err := progress.working(personalassistant.FolderPlanWorkspace); err != nil {
		return err
	}
	_, err := deps.team.CreateReviewedFileJanitor(ctx, sessionhttp.ReviewedTemplateCreationRequest{
		Name: intent.WorkspaceName, Mission: req.Brief, Plan: inboxTeamPlan(*intent),
		Descriptor: sessionhttp.AssistantSetupCreationDescriptor{
			OwnerUserID: req.UserID, RunID: runID, OperationID: ids.create, ReviewDigest: req.Plan.Digest,
			WorkspaceID: ids.workspace, ProfileProvenanceID: ids.profile, ConfigDigest: intent.Agent.ConfigDigest,
		},
	})
	if errors.Is(err, sessionhttp.ErrReviewedPlanChanged) || errors.Is(err, sessionhttp.ErrReviewedPlanUnsupported) {
		progress.line(personalassistant.FolderPlanWorkspace).State = personalassistant.FolderLineWaiting
		return changed(personalassistant.FolderPlanAgents, err)
	}
	if err != nil {
		progress.line(personalassistant.FolderPlanAgents).State = personalassistant.FolderLineWaiting
		return failed(personalassistant.FolderPlanWorkspace, err)
	}
	if _, err := h.inboxWorkspace(deps, ids, req.UserID); err != nil {
		return changed(personalassistant.FolderPlanWorkspace, err)
	}
	progress.done(personalassistant.FolderPlanAgents)
	progress.done(personalassistant.FolderPlanWorkspace)

	// 2. The folder, granted where it is: metadata only, paused, Filed/ inside.
	if err := progress.working(personalassistant.FolderPlanFolder); err != nil {
		return err
	}
	paused := true
	status, err := deps.janitor.ConfirmSetup(filejanitor.SetupRequest{
		WorkspaceID: ids.workspace, Path: req.Path, Paused: &paused,
		Operation: &filejanitor.FolderGrantOperation{RunID: runID, OperationID: ids.folder},
	})
	if err != nil {
		var setupErr *filejanitor.SetupError
		if errors.As(err, &setupErr) && setupErr.Code == filejanitor.CodeFolderConflict {
			return refused(personalassistant.FolderPlanFolder, inboxRefusal(err))
		}
		return failed(personalassistant.FolderPlanFolder, err)
	}
	if !status.Settings.IsSetUp() || status.Settings.ContentMode != filejanitor.ContentModeMetadataOnly {
		return changed(personalassistant.FolderPlanFolder, errInboxChanged)
	}
	progress.done(personalassistant.FolderPlanFolder)

	// 3. Monitoring. Unticked, nothing is approved or switched on: the folder is
	// set to explicit manual scans, which is complete File Janitor setup.
	if err := progress.working(personalassistant.FolderPlanTask); err != nil {
		return err
	}
	// Ticked, the reviewed approval is recorded and watching and the daily scan
	// are switched on with the first scan (the preparation step below); unticked,
	// approval stays unset.
	monitoring := req.Monitoring
	if monitoring {
		progress.line(personalassistant.FolderPlanMode).State = personalassistant.FolderLineWorking
	}
	if !monitoring && !status.Settings.ManualScansOnly() {
		if !status.Settings.AutomationApprovedAt.IsZero() {
			return changed(personalassistant.FolderPlanTask, errInboxChanged)
		}
		if status, err = deps.janitor.RecordManualSetupDecision(ids.workspace, status.Settings.RootID, status.Settings.UpdatedAt); err != nil {
			return failed(personalassistant.FolderPlanTask, err)
		}
	}

	// 4. The first look: one metadata-only scan, prepared through the assisted
	// setup's own step under this run's operation ids. A scan that does not
	// complete does not undo the hire: the workspace, File Curator and the link
	// are kept, the first batch is reported as not complete, and its Retry runs
	// this step again on its own.
	prepared, err := h.inboxPrepareBatch(ctx, deps, req.UserID, ids, ids.scan, monitoring)
	if err != nil {
		logger.Warn("Inbox folder setup: the first scan did not complete", logger.Fields{"offer_id": req.Offer.ID, "error": err.Error()})
		task := progress.line(personalassistant.FolderPlanTask)
		task.State, task.Detail = personalassistant.FolderLineFailed, inboxScanIncomplete
		if monitoring {
			progress.line(personalassistant.FolderPlanMode).State = personalassistant.FolderLineFailed
		}
		return progress.send(personalassistant.FolderSetupDone, "")
	}
	if monitoring {
		progress.done(personalassistant.FolderPlanMode)
	}
	if outcome, known := inboxBatchOutcome(prepared.ScanOutcome); known {
		progress.line(personalassistant.FolderPlanTask).Detail = inboxBatchDetail(outcome, prepared.EligibleCount, -1)
	}
	progress.done(personalassistant.FolderPlanTask)
	return progress.send(personalassistant.FolderSetupDone, "")
}

// inboxScanIncomplete is the first-look line of a run whose scan did not
// complete.
const inboxScanIncomplete = "The first scan did not complete. The workspace, File Curator and the folder link are kept; Retry it from Needs you."

// inboxPrepareBatch is FR15 step 4: one metadata-only scan under scanOperation,
// through the assisted setup's own preparation step, with the monitoring
// choice the setup made. The setup's run and its report's Retry both use it,
// each under its own scan operation, so a replay of either returns its
// recorded result instead of scanning again.
func (h *folderSetupHost) inboxPrepareBatch(ctx context.Context, deps inboxCollaborators, userID string, ids inboxOperations, scanOperation string, monitoring bool) (assistantsetup.PrepareReviewResult, error) {
	facts, err := deps.review.ReviewMonitoring(ctx, userID, ids.workspace)
	if err != nil {
		return assistantsetup.PrepareReviewResult{}, err
	}
	return deps.review.prepareFirstReview(ctx, assistantsetup.PrepareReviewRequest{
		OwnerUserID: userID, RunID: ids.run, WorkspaceID: ids.workspace,
		MonitoringOperation: ids.monitoring, ScanOperation: scanOperation, Review: inboxMonitoringReview(ids.run, facts),
	}, monitoring)
}

// inboxTeamPlan is the reviewed team plan the intent promised, in the terms the
// strict creator compares.
func inboxTeamPlan(intent personalassistant.FolderInboxIntent) sessionhttp.ReviewedTemplateCreationPlan {
	agent := intent.Agent
	return sessionhttp.ReviewedTemplateCreationPlan{
		BlueprintID: intent.BlueprintID, BlueprintVersion: intent.BlueprintVersion,
		BlueprintDigest: intent.BlueprintDigest, PlanRevision: intent.PlanRevision,
		Roles: []sessionhttp.ReviewedTemplateRole{{
			RoleID: agent.RoleID, Name: agent.Name, Action: agent.Action, Provider: agent.Provider, Model: agent.Model,
			ModelConfigured: agent.ModelConfigured, ConfigDigest: agent.ConfigDigest, Warning: agent.Warning,
		}},
	}
}

// inboxMonitoringReview is the monitoring terms read for this run, bound to it.
// Without monitoring nothing is approved under them; they only pin the folder,
// privacy and schedule the scan is prepared on.
func inboxMonitoringReview(runID string, facts assistantsetup.MonitoringFacts) assistantsetup.MonitoringReview {
	sum := sha256.Sum256([]byte(strings.Join([]string{runID, facts.RootGenerationID, facts.SettingsRevision, facts.AuthorityRevision}, "\x00")))
	return assistantsetup.MonitoringReview{
		Revision: hex.EncodeToString(sum[:]), RootGenerationID: facts.RootGenerationID,
		SettingsRevision: facts.SettingsRevision, AuthorityRevision: facts.AuthorityRevision,
		PrivacyMode: facts.PrivacyMode, WatchEvents: append([]string(nil), facts.WatchEvents...),
		DebounceSeconds: facts.DebounceSeconds, SettlingSeconds: facts.SettlingSeconds,
		DailyScanLocalTime: facts.DailyScanLocalTime, Timezone: facts.Timezone, ExcludesFiled: facts.ExcludesFiled, NothingMoves: true,
		WasPaused: facts.WasPaused, WasApproved: facts.WasApproved, WasMonitoringActive: facts.WasMonitoringActive,
	}
}

// inboxRefusal is File Janitor's own message for a folder it will not take,
// or a plain one when it gave none.
func inboxRefusal(err error) string {
	var setupErr *filejanitor.SetupError
	if errors.As(err, &setupErr) && strings.TrimSpace(setupErr.Message) != "" {
		return strings.TrimSpace(setupErr.Message)
	}
	return "File Janitor cannot use this folder. Nothing was created."
}

// inboxBatchOutcome maps a File Janitor scan outcome to a first batch's.
func inboxBatchOutcome(scanOutcome string) (string, bool) {
	switch scanOutcome {
	case filejanitor.ScanOutcomeBatch:
		return personalassistant.FolderFirstBatchReady, true
	case filejanitor.ScanOutcomeNoEligible:
		return personalassistant.FolderFirstBatchEmpty, true
	}
	return "", false
}

// inboxBatchDetail words a first batch's counts ("23 proposed · 4 need
// review"); needsReview < 0 leaves that part out. Empty for an unknown outcome.
func inboxBatchDetail(outcome string, eligible, needsReview int) string {
	switch outcome {
	case personalassistant.FolderFirstBatchEmpty:
		return "Nothing to file right now."
	case personalassistant.FolderFirstBatchFailed:
		return "the first scan did not complete; Retry it from Needs you"
	case personalassistant.FolderFirstBatchReady:
		detail := fmt.Sprintf("%d proposed", eligible)
		if needsReview >= 0 {
			detail += fmt.Sprintf(" · %d need review", needsReview)
		}
		return detail
	}
	return ""
}

// inboxWorkspace reads the user's own active File Janitor workspace this run
// created, from its saved record, by the run's own markers.
func (h *folderSetupHost) inboxWorkspace(deps inboxCollaborators, ids inboxOperations, userID string) (*workspace.Workspace, error) {
	ws, err := deps.workspace(ids.workspace)
	if err != nil || ws == nil || ws.Kind == "group" || ws.GetStatus() != workspace.StatusActive || !folderWorkspaceOwnedBy(ws, userID) ||
		strings.TrimSpace(ws.FolderSlug) == "" || !ws.HasInstalledCapability(workspace.CapabilityFileJanitor) {
		return nil, errInboxChanged
	}
	provenance := ws.GetTemplateProvenance()
	if provenance == nil || provenance.AssistantSetup == nil || provenance.AssistantSetup.RunID != ids.run || provenance.AssistantSetup.OperationID != ids.create {
		return nil, errInboxChanged
	}
	return ws, nil
}

// inboxProject re-reads a finished inbox setup from canonical state: the
// workspace this run created, the folder File Janitor manages for it (this
// exact directory, metadata only) and the first scan's batch. The receipt is
// what those read back, not what the plan said.
func (h *folderSetupHost) inboxProject(_ context.Context, userID string, offer personalassistant.FolderOffer, folderPath, identity string) (personalassistant.FolderCreateResult, error) {
	deps, available := h.inboxDeps()
	if !available || offer.Setup == nil || offer.Setup.Intent.Inbox == nil || strings.TrimSpace(offer.Setup.RunID) == "" {
		return personalassistant.FolderCreateResult{}, errSetupUnavailable
	}
	intent := offer.Setup.Intent.Inbox
	if identity != intent.SubjectIdentity {
		return personalassistant.FolderCreateResult{}, errInboxChanged
	}
	ids := inboxOperationIDs(offer.Setup.RunID)
	ws, settings, err := h.inboxGranted(deps, ids, userID, folderPath)
	if err != nil {
		return personalassistant.FolderCreateResult{}, err
	}
	batch, err := inboxBatchFrom(deps, ws, settings, ids.scan)
	if err != nil {
		return personalassistant.FolderCreateResult{}, err
	}
	return personalassistant.FolderCreateResult{
		WorkspaceID: ws.ID, Route: "/workspaces/" + url.PathEscape(ws.FolderSlug), Created: true, FirstBatch: batch,
		Receipt: h.inboxReceipt(deps, ws, settings, intent, batch),
	}, nil
}

// inboxGranted reads the workspace a run created and the folder File Janitor
// manages for it: this exact directory, metadata only, granted under the run's
// own operation. Anything else is a setup that is no longer the reviewed one.
func (h *folderSetupHost) inboxGranted(deps inboxCollaborators, ids inboxOperations, userID, folderPath string) (*workspace.Workspace, filejanitor.JanitorSettings, error) {
	ws, err := h.inboxWorkspace(deps, ids, userID)
	if err != nil {
		return nil, filejanitor.JanitorSettings{}, err
	}
	status, err := deps.janitor.Status(ws.ID)
	settings := status.Settings
	if err != nil || !settings.IsSetUp() || !sameCanonicalDirectory(settings.RootPath, folderPath) ||
		settings.ContentMode != filejanitor.ContentModeMetadataOnly || settings.LastFolderGrant == nil || settings.LastFolderGrant.OperationID != ids.folder {
		return nil, filejanitor.JanitorSettings{}, errInboxChanged
	}
	return ws, settings, nil
}

// inboxBatchFrom reads one attempt at the first batch from File Janitor's own
// record of its scan operation: the batch with its counts, nothing to file, or
// (no record of the scan) a scan that did not complete. The route opens the
// review surface, on the batch when there is one.
func inboxBatchFrom(deps inboxCollaborators, ws *workspace.Workspace, settings filejanitor.JanitorSettings, scanOperation string) (*personalassistant.FolderFirstBatch, error) {
	review := "/workspaces/" + url.PathEscape(ws.FolderSlug) + "?panel=file-janitor"
	scan, found, err := deps.janitor.ScanOperationReceipt(ws.ID, scanOperation)
	if err != nil {
		return nil, err
	}
	if !found {
		return &personalassistant.FolderFirstBatch{Outcome: personalassistant.FolderFirstBatchFailed, Route: review}, nil
	}
	outcome, known := inboxBatchOutcome(scan.Outcome)
	if !known || scan.RootID != settings.RootID || scan.PrivacyMode != filejanitor.ContentModeMetadataOnly {
		return nil, errInboxChanged
	}
	batch := &personalassistant.FolderFirstBatch{Outcome: outcome, Eligible: scan.EligibleCount, Ineligible: scan.IneligibleCount, Route: review}
	if outcome == personalassistant.FolderFirstBatchReady && strings.TrimSpace(scan.BatchID) != "" {
		batch.BatchID = scan.BatchID
		batch.Route = review + "&batch_id=" + url.QueryEscape(scan.BatchID)
		if batches, err := deps.janitor.ListBatches(ws.ID); err == nil {
			for _, recorded := range batches {
				if recorded.ID == scan.BatchID {
					batch.NeedsReview = min(recorded.Summary.NeedsReview, batch.Eligible)
				}
			}
		}
	}
	return batch, nil
}

// inboxRetryFirstBatch is Retry on an inbox hire's report: FR15 step 4 alone,
// again, under the attempt's own scan operation, on the workspace and folder
// the setup made. The folder is the one File Janitor manages for that
// workspace, still the directory the review was about; no held selection is
// needed, so a Retry after a restart works. Nothing else is created or
// changed. It returns the attempt's batch as File Janitor recorded it; a scan
// that did not complete again is a failed batch, not an error.
func (h *folderSetupHost) inboxRetryFirstBatch(ctx context.Context, userID string, offer personalassistant.FolderOffer, attempt int) (personalassistant.FolderFirstBatch, error) {
	deps, available := h.inboxDeps()
	if !available || offer.Setup == nil || offer.Setup.Intent.Inbox == nil || strings.TrimSpace(offer.Setup.RunID) == "" || attempt < 1 {
		return personalassistant.FolderFirstBatch{}, errSetupUnavailable
	}
	ids := inboxOperationIDs(offer.Setup.RunID)
	status, err := deps.janitor.Status(ids.workspace)
	if err != nil || !softwareDirectoryIs(status.Settings.RootPath, offer.Setup.Intent.Inbox.SubjectIdentity) {
		return personalassistant.FolderFirstBatch{}, errInboxChanged
	}
	ws, settings, err := h.inboxGranted(deps, ids, userID, status.Settings.RootPath)
	if err != nil {
		return personalassistant.FolderFirstBatch{}, err
	}
	// The monitoring choice the setup made stands: a ticked watching is tried
	// again, and manual scans stay manual.
	monitoring := offer.Setup.Monitoring
	scanOperation := ids.scanAttempt(attempt)
	if _, err := h.inboxPrepareBatch(ctx, deps, userID, ids, scanOperation, monitoring); err != nil {
		logger.Warn("Inbox first batch retry did not complete", logger.Fields{"offer_id": offer.ID, "attempt": attempt, "error": err.Error()})
	}
	batch, err := inboxBatchFrom(deps, ws, settings, scanOperation)
	if err != nil {
		return personalassistant.FolderFirstBatch{}, err
	}
	return *batch, nil
}

// inboxReceipt lists what the setup made, in the order of the card's lines: the
// agent (created or assigned), the workspace, the folder link and the first
// batch with its counts. Every row is read from the workspace and File Janitor.
func (h *folderSetupHost) inboxReceipt(deps inboxCollaborators, ws *workspace.Workspace, settings filejanitor.JanitorSettings, intent *personalassistant.FolderInboxIntent, batch *personalassistant.FolderFirstBatch) []personalassistant.FolderReceiptRow {
	var rows []personalassistant.FolderReceiptRow
	for _, instance := range ws.AgentInstances {
		name := strings.TrimSpace(instance.Name)
		if name == "" || !strings.EqualFold(name, strings.TrimSpace(intent.Agent.Name)) {
			continue
		}
		detail := "file curator, added"
		if intent.Agent.Action == personalassistant.FolderSoftwareReuse {
			detail = "your saved agent, assigned as it is"
		}
		_, model, _ := deps.team.SavedAgentModel(name)
		rows = append(rows, personalassistant.FolderReceiptRow{
			Kind: "agent", Name: name, Detail: detail, Assigned: true, Model: personalassistant.FolderReceiptModel(model),
		})
		break
	}
	rows = append(rows, personalassistant.FolderReceiptRow{
		Kind: "workspace", Name: ws.Name, Route: "/workspaces/" + url.PathEscape(ws.FolderSlug), Detail: "created", Operation: "create", Placement: "standalone",
	})
	if ref, err := ws.GetDirectoryReference(settings.DirectoryReferenceID); err == nil && ref != nil && strings.TrimSpace(ref.Name) != "" {
		rows = append(rows, personalassistant.FolderReceiptRow{
			Kind: "folder", Name: ref.Name, Detail: "linked where it is; names, types, sizes and dates only; Filed is inside it",
		})
	}
	return append(rows, personalassistant.FolderReceiptRow{
		Kind: "batch", Name: "First batch", Route: batch.Route, Detail: inboxBatchDetail(batch.Outcome, batch.Eligible, batch.NeedsReview),
	})
}
