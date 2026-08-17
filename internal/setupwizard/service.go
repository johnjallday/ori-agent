package setupwizard

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/runtimecapability"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Store is the workspace access this service needs.
//
// GetFolderWorkspace, not Get: the wizard snapshot and its progress are
// canonical workspace.json fields with no SQLite column, so the primary store's
// Get always reports them as absent. Reading through the folder store is what
// makes "has this workspace finished setup?" answerable at all.
type Store interface {
	GetFolderWorkspace(id string) (*workspace.Workspace, error)
	Update(id string, fn func(*workspace.Workspace) error) error
}

// CompletionHook runs once, the first time a workspace's wizard reaches ready.
// It is how the blueprint's setup help task gets completed without this package
// knowing anything about tasks. Failures are the hook's problem: setup is
// already ready by the time it runs, and re-running it must be harmless.
type CompletionHook func(ctx context.Context, workspaceID string)

// RuntimeService is the narrow generalized runtime surface setup consumes. Mode
// choice and readiness remain authoritative runtime state; Setup Wizard only
// projects them into its ordered lifecycle.
type RuntimeService interface {
	Status(context.Context, string) (runtimecapability.Status, error)
	SelectMode(context.Context, string, string) (runtimecapability.Status, error)
}

// Service owns setup-wizard lifecycle and readiness for every workspace.
type Service struct {
	store    Store
	registry *Registry
	runtime  RuntimeService
	now      func() time.Time
	onReady  CompletionHook
	// blueprints backfills workspaces created before their blueprint declared a
	// wizard. Nil means no workspace is ever migrated.
	blueprints BlueprintLookup
}

// NewService returns a service over the given workspace store and adapter
// registry. A nil registry is usable: every step naming an adapter then reports
// adapter_unavailable rather than running something unregistered.
func NewService(store Store, registry *Registry) *Service {
	return &Service{store: store, registry: registry, now: time.Now}
}

// SetCompletionHook installs the callback fired the first time a workspace
// becomes ready.
func (s *Service) SetCompletionHook(hook CompletionHook) {
	if s == nil {
		return
	}
	s.onReady = hook
}

// SetRuntimeService projects operating-mode choice and runtime readiness into
// runtime_mode/runtime_readiness steps. Nil leaves those steps honestly
// unavailable.
func (s *Service) SetRuntimeService(runtime RuntimeService) {
	if s != nil {
		s.runtime = runtime
	}
}

// Status is the projected setup state of one workspace: the wizard as the
// workspace recorded it, plus the server's current verdict on every step.
type Status struct {
	WorkspaceID string `json:"workspace_id"`
	// Applicable reports whether the workspace's blueprint declares a wizard.
	Applicable bool `json:"applicable"`
	// State is the lifecycle state (workspace.SetupWizardState*).
	State string `json:"state"`
	// BlueprintID and BlueprintName identify the originating blueprint.
	BlueprintID   string `json:"blueprint_id,omitempty"`
	BlueprintName string `json:"blueprint_name,omitempty"`
	// Title is the wizard's user-facing title (untrusted author text).
	Title string `json:"title,omitempty"`
	// WizardVersion is the snapshot's schema version.
	WizardVersion int `json:"wizard_version,omitempty"`
	// CurrentStepID is where the wizard should open or resume: the first
	// unresolved or failed required step. Empty when nothing is outstanding.
	CurrentStepID string `json:"current_step_id,omitempty"`
	// Dismissed reports that the user closed an unfinished wizard. It suppresses
	// auto-open and nothing else.
	Dismissed bool `json:"dismissed"`
	// AutoOpen reports whether this load should open the dialog by itself. The
	// server decides it, so every surface agrees and it can only happen once.
	AutoOpen bool `json:"auto_open"`
	// Steps are the wizard's steps in declaration order with their current
	// status.
	Steps []StepStatus `json:"steps,omitempty"`
	// CompletedAt is when setup first became ready, retained across a later
	// regression.
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// Diagnostic explains a snapshot this build cannot run. When set, no step
	// action is offered and setup cannot complete.
	Diagnostic string `json:"diagnostic,omitempty"`
}

// StepStatus is one step's definition and current server-evaluated state.
type StepStatus struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Required    bool   `json:"required"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Disclosure  string `json:"disclosure,omitempty"`
	// Adapter is the domain that serves this step. It is echoed so a domain
	// module can render only its own steps; it is not a selector — the server
	// resolves the adapter from the workspace's snapshot either way.
	Adapter string `json:"adapter,omitempty"`
	// Status is one of workspace.SetupStepStatus*.
	Status string `json:"status"`
	// Action is what the step's primary control does next: "confirm" to commit
	// it after the user approves, "recheck" to re-evaluate a requirement that is
	// satisfied elsewhere, or "" when the step is already resolved. The server
	// decides it because only the server knows whether a step is backed by an
	// adapter with something to re-check or by a decision only the user can make.
	Action string `json:"action,omitempty"`
	// Summary is the adapter's plain-language statement of where the step
	// stands. Never contains a path, address, account id, or filename.
	Summary string `json:"summary,omitempty"`
	// ErrorCategory is a stable safe category when the step cannot pass.
	ErrorCategory string `json:"error_category,omitempty"`
	// Options are the choices the step offers, if any.
	Options []StepOption `json:"options,omitempty"`
	// SelectedOption is the choice recorded for this step, when one was made.
	SelectedOption string `json:"selected_option,omitempty"`
	// CompletedAt is when the step first passed or was skipped.
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// Directory/Capability/Plugin echo the requirement the step references, so
	// the dialog can label it without re-reading the template.
	DirectoryLabel        string `json:"directory_label,omitempty"`
	DirectorySuggest      string `json:"directory_suggested_path,omitempty"`
	DirectoryAccess       string `json:"directory_access_disclosure,omitempty"`
	CapabilityKey         string `json:"capability_key,omitempty"`
	RuntimeRequirementKey string `json:"runtime_requirement_key,omitempty"`
	PluginName            string `json:"plugin_name,omitempty"`
}

// Ready reports whether every required step currently passes.
func (s Status) Ready() bool { return s.State == workspace.SetupWizardStateReady }

// Status returns the workspace's current setup state, re-evaluating every step
// against its domain. Read-only from the caller's point of view; it may still
// persist a state change, because "this requirement regressed" is a fact
// discovered by looking, and forgetting it until someone mutates something is
// how a broken workspace keeps reporting ready.
func (s *Service) Status(ctx context.Context, workspaceID string) (Status, error) {
	return s.refresh(ctx, workspaceID, nil)
}

// Recheck is Status under the name the "Check again" control uses. Readiness is
// always evaluated live, so there is nothing extra to do — which is the point:
// no cache to invalidate, no stale verdict to explain.
func (s *Service) Recheck(ctx context.Context, workspaceID string) (Status, error) {
	return s.Status(ctx, workspaceID)
}

// Open records that the wizard has been shown. It is what makes auto-open
// happen exactly once, and it is recorded on the server so a second browser
// tab, a reload, or a restart all agree.
func (s *Service) Open(ctx context.Context, workspaceID string) (Status, error) {
	return s.refresh(ctx, workspaceID, func(t *transition, progress *workspace.SetupWizardProgress) error {
		if progress.FirstOpenedAt == nil {
			opened := t.now
			progress.FirstOpenedAt = &opened
		}
		// Re-opening clears the dismissal: the user is looking at it again, so
		// "they closed it" is no longer the reason to keep it shut.
		progress.DismissedAt = nil
		return nil
	})
}

// Dismiss records that the user closed an unfinished wizard. It suppresses
// auto-open and changes nothing else: no step is completed, no readiness is
// implied, and the workspace stays visibly unfinished.
func (s *Service) Dismiss(ctx context.Context, workspaceID string) (Status, error) {
	return s.refresh(ctx, workspaceID, func(t *transition, progress *workspace.SetupWizardProgress) error {
		dismissed := t.now
		progress.DismissedAt = &dismissed
		if progress.FirstOpenedAt == nil {
			// Dismissing implies it was shown; recording that keeps a dismissed
			// wizard from auto-opening on the next load.
			opened := t.now
			progress.FirstOpenedAt = &opened
		}
		return nil
	})
}

// Skip marks an optional step deliberately skipped. Required steps cannot be
// skipped — that is the entire difference between the two.
func (s *Service) Skip(ctx context.Context, workspaceID, stepID string) (Status, error) {
	return s.refresh(ctx, workspaceID, func(t *transition, progress *workspace.SetupWizardProgress) error {
		step, ok := t.resolved.wizard.Step(strings.TrimSpace(stepID))
		if !ok {
			return fmt.Errorf("%w: %q", ErrUnknownStep, stepID)
		}
		if step.Required {
			return fmt.Errorf("%w: step %q is required and cannot be skipped", ErrInvalidAction, step.ID)
		}
		t.skipped = step.ID
		return nil
	})
}

// Confirm performs a step's committing action after the user has explicitly
// approved it, then re-evaluates the step against its domain.
//
// A step already satisfied is not re-run for the same choice: retrying after a
// timeout or a page refresh must update the same folder binding, watcher,
// schedule, link, or attachment rather than create a second one, and the
// cheapest way to guarantee that is not to act twice at all.
func (s *Service) Confirm(ctx context.Context, workspaceID, stepID string, action StepAction) (Status, error) {
	if strings.TrimSpace(action.Type) == "" {
		action.Type = ActionConfirm
	}
	if !strings.EqualFold(strings.TrimSpace(action.Type), ActionConfirm) {
		return Status{}, fmt.Errorf("%w: %q", ErrInvalidAction, action.Type)
	}

	ws, err := s.loadWorkspace(workspaceID)
	if err != nil {
		return Status{}, err
	}
	resolved, err := s.resolve(ws)
	if err != nil {
		return Status{}, err
	}
	step, ok := resolved.wizard.Step(strings.TrimSpace(stepID))
	if !ok {
		return Status{}, fmt.Errorf("%w: %q", ErrUnknownStep, stepID)
	}

	if step.Kind == workspace.SetupStepKindRuntimeMode {
		if s.runtime == nil {
			return Status{}, fmt.Errorf("%w: runtime mode service", ErrUnknownAdapter)
		}
		option := workspace.NormalizeRuntimeIdentifier(action.Option)
		if option == "" {
			return Status{}, fmt.Errorf("%w: step %q requires an operating mode", ErrInvalidAction, step.ID)
		}
		before, statusErr := s.runtime.Status(ctx, workspaceID)
		if statusErr != nil {
			return Status{}, statusErr
		}
		available := false
		for _, mode := range before.Modes {
			if mode.ID == option {
				available = true
				break
			}
		}
		if !available {
			return Status{}, fmt.Errorf("%w: step %q does not offer option %q", ErrInvalidAction, step.ID, option)
		}
		if before.SelectedModeID != option {
			if _, selectErr := s.runtime.SelectMode(ctx, workspaceID, option); selectErr != nil {
				return Status{}, selectErr
			}
		}
		return s.refresh(ctx, workspaceID, func(t *transition, progress *workspace.SetupWizardProgress) error {
			t.acknowledged = step.ID
			t.chosenOption = option
			return nil
		})
	}

	adapter, err := s.adapterFor(step)
	if err != nil {
		return Status{}, err
	}
	if adapter == nil {
		// A step with no adapter offers no choices, so a client sending one is
		// sending something this step does not have. Recording it anyway would
		// persist an option no adapter ever declared.
		if strings.TrimSpace(action.Option) != "" {
			return Status{}, fmt.Errorf("%w: step %q offers no options", ErrInvalidAction, step.ID)
		}
		// A step with no adapter has no external truth to check: the user's
		// explicit approval, recorded here, *is* the server-side fact. That is
		// not the browser deciding readiness — it is the server recording a
		// decision only the user can make.
		return s.refresh(ctx, workspaceID, func(t *transition, progress *workspace.SetupWizardProgress) error {
			t.acknowledged = step.ID
			t.chosenOption = strings.TrimSpace(action.Option)
			return nil
		})
	}

	progress := ws.GetSetupWizardProgress()
	req := resolved.request(workspaceID, step)
	req.Selections = recordedSelections(progress)
	if recorded, ok := progress.Step(step.ID); ok {
		req.SelectedOption = recorded.SelectedOption
	}
	before, evalErr := adapter.Evaluate(ctx, req)
	// The option a client may send is exactly one the adapter itself just
	// offered. Checking it here rather than in each adapter makes it a property
	// of the platform: an unrecognized token is refused before any domain code
	// sees it, whatever the blueprint.
	if err := validateOption(step, before, evalErr, action.Option); err != nil {
		return Status{}, err
	}
	alreadySatisfied := evalErr == nil && before.Ready && optionAlreadySelected(before, action.Option)
	if !alreadySatisfied {
		if _, err := adapter.Confirm(ctx, req, action); err != nil {
			return Status{}, err
		}
	}
	return s.refresh(ctx, workspaceID, func(t *transition, progress *workspace.SetupWizardProgress) error {
		t.acknowledged = step.ID
		t.chosenOption = strings.TrimSpace(action.Option)
		return nil
	})
}

// Complete is the explicit "Finish" gesture. It succeeds only when the server
// finds every required step passing right now — a client cannot complete a
// wizard by asserting that it is done.
func (s *Service) Complete(ctx context.Context, workspaceID string) (Status, error) {
	status, err := s.refresh(ctx, workspaceID, nil)
	if err != nil {
		return Status{}, err
	}
	if !status.Ready() {
		return status, fmt.Errorf("%w: setup is not ready; %s", ErrInvalidAction, outstandingSummary(status))
	}
	return status, nil
}

// transition carries one call's intent into the state derivation: what the user
// just did, on top of what the adapters currently report.
type transition struct {
	now      time.Time
	resolved resolvedWizard
	// skipped is the optional step the user chose to skip on this call.
	skipped string
	// acknowledged is the step the user explicitly confirmed on this call. It
	// matters only for steps with no adapter, where the user's decision is the
	// only fact there is to record.
	acknowledged string
	// chosenOption is the option the user picked on the acknowledged step, when
	// it offered a choice. Recorded because a choice outlives the click.
	chosenOption string
}

// mutator applies a caller's intent to the progress record before the derived
// state is recomputed over it.
type mutator func(*transition, *workspace.SetupWizardProgress) error

func (s *Service) loadWorkspace(workspaceID string) (*workspace.Workspace, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("setup wizard service is not configured")
	}
	if strings.TrimSpace(workspaceID) == "" {
		return nil, fmt.Errorf("workspace id is required")
	}
	ws, err := s.store.GetFolderWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}
	if ws == nil {
		return nil, fmt.Errorf("workspace %s not found", workspaceID)
	}
	return ws, nil
}

func (s *Service) timestamp() time.Time {
	if s == nil || s.now == nil {
		return time.Now()
	}
	return s.now()
}

// refresh is the single path through which setup state is read or changed:
// resolve the snapshot, ask every adapter where its requirement stands, apply
// the caller's intent, derive the lifecycle from the result, and persist only
// what actually changed.
//
// mutate may be nil for a pure read. Even then this can write: discovering that
// a completed requirement has regressed is a fact, and a workspace that keeps
// reporting ready until someone happens to mutate it is the failure mode the
// needs_attention state exists to prevent.
func (s *Service) refresh(ctx context.Context, workspaceID string, mutate mutator) (Status, error) {
	ws, err := s.loadWorkspace(workspaceID)
	if err != nil {
		return Status{}, err
	}
	// A workspace created before its blueprint declared a wizard is backfilled
	// here, on the first look — not by a startup sweep over everything a user
	// owns. Setup state is only ever needed for the workspace being looked at,
	// and doing it lazily means a workspace nobody opens is never touched.
	if s.migrateIfNeeded(ws) {
		if reloaded, reloadErr := s.loadWorkspace(workspaceID); reloadErr == nil {
			ws = reloaded
		}
	}

	resolved, resolveErr := s.resolve(ws)
	if resolveErr != nil {
		if mutate != nil {
			return Status{}, resolveErr
		}
		return s.unrunnableStatus(ws, workspaceID, resolveErr), nil
	}

	t := &transition{now: s.timestamp(), resolved: resolved}
	// The persisted record is read from the *folder* workspace: the store's
	// Update hands back a SQLite-shaped copy, which never carries setup progress
	// (it has no column), so reading it there would look like "no progress" and
	// silently reset the user's approvals.
	previous := ws.GetSetupWizardProgress()
	next := workspace.CloneSetupWizardProgress(previous)
	if next == nil {
		next = &workspace.SetupWizardProgress{WizardVersion: resolved.wizard.Version}
	}
	if mutate != nil {
		if err := mutate(t, next); err != nil {
			return Status{}, err
		}
		if next.FirstOpenedAt == nil {
			// Any deliberate action implies the user has seen the wizard.
			// Recording it here means acting on a step also spends the one
			// auto-open, instead of leaving it primed to reappear later.
			opened := t.now
			next.FirstOpenedAt = &opened
		}
		// The caller's intent is applied before the adapters are asked, so a
		// choice made on this call is one they can see. Evaluating first would
		// hand them the previous answer and report the step unsatisfied by the
		// very action that satisfied it.
		s.applyChoices(t, next)
	}

	// Adapters are consulted before the store lock is taken: an evaluation can
	// reach a connector or the filesystem, and holding a workspace lock across
	// that would serialize every other write to the workspace behind it.
	readiness := s.evaluateSteps(ctx, workspaceID, resolved, next)
	s.derive(t, next, readiness)

	// Emitted from the two records rather than from the call sites, so an event
	// can only describe a move the workspace actually made.
	s.emitTransitions(resolved, previous, next, readiness)

	changed := mutate != nil || progressChanged(previous, next)
	if changed && !(previous == nil && next.State == workspace.SetupWizardStateNotStarted) {
		if err := s.store.Update(workspaceID, func(w *workspace.Workspace) error {
			w.SetSetupWizardProgress(next)
			return nil
		}); err != nil {
			return Status{}, err
		}
		if previous == nil || previous.CompletedAt == nil {
			if next.CompletedAt != nil && s.onReady != nil {
				// Fired outside the store lock, and only on the first transition
				// to ready: a repaired workspace must not look like a fresh
				// completion to whatever this hook drives.
				s.onReady(ctx, workspaceID)
			}
		}
	}

	return s.status(workspaceID, resolved, next, readiness), nil
}

// evaluateSteps asks each step's adapter where its requirement stands. Steps
// with no adapter get no verdict here: their readiness comes from the recorded
// user decision, which derive() applies.
func (s *Service) evaluateSteps(ctx context.Context, workspaceID string, resolved resolvedWizard, progress *workspace.SetupWizardProgress) map[string]StepReadiness {
	out := make(map[string]StepReadiness, len(resolved.wizard.Steps))
	// Every adapter sees every recorded choice, not just its own step's: the
	// step that asks a question and the steps that must honor the answer are
	// different steps.
	selections := recordedSelections(progress)
	var (
		runtimeStatus runtimecapability.Status
		runtimeErr    error
		runtimeRead   bool
	)
	for _, step := range resolved.wizard.Steps {
		if step.Kind == workspace.SetupStepKindRuntimeMode || step.Kind == workspace.SetupStepKindRuntimeReadiness {
			if !runtimeRead {
				runtimeRead = true
				if s.runtime == nil {
					runtimeErr = errors.New("runtime capability service is unavailable")
				} else {
					runtimeStatus, runtimeErr = s.runtime.Status(ctx, workspaceID)
				}
			}
			out[step.ID] = runtimeStepReadiness(step, runtimeStatus, runtimeErr)
			continue
		}
		adapter, err := s.adapterFor(step)
		if err != nil {
			// A step whose adapter is not wired in this build is blocked, never
			// assumed satisfied and never attempted.
			out[step.ID] = StepReadiness{
				Blocked:       true,
				Summary:       "This setup step is unavailable in this build.",
				ErrorCategory: ErrorCategoryUnavailable,
			}
			continue
		}
		if adapter == nil {
			continue
		}
		request := resolved.request(workspaceID, step)
		request.Selections = selections
		if recorded, ok := progress.Step(step.ID); ok {
			request.SelectedOption = recorded.SelectedOption
		}
		readiness, err := adapter.Evaluate(ctx, request)
		if err != nil {
			out[step.ID] = StepReadiness{
				Blocked:       true,
				Summary:       "This requirement could not be checked.",
				ErrorCategory: ErrorCategoryDomainError,
			}
			continue
		}
		out[step.ID] = readiness
	}
	return out
}

func runtimeStepReadiness(step workspace.SetupWizardStep, status runtimecapability.Status, err error) StepReadiness {
	if err != nil || !status.Applicable {
		return StepReadiness{
			Blocked:       true,
			Summary:       "Runtime setup is unavailable in this build.",
			ErrorCategory: ErrorCategoryUnavailable,
		}
	}
	if step.Kind == workspace.SetupStepKindRuntimeMode {
		options := make([]StepOption, 0, len(status.Modes))
		for _, mode := range status.Modes {
			options = append(options, StepOption{
				ID: mode.ID, Label: mode.Label, Description: mode.Description,
				Selected: mode.ID == status.SelectedModeID,
			})
		}
		readiness := StepReadiness{Ready: status.SelectedModeID != "", Options: options}
		if readiness.Ready {
			readiness.Summary = "Operating mode selected."
		} else {
			readiness.Summary = "Choose how this workspace should operate."
		}
		return readiness
	}

	key := workspace.NormalizeRuntimeIdentifier(step.RequirementKey)
	for _, requirement := range status.Requirements {
		if requirement.Key != key {
			continue
		}
		ready := requirement.DurableState == runtimecapability.DurableConfigured
		category := ""
		if !ready {
			category = ErrorCategoryNotConfigured
			if requirement.ReasonCode == runtimecapability.ReasonAdapterUnavailable || requirement.ReasonCode == runtimecapability.ReasonCheckFailed {
				category = ErrorCategoryUnavailable
			}
		}
		return StepReadiness{
			Ready:         ready,
			Blocked:       !ready && requirement.ReasonCode != "",
			Summary:       requirement.Summary,
			ErrorCategory: category,
		}
	}
	if status.SelectedModeID == "" {
		return StepReadiness{Summary: "Choose an operating mode first."}
	}
	// A declared requirement absent from the selected mode is deliberately not
	// applicable (for example live control in File-only mode), so this step is
	// complete without probing or granting anything.
	return StepReadiness{Ready: true, Summary: "Not required in the selected operating mode."}
}

// applyChoices records an option the user picked on this call, so the adapters
// evaluated next see the workspace as it is about to be, not as it was.
func (s *Service) applyChoices(t *transition, progress *workspace.SetupWizardProgress) {
	if t.acknowledged == "" || t.chosenOption == "" {
		return
	}
	for i := range progress.Steps {
		if progress.Steps[i].StepID == t.acknowledged {
			progress.Steps[i].SelectedOption = t.chosenOption
			return
		}
	}
	progress.Steps = append(progress.Steps, workspace.SetupStepProgress{
		StepID:         t.acknowledged,
		Status:         workspace.SetupStepStatusActive,
		SelectedOption: t.chosenOption,
		UpdatedAt:      t.now,
	})
}

// derive recomputes every step's status and the workspace's lifecycle state
// from the adapters' current verdicts plus what the user has done. It is the
// only place either is decided, so a client's belief about its own progress can
// never become the record.
func (s *Service) derive(t *transition, progress *workspace.SetupWizardProgress, readiness map[string]StepReadiness) {
	steps := t.resolved.wizard.Steps
	progress.WizardVersion = t.resolved.wizard.Version

	previous := make(map[string]workspace.SetupStepProgress, len(progress.Steps))
	for _, step := range progress.Steps {
		previous[step.StepID] = step
	}

	rebuilt := make([]workspace.SetupStepProgress, 0, len(steps))
	allRequiredReady := true
	currentStepID := ""
	for _, step := range steps {
		prior := previous[step.ID]
		priorStatus := workspace.NormalizeSetupStepStatus(prior.Status)
		verdict, evaluated := readiness[step.ID]

		status := workspace.SetupStepStatusPending
		switch {
		case evaluated && verdict.Ready:
			status = workspace.SetupStepStatusComplete
		case !evaluated && (t.acknowledged == step.ID || priorStatus == workspace.SetupStepStatusComplete):
			// No adapter means no external truth to check; the user's recorded
			// approval is the fact, and it is recorded here on the server.
			status = workspace.SetupStepStatusComplete
		case !step.Required && (t.skipped == step.ID || priorStatus == workspace.SetupStepStatusOptionalSkipped):
			status = workspace.SetupStepStatusOptionalSkipped
		case evaluated && verdict.Blocked:
			status = workspace.SetupStepStatusBlocked
		}

		record := workspace.SetupStepProgress{StepID: step.ID, Status: status, UpdatedAt: t.now}
		record.CompletedAt = prior.CompletedAt
		record.SelectedOption = prior.SelectedOption
		if t.acknowledged == step.ID && t.chosenOption != "" {
			record.SelectedOption = t.chosenOption
		}
		if step.Kind == workspace.SetupStepKindRuntimeMode {
			for _, option := range verdict.Options {
				if option.Selected {
					record.SelectedOption = option.ID
					break
				}
			}
		}
		if status == workspace.SetupStepStatusComplete || status == workspace.SetupStepStatusOptionalSkipped {
			if record.CompletedAt == nil {
				completed := t.now
				record.CompletedAt = &completed
			}
		} else {
			// A regressed step is no longer complete; clearing its completion
			// keeps "when did this last pass?" honest for repair copy.
			record.CompletedAt = nil
		}
		if priorStatus == status && !prior.UpdatedAt.IsZero() {
			record.UpdatedAt = prior.UpdatedAt
		}

		if step.Required && status != workspace.SetupStepStatusComplete {
			allRequiredReady = false
			if currentStepID == "" {
				currentStepID = step.ID
				if status == workspace.SetupStepStatusPending {
					record.Status = workspace.SetupStepStatusActive
				}
			}
		}
		rebuilt = append(rebuilt, record)
	}

	progress.Steps = rebuilt
	progress.CurrentStepID = currentStepID

	switch {
	case allRequiredReady:
		progress.State = workspace.SetupWizardStateReady
		if progress.CompletedAt == nil {
			completed := t.now
			progress.CompletedAt = &completed
		}
	case progress.CompletedAt != nil:
		// Setup passed before and no longer does. The completion timestamp is
		// deliberately kept: repair is not a first-time completion, and the
		// blueprint's setup help task must not be reopened.
		progress.State = workspace.SetupWizardStateNeedsAttention
	case progress.WasMigrated() && configuredBefore(steps, readiness):
		// A backfilled workspace has no completion timestamp — it was set up
		// before anything recorded one. Its evidence of prior setup is what the
		// adapters report: a revoked permission or a failing domain call, neither
		// of which an untouched workspace can produce. Calling that "unfinished
		// setup" would tell someone to configure what they already configured.
		progress.State = workspace.SetupWizardStateNeedsAttention
	case hasActivity(progress):
		progress.State = workspace.SetupWizardStateInProgress
	default:
		progress.State = workspace.SetupWizardStateNotStarted
	}
}

// hasActivity reports whether anything has happened to this wizard yet.
func hasActivity(progress *workspace.SetupWizardProgress) bool {
	if progress == nil {
		return false
	}
	if progress.FirstOpenedAt != nil || progress.DismissedAt != nil {
		return true
	}
	for _, step := range progress.Steps {
		switch workspace.NormalizeSetupStepStatus(step.Status) {
		case workspace.SetupStepStatusPending, workspace.SetupStepStatusActive:
		default:
			return true
		}
	}
	return false
}

// progressChanged reports whether the derived record differs from what is
// persisted in any way worth a write.
func progressChanged(previous, next *workspace.SetupWizardProgress) bool {
	if previous == nil {
		return next != nil
	}
	if previous.State != next.State || previous.CurrentStepID != next.CurrentStepID {
		return true
	}
	if (previous.CompletedAt == nil) != (next.CompletedAt == nil) {
		return true
	}
	if (previous.DismissedAt == nil) != (next.DismissedAt == nil) {
		return true
	}
	if (previous.FirstOpenedAt == nil) != (next.FirstOpenedAt == nil) {
		return true
	}
	if len(previous.Steps) != len(next.Steps) {
		return true
	}
	for i, step := range next.Steps {
		prior := previous.Steps[i]
		if prior.StepID != step.StepID || workspace.NormalizeSetupStepStatus(prior.Status) != workspace.NormalizeSetupStepStatus(step.Status) {
			return true
		}
		if prior.SelectedOption != step.SelectedOption {
			return true
		}
		if (prior.CompletedAt == nil) != (step.CompletedAt == nil) {
			return true
		}
	}
	return false
}

// status projects the runnable wizard plus its derived progress into the view
// every surface renders from.
func (s *Service) status(workspaceID string, resolved resolvedWizard, progress *workspace.SetupWizardProgress, readiness map[string]StepReadiness) Status {
	status := Status{
		WorkspaceID:   workspaceID,
		Applicable:    true,
		State:         workspace.NormalizeSetupWizardState(progress.State),
		BlueprintID:   resolved.provenance.TemplateID,
		BlueprintName: resolved.provenance.TemplateName,
		Title:         resolved.wizard.Title,
		WizardVersion: resolved.wizard.Version,
		CurrentStepID: progress.CurrentStepID,
		Dismissed:     progress.IsDismissed(),
		CompletedAt:   progress.CompletedAt,
	}
	// Auto-open is for setup that has never been shown. A workspace whose setup
	// regressed is deliberately excluded: `needs_attention` is an invitation to
	// repair, not a dialog that ambushes the user for something that was
	// working when they last looked.
	// A backfilled workspace is excluded for the same reason: the user did not
	// create it just now and did not ask to reconfigure it. It gets the durable
	// banner — visible, dismissible, theirs to act on — rather than a dialog
	// over whatever they actually opened the workspace to do.
	status.AutoOpen = status.State != workspace.SetupWizardStateReady &&
		status.State != workspace.SetupWizardStateNeedsAttention &&
		!progress.WasMigrated() &&
		!progress.HasBeenOpened() &&
		!progress.IsDismissed()

	for _, step := range resolved.wizard.Steps {
		record, _ := progress.Step(step.ID)
		verdict := readiness[step.ID]
		projected := StepStatus{
			ID:             step.ID,
			Kind:           step.Kind,
			Adapter:        step.Adapter,
			Required:       step.Required,
			Title:          step.Title,
			Description:    step.Description,
			Disclosure:     step.Disclosure,
			Status:         workspace.NormalizeSetupStepStatus(record.Status),
			Summary:        verdict.Summary,
			ErrorCategory:  verdict.ErrorCategory,
			Options:        verdict.Options,
			SelectedOption: record.SelectedOption,
			CompletedAt:    record.CompletedAt,
		}
		if projected.Status == workspace.SetupStepStatusComplete {
			projected.ErrorCategory = ""
		}
		projected.Action = stepAction(step, projected.Status)
		request := resolved.request(workspaceID, step)
		if request.Directory != nil {
			projected.DirectoryLabel = request.Directory.Label
			projected.DirectorySuggest = request.Directory.SuggestedPath
			projected.DirectoryAccess = request.Directory.AccessDisclosure
		}
		if request.Capability != nil {
			projected.CapabilityKey = request.Capability.Key
		}
		if request.RuntimeRequirement != nil {
			projected.RuntimeRequirementKey = request.RuntimeRequirement.Key
		}
		projected.PluginName = request.Plugin
		status.Steps = append(status.Steps, projected)
	}
	return status
}

// Step action names. A client asks for one of these; it never invents an action
// of its own.
const (
	// StepActionConfirm commits the step after the user approves it.
	StepActionConfirm = "confirm"
	// StepActionRecheck re-evaluates a requirement satisfied outside the wizard
	// (a connector authorized in another tab, a permission granted in Settings).
	StepActionRecheck = "recheck"
)

// stepAction reports what a step's primary control should do next.
//
// The distinction that matters is between a requirement the wizard can commit
// and one it can only observe. A readiness check has nothing to approve — its
// answer comes from the domain, so the only useful action is to look again.
// Everything else, including a summary the user must acknowledge, is committed
// by an explicit confirmation.
func stepAction(step workspace.SetupWizardStep, status string) string {
	switch status {
	case workspace.SetupStepStatusComplete, workspace.SetupStepStatusOptionalSkipped:
		return ""
	}
	if step.Kind == workspace.SetupStepKindReadiness || step.Kind == workspace.SetupStepKindRuntimeReadiness {
		return StepActionRecheck
	}
	return StepActionConfirm
}

// unrunnableStatus reports a workspace whose recorded wizard this build cannot
// run. It offers no steps and no actions — only a diagnostic — so nothing
// executes against a snapshot Ori does not fully understand.
func (s *Service) unrunnableStatus(ws *workspace.Workspace, workspaceID string, err error) Status {
	if errors.Is(err, ErrNoWizard) {
		return Status{
			WorkspaceID: workspaceID,
			Applicable:  false,
			State:       workspace.SetupWizardStateNotApplicable,
		}
	}
	status := Status{
		WorkspaceID: workspaceID,
		Applicable:  true,
		State:       workspace.SetupWizardStateInProgress,
		Diagnostic:  err.Error(),
	}
	if provenance := ws.GetTemplateProvenance(); provenance != nil {
		status.BlueprintID = provenance.TemplateID
		status.BlueprintName = provenance.TemplateName
		if provenance.SetupWizard != nil {
			status.Title = provenance.SetupWizard.Title
			status.WizardVersion = provenance.SetupWizard.Version
		}
		// Worth counting: a snapshot this build refuses is a workspace nobody can
		// finish setting up, and it is invisible from the outside otherwise.
		fields := logger.Fields{eventFieldVersion: status.WizardVersion}
		if id := strings.TrimSpace(provenance.TemplateID); id != "" {
			fields[eventFieldBlueprint] = id
		}
		s.event(EventSnapshotRefused, fields)
	}
	if progress := ws.GetSetupWizardProgress(); progress != nil {
		status.Dismissed = progress.IsDismissed()
		status.CompletedAt = progress.CompletedAt
	}
	return status
}

// optionAlreadySelected reports whether the requested option (if any) is the
// step's current choice, so re-confirming the same choice is a no-op while
// switching to a different one still runs.
func optionAlreadySelected(readiness StepReadiness, option string) bool {
	option = strings.TrimSpace(option)
	if option == "" {
		return true
	}
	for _, opt := range readiness.Options {
		if strings.EqualFold(strings.TrimSpace(opt.ID), option) {
			return opt.Selected
		}
	}
	return false
}

// outstandingSummary names what is still blocking completion, without leaking
// anything domain-specific.
func outstandingSummary(status Status) string {
	var pending []string
	for _, step := range status.Steps {
		if step.Required && step.Status != workspace.SetupStepStatusComplete {
			pending = append(pending, step.ID)
		}
	}
	if len(pending) == 0 {
		return "no required step is outstanding"
	}
	return "outstanding required steps: " + strings.Join(pending, ", ")
}

// recordedSelections collects the choices a workspace has made so far, keyed by
// step ID. Nil progress yields a nil map, which StepRequest.Choice reads as
// "nothing chosen" — the correct answer for a wizard that has never been run.
func recordedSelections(progress *workspace.SetupWizardProgress) map[string]string {
	if progress == nil {
		return nil
	}
	var out map[string]string
	for _, record := range progress.Steps {
		if record.SelectedOption == "" {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(progress.Steps))
		}
		out[record.StepID] = record.SelectedOption
	}
	return out
}

// validateOption refuses any option the step is not currently offering.
//
// Fail-closed on an evaluation error: if the adapter could not say what the
// choices are, there is no basis for accepting one.
func validateOption(step workspace.SetupWizardStep, before StepReadiness, evalErr error, option string) error {
	option = strings.TrimSpace(option)
	if option == "" {
		return nil
	}
	if evalErr != nil {
		return fmt.Errorf("%w: step %q could not be checked, so no option can be chosen", ErrInvalidAction, step.ID)
	}
	for _, offered := range before.Options {
		if offered.ID == option {
			return nil
		}
	}
	return fmt.Errorf("%w: step %q does not offer that option", ErrInvalidAction, step.ID)
}
