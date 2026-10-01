// Package foldersetup drives a recognized project folder through the setup
// journey on the server, so the folder card needs one consent instead of one
// review-and-confirm screen per step.
//
// How the journey is driven (read from internal/setupjourney, 2026-10-01):
//   - Read(userID, "") resolves the quest's ROOT run and creates its inert row
//     on first use. The root is used until its projection has
//     receipts.project_workspace_id; a further folder is a CHILD run created
//     with CreateOrResumeChild (needs connect_another_project on the summary).
//   - Every consequence is a review action then a commit action on one run, both
//     with IfRevision = the projection's state_revision. A review returns a typed
//     disclosure and a token; the commit repeats the SAME input with that token.
//     A commit bumps the revision, so the runner re-reads between every pair.
//   - A step is done when its StepProjection.Status is "complete"; the journey is
//     done when Lifecycle is "ready". Step actions list what is available now.
//   - The install quest is a HOST quest (ForHostQuest); the project steps belong
//     to the PLUGIN's quest (ForQuest) and exist only after the install.
package foldersetup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// fileOnlyModeID is the workspace-setup mode that grants nothing (D3). It
// mirrors the journey's own constant, which is unexported.
const fileOnlyModeID = "file_only"

// maxPasses bounds the loop: each pass completes at least one step or stops.
const maxPasses = 16

// Journey is the slice of setupjourney.Service the runner drives, scoped to one
// user and one quest. runID "" is the quest's root run.
type Journey interface {
	Read(ctx context.Context, runID string) (*setupjourney.JourneyProjection, error)
	Mutate(ctx context.Context, runID string, action setupjourney.ActionID, request setupjourney.ActionMutation) (*setupjourney.ActionResult, error)
}

// Selections mints the project-picker token for the offer's remembered folder
// and names the folder it stands for. The browser never supplies either.
// It returns ErrNeedsPick when the server no longer holds the folder's path.
type Selections interface {
	Select(ctx context.Context) (token, folder string, err error)
}

// Progress records the run's state on the offer after every change.
type Progress interface {
	Update(ctx context.Context, update Update) error
}

// Update is one snapshot of a run.
type Update = personalassistant.FolderSetupUpdate

// Config is what the user confirmed on the card.
type Config struct {
	Plan personalassistant.FolderSetupPlan
	// WorkspaceName is the name the plan shows for the new workspace.
	WorkspaceName string
	// EntryName names the project file when the user or the scan chose one.
	EntryName string
}

// Result is how a run ended. Cause is the underlying error behind a stop the
// runner did not choose itself; it is for the host's log and never shown.
type Result struct {
	Status     string
	StopReason string
	RunID      string
	Cause      error
}

// ErrNeedsPick means the folder's path is gone (the server restarted).
var ErrNeedsPick = errors.New("foldersetup: the folder must be picked again")

// ProviderPreview is where a supporting plugin (the Home provider) stands.
type ProviderPreview struct {
	// Ready means it is installed at the reviewed release and enabled.
	Ready    bool
	PluginID string
	// Version is the reviewed release an Install would put in place.
	Version string
}

// Provider installs the reviewed plugin that supplies a split blueprint's Home.
// Install takes the version Preview disclosed and refuses any other.
type Provider interface {
	Preview(ctx context.Context) (ProviderPreview, error)
	Install(ctx context.Context, reviewedVersion string) error
}

// Runner is the resumable run loop. It holds no state between calls: every pass
// re-reads the journey and performs only the steps that are still incomplete.
type Runner struct {
	// Journey is the plugin quest's journey: the project, the mode and the team.
	Journey Journey
	// OpenProject, when set, resolves that journey after the install steps have
	// run: the plugin's quest exists only once the plugin does.
	OpenProject func(ctx context.Context) (Journey, error)
	// Install is the host's install quest for the reviewed integration. Nil
	// means the integration is taken to be ready already.
	Install Journey
	// Providers installs the Home provider the plan promised. Nil means none.
	Providers  Provider
	Selections Selections
	Progress   Progress
	// NewKey returns a fresh idempotency key; nil uses random bytes.
	NewKey func() string
}

// stop ends a run on purpose with a reason the card can explain. detail is for
// the host's log only.
type stop struct {
	reason     string
	candidates []string
	detail     string
}

func (s *stop) Error() string {
	if s.detail != "" {
		return "foldersetup: stopped: " + s.reason + ": " + s.detail
	}
	return "foldersetup: stopped: " + s.reason
}

// failed is a stop the card explains only as "a step did not finish", with the
// reason it happened kept for the log.
func failed(detail string) *stop {
	return &stop{reason: personalassistant.FolderStopFailed, detail: detail}
}

// stepLines names the plan line kinds each journey step finishes.
var stepLines = map[specialist.SetupStepKind][]string{
	specialist.SetupStepProjectConnect:           {personalassistant.FolderPlanHome, personalassistant.FolderPlanWorkspace, personalassistant.FolderPlanFolder},
	specialist.SetupStepWorkspaceSetup:           {personalassistant.FolderPlanMode},
	specialist.SetupStepAssistantProgramStaffing: {personalassistant.FolderPlanAgents},
}

type run struct {
	*Runner
	cfg   Config
	lines []personalassistant.FolderPlanLine
	// journey is the plugin quest being driven; runID is its run.
	journey   Journey
	runID     string
	entryName string
}

// Run drives the journey to the end or to the first thing that needs the user.
func (r *Runner) Run(ctx context.Context, cfg Config) (Result, error) {
	if r == nil || (r.Journey == nil && r.OpenProject == nil) || r.Selections == nil || r.Progress == nil {
		return Result{}, errors.New("foldersetup: runner is not wired")
	}
	state := &run{Runner: r, cfg: cfg, entryName: cfg.EntryName}
	state.lines = append([]personalassistant.FolderPlanLine(nil), cfg.Plan.Lines...)
	for i := range state.lines {
		state.lines[i].State = personalassistant.FolderLineWaiting
	}
	err := state.drive(ctx)
	var halt *stop
	switch {
	case err == nil:
		state.setKind(personalassistant.FolderPlanTask, personalassistant.FolderLineDone)
		return state.finish(ctx, personalassistant.FolderSetupDone, "", nil, nil)
	case errors.As(err, &halt):
		var cause error
		if halt.detail != "" {
			cause = halt
		}
		return state.finish(ctx, personalassistant.FolderSetupStopped, halt.reason, halt.candidates, cause)
	case errors.Is(err, ErrNeedsPick):
		return state.finish(ctx, personalassistant.FolderSetupStopped, personalassistant.FolderStopNeedsPick, nil, nil)
	case ctx.Err() != nil:
		return state.finish(ctx, personalassistant.FolderSetupStopped, personalassistant.FolderStopInterrupted, nil, err)
	default:
		return state.finish(ctx, personalassistant.FolderSetupStopped, failureReason(err), nil, err)
	}
}

func (s *run) finish(ctx context.Context, status, reason string, candidates []string, cause error) (Result, error) {
	if status == personalassistant.FolderSetupStopped {
		// The line that was in progress is the one that did not finish.
		for i := range s.lines {
			if s.lines[i].State == personalassistant.FolderLineWorking {
				s.lines[i].State = personalassistant.FolderLineFailed
			}
		}
	}
	// A cancelled request must still be able to record why it stopped.
	recordCtx := context.WithoutCancel(ctx)
	err := s.record(recordCtx, status, reason, candidates)
	return Result{Status: status, StopReason: reason, RunID: s.runID, Cause: cause}, err
}

func (s *run) record(ctx context.Context, status, reason string, candidates []string) error {
	return s.Progress.Update(ctx, Update{
		Lines: append([]personalassistant.FolderPlanLine(nil), s.lines...), Status: status,
		StopReason: reason, RunID: s.runID, EntryName: s.entryName, EntryCandidates: candidates,
	})
}

func (s *run) setKind(kind, state string) {
	for i := range s.lines {
		if s.lines[i].Kind == kind {
			s.lines[i].State = state
		}
	}
}

func (s *run) setKinds(kinds []string, state string) {
	for _, kind := range kinds {
		s.setKind(kind, state)
	}
}

func (s *run) drive(ctx context.Context) error {
	if err := s.record(ctx, personalassistant.FolderSetupRunning, "", nil); err != nil {
		return err
	}
	if err := s.prepare(ctx); err != nil {
		return err
	}
	for pass := 0; pass < maxPasses; pass++ {
		journey, err := s.journey.Read(ctx, s.runID)
		if err != nil {
			return err
		}
		if journey.RunID != "" {
			s.runID = journey.RunID
		}
		if journey.Busy || journey.ReconciliationRequired || journey.DeclarationIncompatible {
			return &stop{reason: personalassistant.FolderStopFailed}
		}
		if journey.Precondition != nil {
			return &stop{reason: personalassistant.FolderStopInstallFailed}
		}
		s.markFinished(journey)
		if journey.Lifecycle == setupjourney.LifecycleReady {
			return nil
		}
		step := nextStep(journey)
		if step == nil {
			return &stop{reason: personalassistant.FolderStopFailed}
		}
		s.setKinds(stepLines[step.Kind], personalassistant.FolderLineWorking)
		if err := s.record(ctx, personalassistant.FolderSetupRunning, "", nil); err != nil {
			return err
		}
		if err := s.driveStep(ctx, journey, step); err != nil {
			return err
		}
	}
	return &stop{reason: personalassistant.FolderStopFailed}
}

// prepare brings the plugins the plan promised into place, then opens the plugin
// quest, which exists only once the plugin does. Each is held to the confirmed
// intent: a release other than the one the user saw stops the run.
func (s *run) prepare(ctx context.Context) error {
	intent := s.cfg.Plan.Intent
	if s.Install != nil && intent.Integration != "" {
		if err := s.installIntegration(ctx, intent); err != nil {
			return err
		}
	} else {
		s.setKind(personalassistant.FolderPlanIntegration, personalassistant.FolderLineDone)
	}
	if s.Providers != nil && intent.Provider != "" {
		if err := s.installProvider(ctx, intent); err != nil {
			return err
		}
	} else {
		s.setKind(personalassistant.FolderPlanProvider, personalassistant.FolderLineDone)
	}
	s.journey = s.Journey
	if s.OpenProject != nil {
		opened, err := s.OpenProject(ctx)
		if err != nil || opened == nil {
			// An install that succeeded but left no single plugin quest is not a
			// setup the runner can finish.
			return failed("the plugin quest is unavailable after the install")
		}
		s.journey = opened
	}
	return s.record(ctx, personalassistant.FolderSetupRunning, "", nil)
}

// installActions are the reviews a state of the integration may need, in order.
func installActions(state string) []setupjourney.ActionID {
	switch state {
	case personalassistant.FolderInstallInstall:
		return []setupjourney.ActionID{setupjourney.ActionReviewInstall, setupjourney.ActionReviewEnable}
	case personalassistant.FolderInstallEnable:
		return []setupjourney.ActionID{setupjourney.ActionReviewEnable}
	case personalassistant.FolderInstallUpdate:
		return []setupjourney.ActionID{setupjourney.ActionReviewUpdate, setupjourney.ActionReviewEnable}
	}
	return nil
}

// installIntegration drives the host's install quest through the reviews the
// plan promised, one review then commit at a time.
func (s *run) installIntegration(ctx context.Context, intent personalassistant.FolderSetupIntent) error {
	s.setKind(personalassistant.FolderPlanIntegration, personalassistant.FolderLineWorking)
	if err := s.record(ctx, personalassistant.FolderSetupRunning, "", nil); err != nil {
		return err
	}
	allowed := installActions(intent.Integration)
	runID := ""
	for pass := 0; pass < 6; pass++ {
		journey, err := s.Install.Read(ctx, runID)
		if err != nil {
			return &stop{reason: personalassistant.FolderStopInstallFailed, detail: err.Error()}
		}
		if journey.RunID != "" {
			runID = journey.RunID
		}
		var step *setupjourney.StepProjection
		for i := range journey.Steps {
			if journey.Steps[i].Kind == specialist.SetupStepIntegrationInstall {
				step = &journey.Steps[i]
			}
		}
		if step == nil {
			return &stop{reason: personalassistant.FolderStopInstallFailed, detail: "the install quest has no install step"}
		}
		if step.Status == setupjourney.StepComplete {
			s.setKind(personalassistant.FolderPlanIntegration, personalassistant.FolderLineDone)
			return nil
		}
		var action setupjourney.ActionID
		for _, candidate := range []setupjourney.ActionID{setupjourney.ActionReviewInstall, setupjourney.ActionReviewEnable, setupjourney.ActionReviewUpdate} {
			if has(step, candidate) {
				action = candidate
				break
			}
		}
		if action == "" {
			return &stop{reason: personalassistant.FolderStopInstallFailed, detail: "the install quest offers no action (" + string(step.ReasonCode) + ")"}
		}
		if !containsAction(allowed, action) {
			// The plan said this would not be needed (or would be a different step).
			return &stop{reason: personalassistant.FolderStopPlanChanged, detail: "the install quest now needs " + string(action)}
		}
		review, err := s.reviewOn(ctx, s.Install, runID, journey.StateRevision, action, nil)
		if err != nil {
			return &stop{reason: personalassistant.FolderStopInstallFailed, detail: err.Error()}
		}
		shown := review.Integration
		if shown == nil || shown.PluginID != intent.IntegrationPlugin || !integrationVersionMatches(shown, action, intent.IntegrationVersion) {
			return &stop{reason: personalassistant.FolderStopPlanChanged, detail: "the integration review differs from the plan"}
		}
		if err := s.commitOn(ctx, s.Install, runID, journey.StateRevision, review, nil); err != nil {
			return &stop{reason: personalassistant.FolderStopInstallFailed, detail: err.Error()}
		}
	}
	return &stop{reason: personalassistant.FolderStopInstallFailed, detail: "the integration did not become ready"}
}

// integrationVersionMatches holds a review to the release the plan named: the
// release an install or update would put in place, or the one an enable turns on.
func integrationVersionMatches(shown *setupjourney.IntegrationProjection, action setupjourney.ActionID, planned string) bool {
	if action == setupjourney.ActionReviewEnable {
		return shown.InstalledVersion == planned
	}
	return shown.ExpectedVersion == planned
}

func containsAction(list []setupjourney.ActionID, action setupjourney.ActionID) bool {
	for _, candidate := range list {
		if candidate == action {
			return true
		}
	}
	return false
}

// installProvider brings the Home provider to the reviewed release the plan named.
func (s *run) installProvider(ctx context.Context, intent personalassistant.FolderSetupIntent) error {
	s.setKind(personalassistant.FolderPlanProvider, personalassistant.FolderLineWorking)
	if err := s.record(ctx, personalassistant.FolderSetupRunning, "", nil); err != nil {
		return err
	}
	preview, err := s.Providers.Preview(ctx)
	if err != nil {
		return &stop{reason: personalassistant.FolderStopInstallFailed, detail: err.Error()}
	}
	if preview.Ready {
		s.setKind(personalassistant.FolderPlanProvider, personalassistant.FolderLineDone)
		return nil
	}
	if intent.Provider == personalassistant.FolderInstallReady || preview.PluginID != intent.ProviderPlugin || preview.Version != intent.ProviderVersion {
		return &stop{reason: personalassistant.FolderStopPlanChanged, detail: "the Home provider differs from the plan"}
	}
	if err := s.Providers.Install(ctx, preview.Version); err != nil {
		return &stop{reason: personalassistant.FolderStopInstallFailed, detail: err.Error()}
	}
	if verified, err := s.Providers.Preview(ctx); err != nil || !verified.Ready {
		return &stop{reason: personalassistant.FolderStopInstallFailed, detail: "the Home provider was not ready after the install"}
	}
	s.setKind(personalassistant.FolderPlanProvider, personalassistant.FolderLineDone)
	return nil
}

// markFinished turns the lines of every complete step to done.
func (s *run) markFinished(journey *setupjourney.JourneyProjection) {
	for _, step := range journey.Steps {
		if step.Status == setupjourney.StepComplete {
			s.setKinds(stepLines[step.Kind], personalassistant.FolderLineDone)
		}
	}
}

// nextStep is the first step the runner drives that is not yet complete.
func nextStep(journey *setupjourney.JourneyProjection) *setupjourney.StepProjection {
	for i := range journey.Steps {
		step := &journey.Steps[i]
		if _, handled := stepLines[step.Kind]; handled && step.Status != setupjourney.StepComplete {
			return step
		}
	}
	return nil
}

func (s *run) driveStep(ctx context.Context, journey *setupjourney.JourneyProjection, step *setupjourney.StepProjection) error {
	switch step.Kind {
	case specialist.SetupStepProjectConnect:
		return s.connectProject(ctx, journey, step)
	case specialist.SetupStepWorkspaceSetup:
		return s.selectFileOnly(ctx, journey, step)
	case specialist.SetupStepAssistantProgramStaffing:
		return s.staff(ctx, journey, step)
	default:
		return &stop{reason: personalassistant.FolderStopFailed}
	}
}

func has(step *setupjourney.StepProjection, action setupjourney.ActionID) bool {
	for _, available := range step.Actions {
		if available.ID == action {
			return true
		}
	}
	return false
}

// placement is D5: grouped when the blueprint requires a Home or one already
// exists, standalone otherwise.
func placement(prep *projectconnection.HomePreparation) (string, error) {
	if prep == nil {
		return "", nil
	}
	allowed := map[string]bool{}
	for _, composition := range prep.AvailableCompositions {
		allowed[composition] = true
	}
	switch {
	case len(allowed) == 1:
		for composition := range allowed {
			return composition, nil
		}
	case allowed["grouped"] && (prep.GroupPolicy == string(projecttemplates.GroupPolicyRequired) || prep.Exists):
		return "grouped", nil
	case allowed["standalone"]:
		return "standalone", nil
	}
	return "", &stop{reason: personalassistant.FolderStopFailed}
}

func (s *run) connectProject(ctx context.Context, journey *setupjourney.JourneyProjection, step *setupjourney.StepProjection) error {
	prep := step.Preparation
	if has(step, setupjourney.ActionReviewCreateGroup) && prep != nil && prep.GroupPolicy == string(projecttemplates.GroupPolicyRequired) && !prep.Exists {
		// Creating a required Home is driven in a later group.
		return failed("the blueprint requires a Home that does not exist yet")
	}
	if !has(step, setupjourney.ActionReviewExistingProject) {
		return failed("the project step does not offer an existing-project review")
	}
	composition, err := placement(prep)
	if err != nil {
		return err
	}
	token, folder, err := s.Selections.Select(ctx)
	if err != nil {
		return err
	}
	input, err := json.Marshal(projectconnection.Request{
		ModeID: projecttemplates.ProjectConnectionExistingProject, SelectionToken: token,
		EntryName: s.entryName, WorkspaceName: s.cfg.WorkspaceName, GroupComposition: composition,
	})
	if err != nil {
		return err
	}
	review, err := s.review(ctx, journey, setupjourney.ActionReviewExistingProject, input)
	if err != nil {
		return err
	}
	shown := review.ProjectConnection
	if shown == nil || review.CommitAction != setupjourney.ActionConnectExistingProject ||
		shown.ModeID != projecttemplates.ProjectConnectionExistingProject ||
		filepath.Clean(shown.SelectedFolder) != filepath.Clean(folder) ||
		shown.WorkspaceName != s.cfg.WorkspaceName ||
		(composition != "" && shown.GroupComposition != composition) {
		return &stop{reason: personalassistant.FolderStopPlanChanged}
	}
	if shown.EntryName == "" {
		candidates := make([]string, 0, len(shown.EntryCandidates))
		for _, name := range shown.EntryCandidates {
			if name != "" && filepath.Base(name) == name {
				candidates = append(candidates, name)
			}
		}
		return &stop{reason: personalassistant.FolderStopNeedsChoice, candidates: candidates}
	}
	if s.entryName != "" && shown.EntryName != s.entryName {
		return &stop{reason: personalassistant.FolderStopPlanChanged}
	}
	return s.commit(ctx, journey, review, input)
}

func (s *run) selectFileOnly(ctx context.Context, journey *setupjourney.JourneyProjection, step *setupjourney.StepProjection) error {
	if !has(step, setupjourney.ActionReviewFileOnlyMode) {
		return &stop{reason: personalassistant.FolderStopFailed}
	}
	review, err := s.review(ctx, journey, setupjourney.ActionReviewFileOnlyMode, nil)
	if err != nil {
		return err
	}
	// The commit action is what pins the mode. Before it lands the review's own
	// mode_id is still empty, so only an explicit different mode is a mismatch.
	shown := review.WorkspaceSetup
	if shown == nil || review.CommitAction != setupjourney.ActionSelectFileOnlyMode ||
		(shown.ModeID != "" && shown.ModeID != fileOnlyModeID) ||
		shown.LiveControlConfigured || shown.LiveControlTested {
		return &stop{reason: personalassistant.FolderStopPlanChanged}
	}
	return s.commit(ctx, journey, review, nil)
}

type staffedRole struct {
	RoleID string `json:"role_id"`
	Name   string `json:"name"`
}

func (s *run) staff(ctx context.Context, journey *setupjourney.JourneyProjection, step *setupjourney.StepProjection) error {
	if step.Staffing == nil {
		return &stop{reason: personalassistant.FolderStopFailed}
	}
	// Home roles first, then the project's; one pair per scope per pass.
	for _, scope := range []workspace.AssistantRoleScope{workspace.AssistantRoleScopeHome, workspace.AssistantRoleScopeProject} {
		review, commit := setupjourney.ActionReviewHomeStaffing, setupjourney.ActionAddHomeStaffing
		if scope == workspace.AssistantRoleScopeProject {
			review, commit = setupjourney.ActionReviewProjectStaffing, setupjourney.ActionAddProjectStaffing
		}
		roles := missingRoles(step.Staffing, scope)
		if len(roles) == 0 {
			continue
		}
		if !has(step, review) {
			return &stop{reason: personalassistant.FolderStopFailed}
		}
		input, err := json.Marshal(struct {
			Roles []staffedRole `json:"roles"`
		}{Roles: roles})
		if err != nil {
			return err
		}
		reviewed, err := s.review(ctx, journey, review, input)
		if err != nil {
			return err
		}
		if reviewed.Staffing == nil || reviewed.CommitAction != commit || !staffingMatches(reviewed.Staffing, scope, roles) {
			return &stop{reason: personalassistant.FolderStopPlanChanged}
		}
		for _, target := range reviewed.Staffing.Scopes {
			if !target.ModelsReady {
				return &stop{reason: personalassistant.FolderStopNeedsModel}
			}
		}
		return s.commit(ctx, journey, reviewed, input)
	}
	return &stop{reason: personalassistant.FolderStopFailed}
}

// missingRoles are the required, unconfigured roles of one scope, named the way
// the journey's own staffing form proposes them.
func missingRoles(staffing *setupjourney.StaffingProjection, scope workspace.AssistantRoleScope) []staffedRole {
	var roles []staffedRole
	for _, target := range staffing.Scopes {
		if target.Scope != scope {
			continue
		}
		for _, role := range target.Roles {
			if !role.Required || role.Configured {
				continue
			}
			name := role.Label
			if scope == workspace.AssistantRoleScopeProject {
				name = role.Label + " · " + target.WorkspaceLabel
			}
			roles = append(roles, staffedRole{RoleID: role.RoleID, Name: truncate(name, 80)})
		}
	}
	return roles
}

func truncate(value string, limit int) string {
	if runes := []rune(value); len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

// staffingMatches holds the review to the plan: only the requested required
// roles, created (never bound to an existing agent), in the requested scope.
func staffingMatches(shown *setupjourney.StaffingProjection, scope workspace.AssistantRoleScope, want []staffedRole) bool {
	wanted := map[string]string{}
	for _, role := range want {
		wanted[role.RoleID] = role.Name
	}
	seen := 0
	for _, target := range shown.Scopes {
		if target.Scope != scope {
			return false
		}
		for _, role := range target.Roles {
			name, ok := wanted[role.RoleID]
			if !ok || !role.Required || role.Bound || role.ProfileName != name {
				return false
			}
			seen++
		}
	}
	return seen == len(wanted)
}

// review runs a review action on the plugin quest at the projection's revision.
func (s *run) review(ctx context.Context, journey *setupjourney.JourneyProjection, action setupjourney.ActionID, input json.RawMessage) (*setupjourney.ReviewProjection, error) {
	return s.reviewOn(ctx, s.journey, s.runID, journey.StateRevision, action, input)
}

// commit confirms a review it has already checked, with the same input.
func (s *run) commit(ctx context.Context, journey *setupjourney.JourneyProjection, review *setupjourney.ReviewProjection, input json.RawMessage) error {
	return s.commitOn(ctx, s.journey, s.runID, journey.StateRevision, review, input)
}

func (s *run) reviewOn(ctx context.Context, j Journey, runID string, revision int64, action setupjourney.ActionID, input json.RawMessage) (*setupjourney.ReviewProjection, error) {
	result, err := j.Mutate(ctx, runID, action, setupjourney.ActionMutation{
		IfRevision: revision, IdempotencyKey: s.key(), Input: input,
	})
	if err != nil {
		return nil, err
	}
	if result == nil || result.Review == nil || result.Review.Token == "" {
		return nil, fmt.Errorf("foldersetup: %s returned no review", action)
	}
	return result.Review, nil
}

func (s *run) commitOn(ctx context.Context, j Journey, runID string, revision int64, review *setupjourney.ReviewProjection, input json.RawMessage) error {
	_, err := j.Mutate(ctx, runID, review.CommitAction, setupjourney.ActionMutation{
		IfRevision: revision, IdempotencyKey: s.key(), ReviewToken: review.Token, Input: input,
	})
	return err
}

func (s *run) key() string {
	if s.NewKey != nil {
		return s.NewKey()
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("fs-%d", len(buf))
	}
	return "fs-" + hex.EncodeToString(buf)
}

// failureReason maps a journey failure to a stop reason the card explains.
func failureReason(err error) string {
	var failure *setupjourney.Failure
	if errors.As(err, &failure) {
		switch failure.ReasonCode {
		case setupjourney.ReasonIntegrationNotInstalled, setupjourney.ReasonIntegrationDisabled,
			setupjourney.ReasonIntegrationUpdateRequired, setupjourney.ReasonIntegrationLocalUnverified,
			setupjourney.ReasonIntegrationIdentityMismatch, setupjourney.ReasonIntegrationUnsupported,
			setupjourney.ReasonIntegrationReleaseNotReady:
			return personalassistant.FolderStopInstallFailed
		}
	}
	if strings.Contains(err.Error(), "context canceled") {
		return personalassistant.FolderStopInterrupted
	}
	return personalassistant.FolderStopFailed
}
