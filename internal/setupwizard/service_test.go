package setupwizard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// fakeStore stands in for the production store chain, including the trap that
// chain sets: Update hands back a SQLite-shaped copy with the workspace.json-only
// fields (provenance, setup progress) missing, and the disk write re-hydrates
// whatever the mutation left nil. A service that read progress from inside
// Update would see "nothing recorded" and quietly reset the user's approvals —
// so the fake reproduces that shape rather than a convenient one.
type fakeStore struct {
	ws      *workspace.Workspace
	updates int
	failNth int
}

func (f *fakeStore) GetFolderWorkspace(id string) (*workspace.Workspace, error) {
	if f.ws == nil || f.ws.ID != id {
		return nil, fmt.Errorf("workspace %s not found", id)
	}
	return cloneWorkspace(f.ws), nil
}

func (f *fakeStore) Update(id string, fn func(*workspace.Workspace) error) error {
	if f.ws == nil || f.ws.ID != id {
		return fmt.Errorf("workspace %s not found", id)
	}
	f.updates++
	if f.failNth > 0 && f.updates == f.failNth {
		return errors.New("simulated store failure")
	}
	stale := cloneWorkspace(f.ws)
	stale.TemplateProvenance = nil
	stale.SetupWizardProgress = nil
	if err := fn(stale); err != nil {
		return err
	}
	if stale.TemplateProvenance == nil {
		stale.TemplateProvenance = f.ws.TemplateProvenance
	}
	if stale.SetupWizardProgress == nil {
		stale.SetupWizardProgress = f.ws.SetupWizardProgress
	}
	f.ws = stale
	return nil
}

func cloneWorkspace(ws *workspace.Workspace) *workspace.Workspace {
	data, err := json.Marshal(ws)
	if err != nil {
		panic(err)
	}
	var out workspace.Workspace
	if err := json.Unmarshal(data, &out); err != nil {
		panic(err)
	}
	return &out
}

// fakeAdapter is a programmable domain adapter that counts what it was asked to
// do, so tests can assert that evaluation never mutates and that a retry does
// not act twice.
type fakeAdapter struct {
	id        string
	ready     bool
	blocked   bool
	options   []StepOption
	evalErr   error
	evals     int
	confirms  int
	onConfirm func()
}

func (a *fakeAdapter) ID() string { return a.id }

func (a *fakeAdapter) Evaluate(ctx context.Context, req StepRequest) (StepReadiness, error) {
	a.evals++
	if a.evalErr != nil {
		return StepReadiness{}, a.evalErr
	}
	readiness := StepReadiness{Ready: a.ready, Blocked: a.blocked, Options: a.options}
	if !a.ready {
		readiness.ErrorCategory = ErrorCategoryNotConfigured
		readiness.Summary = "Not configured yet."
	}
	return readiness, nil
}

func (a *fakeAdapter) Confirm(ctx context.Context, req StepRequest, action StepAction) (StepReadiness, error) {
	a.confirms++
	if a.onConfirm != nil {
		a.onConfirm()
	}
	a.ready = true
	a.blocked = false
	return StepReadiness{Ready: true}, nil
}

func downloadsWizard() *workspace.SetupWizard {
	return &workspace.SetupWizard{
		Version: workspace.SetupWizardSchemaVersion,
		Title:   "Set up Downloads Janitor",
		Steps: []workspace.SetupWizardStep{
			{ID: "folder", Kind: workspace.SetupStepKindDirectory, RequirementKey: "downloads-root", Required: true, Adapter: "downloads_janitor"},
			{ID: "automation", Kind: workspace.SetupStepKindAutomationReview, RequirementKey: "downloads-root", Required: true},
			{ID: "summary", Kind: workspace.SetupStepKindSummary, Required: false},
		},
	}
}

// newTestService wires a workspace whose blueprint declares the wizard above.
func newTestService(t *testing.T, wizard *workspace.SetupWizard, adapters ...Adapter) (*Service, *fakeStore) {
	t.Helper()
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Downloads"})
	ws.ID = "ws-1"
	if wizard != nil {
		ws.SetTemplateProvenance(&workspace.TemplateProvenance{
			TemplateID:             "downloads-janitor",
			TemplateName:           "Downloads Janitor",
			Builtin:                true,
			Version:                2,
			DirectoryRequirements:  []workspace.DirectoryRequirement{{Key: "downloads-root", Label: "Downloads folder", SuggestedPath: "~/Downloads", AccessDisclosure: "Ori lists files here."}},
			AutomationRecipes:      []workspace.AutomationRecipe{{DirectoryKey: "downloads-root", Watch: &workspace.WatchRecipe{Events: []string{"create"}}}},
			CapabilityRequirements: []workspace.CapabilityRequirement{{Key: "calendar"}},
			Plugins:                []string{"reaper-plugin"},
			SetupWizard:            wizard,
		})
	}
	store := &fakeStore{ws: ws}
	registry := NewRegistry()
	for _, adapter := range adapters {
		if err := registry.Register(adapter); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	return NewService(store, registry), store
}

func statusStep(t *testing.T, status Status, id string) StepStatus {
	t.Helper()
	for _, step := range status.Steps {
		if step.ID == id {
			return step
		}
	}
	t.Fatalf("status has no step %q: %+v", id, status.Steps)
	return StepStatus{}
}

func TestService_WorkspaceWithoutWizardIsNotApplicable(t *testing.T) {
	service, store := newTestService(t, nil)

	status, err := service.Status(context.Background(), "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Applicable || status.State != workspace.SetupWizardStateNotApplicable {
		t.Fatalf("expected not_applicable, got %+v", status)
	}
	if status.AutoOpen {
		t.Fatal("a workspace with no wizard must never auto-open one")
	}
	if store.updates != 0 {
		t.Fatalf("reading a wizard-less workspace wrote to it %d times", store.updates)
	}
}

func TestService_FreshWizardResumesAtFirstRequiredStep(t *testing.T) {
	adapter := &fakeAdapter{id: "downloads_janitor"}
	service, _ := newTestService(t, downloadsWizard(), adapter)

	status, err := service.Status(context.Background(), "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Applicable || status.State != workspace.SetupWizardStateNotStarted {
		t.Fatalf("expected not_started, got %q", status.State)
	}
	if !status.AutoOpen {
		t.Fatal("a never-opened, unfinished wizard should auto-open once")
	}
	if status.CurrentStepID != "folder" {
		t.Fatalf("expected to resume at the first required step, got %q", status.CurrentStepID)
	}
	if status.Title != "Set up Downloads Janitor" || status.BlueprintID != "downloads-janitor" {
		t.Fatalf("status is missing blueprint identity: %+v", status)
	}
	folder := statusStep(t, status, "folder")
	if folder.Status != workspace.SetupStepStatusActive {
		t.Fatalf("the resume step should be active, got %q", folder.Status)
	}
	// The requirement travels with the step so the dialog can label it without
	// re-reading a template that may since have changed.
	if folder.DirectoryLabel != "Downloads folder" || folder.DirectorySuggest != "~/Downloads" || folder.DirectoryAccess == "" {
		t.Fatalf("directory requirement not projected: %+v", folder)
	}
	if adapter.confirms != 0 {
		t.Fatal("reading status must not perform any domain action")
	}
}

func TestService_OpenAndDismissChangeNoReadiness(t *testing.T) {
	adapter := &fakeAdapter{id: "downloads_janitor"}
	service, _ := newTestService(t, downloadsWizard(), adapter)
	ctx := context.Background()

	opened, err := service.Open(ctx, "ws-1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened.AutoOpen {
		t.Fatal("once opened, the wizard must not auto-open again")
	}
	if opened.State != workspace.SetupWizardStateInProgress {
		t.Fatalf("opening starts setup, got %q", opened.State)
	}

	dismissed, err := service.Dismiss(ctx, "ws-1")
	if err != nil {
		t.Fatalf("Dismiss: %v", err)
	}
	if !dismissed.Dismissed {
		t.Fatal("dismissal should be recorded")
	}
	if dismissed.Ready() || dismissed.State != workspace.SetupWizardStateInProgress {
		t.Fatalf("closing the dialog must not change readiness, got %q", dismissed.State)
	}
	if dismissed.CompletedAt != nil {
		t.Fatal("dismissal must not record completion")
	}
	if dismissed.AutoOpen {
		t.Fatal("a dismissed wizard must not auto-open on the next load")
	}
	if adapter.confirms != 0 {
		t.Fatal("opening or dismissing must not perform a domain action")
	}

	// Resuming clears the dismissal — the user is looking at it again.
	reopened, err := service.Open(ctx, "ws-1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if reopened.Dismissed {
		t.Fatal("re-opening should clear the dismissal")
	}
	if reopened.CurrentStepID != "folder" {
		t.Fatalf("resume should return to the first unresolved required step, got %q", reopened.CurrentStepID)
	}
}

func TestService_CompletionRequiresServerEvaluatedReadiness(t *testing.T) {
	adapter := &fakeAdapter{id: "downloads_janitor"}
	service, _ := newTestService(t, downloadsWizard(), adapter)
	ctx := context.Background()

	var hookCalls int
	service.SetCompletionHook(func(context.Context, string) { hookCalls++ })

	if _, err := service.Complete(ctx, "ws-1"); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("completing an unfinished wizard should fail, got %v", err)
	}

	// The adapter-backed step passes only when the adapter says so.
	if _, err := service.Confirm(ctx, "ws-1", "folder", StepAction{Type: ActionConfirm}); err != nil {
		t.Fatalf("Confirm folder: %v", err)
	}
	if adapter.confirms != 1 {
		t.Fatalf("expected exactly one domain action, got %d", adapter.confirms)
	}
	mid, err := service.Status(ctx, "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if mid.Ready() {
		t.Fatal("one of two required steps does not make a wizard ready")
	}
	if mid.CurrentStepID != "automation" {
		t.Fatalf("expected to advance to the next required step, got %q", mid.CurrentStepID)
	}
	if hookCalls != 0 {
		t.Fatal("the completion hook must not fire before setup is ready")
	}

	// The automation step has no adapter: the user's recorded approval is the
	// server-side fact.
	if _, err := service.Confirm(ctx, "ws-1", "automation", StepAction{}); err != nil {
		t.Fatalf("Confirm automation: %v", err)
	}
	final, err := service.Complete(ctx, "ws-1")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !final.Ready() || final.CompletedAt == nil {
		t.Fatalf("expected a ready, completed wizard: %+v", final)
	}
	if final.CurrentStepID != "" {
		t.Fatalf("a ready wizard has no outstanding step, got %q", final.CurrentStepID)
	}
	if final.AutoOpen {
		t.Fatal("a completed wizard must not auto-open again")
	}
	if hookCalls != 1 {
		t.Fatalf("the completion hook should fire exactly once, got %d", hookCalls)
	}

	// Re-reading a ready wizard neither re-fires the hook nor re-runs the domain.
	if _, err := service.Status(ctx, "ws-1"); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if hookCalls != 1 {
		t.Fatalf("completion must not be re-announced on every read, got %d", hookCalls)
	}
	if adapter.confirms != 1 {
		t.Fatalf("re-reading status performed %d domain actions", adapter.confirms)
	}
}

func TestService_ConfirmIsRetrySafe(t *testing.T) {
	adapter := &fakeAdapter{id: "downloads_janitor"}
	service, _ := newTestService(t, downloadsWizard(), adapter)
	ctx := context.Background()

	for range 3 {
		if _, err := service.Confirm(ctx, "ws-1", "folder", StepAction{Type: ActionConfirm}); err != nil {
			t.Fatalf("Confirm: %v", err)
		}
	}
	// A retry after a timeout or a page refresh must update the same domain
	// record, not create a second folder binding, watcher, or link. The
	// strongest form of that guarantee is not acting twice at all.
	if adapter.confirms != 1 {
		t.Fatalf("expected one domain action across three confirms, got %d", adapter.confirms)
	}

	status, err := service.Status(ctx, "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	folder := statusStep(t, status, "folder")
	if folder.Status != workspace.SetupStepStatusComplete || folder.CompletedAt == nil {
		t.Fatalf("folder step should stay complete: %+v", folder)
	}
	firstCompletion := *folder.CompletedAt
	if _, err := service.Confirm(ctx, "ws-1", "folder", StepAction{}); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	after, err := service.Status(ctx, "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got := statusStep(t, after, "folder"); !got.CompletedAt.Equal(firstCompletion) {
		t.Fatalf("a repeated confirm rewrote the original completion time: %v -> %v", firstCompletion, got.CompletedAt)
	}
}

func TestService_OptionalStepsSkipAndRequiredStepsCannot(t *testing.T) {
	service, _ := newTestService(t, downloadsWizard(), &fakeAdapter{id: "downloads_janitor", ready: true})
	ctx := context.Background()

	if _, err := service.Skip(ctx, "ws-1", "folder"); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("skipping a required step must fail, got %v", err)
	}
	status, err := service.Skip(ctx, "ws-1", "summary")
	if err != nil {
		t.Fatalf("Skip: %v", err)
	}
	if got := statusStep(t, status, "summary"); got.Status != workspace.SetupStepStatusOptionalSkipped {
		t.Fatalf("optional step should be skipped, got %q", got.Status)
	}
	// A skipped optional step never blocks completion, and stays visible so it
	// can be revisited later.
	if _, err := service.Confirm(ctx, "ws-1", "automation", StepAction{}); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	final, err := service.Complete(ctx, "ws-1")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !final.Ready() {
		t.Fatalf("a skipped optional step must not block completion: %+v", final)
	}
	if got := statusStep(t, final, "summary"); got.Status != workspace.SetupStepStatusOptionalSkipped {
		t.Fatalf("the skipped step should remain skipped, got %q", got.Status)
	}
}

func TestService_ReadinessRegressionMovesToNeedsAttention(t *testing.T) {
	adapter := &fakeAdapter{id: "downloads_janitor"}
	service, _ := newTestService(t, downloadsWizard(), adapter)
	ctx := context.Background()

	var hookCalls int
	service.SetCompletionHook(func(context.Context, string) { hookCalls++ })

	if _, err := service.Confirm(ctx, "ws-1", "folder", StepAction{}); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := service.Confirm(ctx, "ws-1", "automation", StepAction{}); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	ready, err := service.Status(ctx, "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !ready.Ready() {
		t.Fatalf("precondition: expected ready, got %q", ready.State)
	}
	completedAt := ready.CompletedAt

	// The user revokes folder access outside Ori. Nothing is written here; the
	// regression is discovered by looking.
	adapter.ready = false
	adapter.blocked = true

	regressed, err := service.Status(ctx, "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if regressed.State != workspace.SetupWizardStateNeedsAttention {
		t.Fatalf("a regressed requirement should need attention, got %q", regressed.State)
	}
	if regressed.CurrentStepID != "folder" {
		t.Fatalf("repair should focus the affected step, got %q", regressed.CurrentStepID)
	}
	if regressed.AutoOpen {
		t.Fatal("needs_attention must not force the dialog open; the user chooses repair")
	}
	if regressed.CompletedAt == nil || !regressed.CompletedAt.Equal(*completedAt) {
		t.Fatalf("the original completion must survive a regression: %v", regressed.CompletedAt)
	}
	// Valid prior selections are untouched: the second step is still complete.
	if got := statusStep(t, regressed, "automation"); got.Status != workspace.SetupStepStatusComplete {
		t.Fatalf("a regression on one step erased another: %+v", got)
	}
	if got := statusStep(t, regressed, "folder"); got.Status != workspace.SetupStepStatusBlocked {
		t.Fatalf("the regressed step should be blocked, got %q", got.Status)
	}

	// Repair returns it to ready without re-announcing completion.
	adapter.ready = true
	adapter.blocked = false
	repaired, err := service.Status(ctx, "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !repaired.Ready() {
		t.Fatalf("repair should restore ready, got %q", repaired.State)
	}
	if hookCalls != 1 {
		t.Fatalf("repair must not look like a first completion; hook fired %d times", hookCalls)
	}
}

func TestService_UnregisteredAdapterBlocksWithoutRunningAnything(t *testing.T) {
	// The blueprint is valid; this build simply has not wired that adapter.
	service, _ := newTestService(t, downloadsWizard())
	ctx := context.Background()

	status, err := service.Status(ctx, "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	folder := statusStep(t, status, "folder")
	if folder.Status != workspace.SetupStepStatusBlocked {
		t.Fatalf("a step whose adapter is missing must be blocked, got %q", folder.Status)
	}
	if folder.ErrorCategory != ErrorCategoryUnavailable {
		t.Fatalf("expected a stable safe category, got %q", folder.ErrorCategory)
	}
	if _, err := service.Complete(ctx, "ws-1"); err == nil {
		t.Fatal("a blocked required step must prevent completion")
	}
	if _, err := service.Confirm(ctx, "ws-1", "folder", StepAction{}); !errors.Is(err, ErrUnknownAdapter) {
		t.Fatalf("confirming a step with no registered adapter must fail, got %v", err)
	}
}

func TestService_RejectsUnknownStepsAndActions(t *testing.T) {
	service, _ := newTestService(t, downloadsWizard(), &fakeAdapter{id: "downloads_janitor"})
	ctx := context.Background()

	if _, err := service.Confirm(ctx, "ws-1", "made-up", StepAction{}); !errors.Is(err, ErrUnknownStep) {
		t.Fatalf("an undeclared step must be rejected, got %v", err)
	}
	if _, err := service.Skip(ctx, "ws-1", "made-up"); !errors.Is(err, ErrUnknownStep) {
		t.Fatalf("skipping an undeclared step must be rejected, got %v", err)
	}
	if _, err := service.Confirm(ctx, "ws-1", "folder", StepAction{Type: "install_plugin"}); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("an action type outside the allowlist must be rejected, got %v", err)
	}
	if _, err := service.Status(ctx, "missing-workspace"); err == nil {
		t.Fatal("an unknown workspace must be an error, not an empty status")
	}
}

func TestService_UnsupportedSnapshotRunsNothing(t *testing.T) {
	future := downloadsWizard()
	future.Version = workspace.SetupWizardSchemaVersion + 1
	adapter := &fakeAdapter{id: "downloads_janitor", ready: true}
	service, store := newTestService(t, future, adapter)
	ctx := context.Background()

	status, err := service.Status(ctx, "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Applicable || status.Diagnostic == "" {
		t.Fatalf("an unrunnable snapshot must report a diagnostic: %+v", status)
	}
	if status.Ready() {
		t.Fatal("a snapshot this build cannot read must never report ready")
	}
	if len(status.Steps) != 0 {
		t.Fatalf("no step of an unrunnable snapshot may be offered: %+v", status.Steps)
	}
	if adapter.evals != 0 {
		t.Fatalf("an unrunnable snapshot must not reach an adapter, got %d evaluations", adapter.evals)
	}
	if store.updates != 0 {
		t.Fatal("an unrunnable snapshot must not rewrite workspace state")
	}
	if _, err := service.Confirm(ctx, "ws-1", "folder", StepAction{}); !errors.Is(err, ErrUnsupportedSnapshot) {
		t.Fatalf("no action may run against an unrunnable snapshot, got %v", err)
	}
	if _, err := service.Open(ctx, "ws-1"); !errors.Is(err, ErrUnsupportedSnapshot) {
		t.Fatalf("opening an unrunnable snapshot must fail closed, got %v", err)
	}
}

func TestService_SnapshotReferenceMustResolveInsideTheWorkspace(t *testing.T) {
	wizard := downloadsWizard()
	// A hand-edited workspace.json pointing at a folder the blueprint never
	// declared.
	wizard.Steps[0].RequirementKey = "somewhere-else"
	adapter := &fakeAdapter{id: "downloads_janitor", ready: true}
	service, _ := newTestService(t, wizard, adapter)

	status, err := service.Status(context.Background(), "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Diagnostic == "" || len(status.Steps) != 0 {
		t.Fatalf("a reference outside the workspace's own snapshot must fail closed: %+v", status)
	}
	if adapter.evals != 0 {
		t.Fatal("a step referencing undeclared data must not reach its adapter")
	}
}

func TestService_ProgressSurvivesTheSQLiteShapedUpdatePath(t *testing.T) {
	adapter := &fakeAdapter{id: "downloads_janitor"}
	service, store := newTestService(t, downloadsWizard(), adapter)
	ctx := context.Background()

	if _, err := service.Confirm(ctx, "ws-1", "folder", StepAction{}); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	// An unrelated write lands between the two steps, exactly as a task or
	// agent update would in production.
	if err := store.Update("ws-1", func(w *workspace.Workspace) error {
		w.Name = "Renamed"
		return nil
	}); err != nil {
		t.Fatalf("unrelated update: %v", err)
	}
	if _, err := service.Confirm(ctx, "ws-1", "automation", StepAction{}); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	status, err := service.Status(ctx, "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Ready() {
		t.Fatalf("an unrelated write erased setup progress: %+v", status)
	}
	if got := statusStep(t, status, "folder"); got.Status != workspace.SetupStepStatusComplete {
		t.Fatalf("the first approval was lost: %+v", got)
	}
}

func TestRegistry_RejectsDuplicateAndBlankAdapters(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&fakeAdapter{id: "downloads_janitor"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := registry.Register(&fakeAdapter{id: "Downloads_Janitor"}); err == nil {
		t.Fatal("registering the same adapter id twice must fail")
	}
	if err := registry.Register(&fakeAdapter{id: "  "}); err == nil {
		t.Fatal("registering a blank adapter id must fail")
	}
	if err := registry.Register(nil); err == nil {
		t.Fatal("registering a nil adapter must fail")
	}
	if _, ok := registry.Lookup("  DOWNLOADS_JANITOR "); !ok {
		t.Fatal("lookup should normalize the name")
	}
	for _, miss := range []string{"", "   ", "downloads", "internal/setupwizard"} {
		if _, ok := registry.Lookup(miss); ok {
			t.Fatalf("lookup of %q must fail closed", miss)
		}
	}
	var nilRegistry *Registry
	if _, ok := nilRegistry.Lookup("downloads_janitor"); ok {
		t.Fatal("a nil registry resolves nothing")
	}
}

// optionAdapter offers two modes and passes only when one has been chosen —
// REAPER's shape, where the simpler mode is a complete answer rather than a
// half-finished one.
type optionAdapter struct {
	id       string
	confirms int
	lastSeen string
}

func (a *optionAdapter) ID() string { return a.id }

func (a *optionAdapter) Evaluate(_ context.Context, req StepRequest) (StepReadiness, error) {
	a.lastSeen = req.SelectedOption
	readiness := StepReadiness{
		Options: []StepOption{
			{ID: "simple", Label: "Simple", Selected: req.SelectedOption == "simple"},
			{ID: "full", Label: "Full", Selected: req.SelectedOption == "full"},
		},
	}
	switch req.SelectedOption {
	case "simple":
		readiness.Ready = true
		readiness.Summary = "Simple mode."
	case "full":
		readiness.Ready = true
		readiness.Summary = "Full mode."
	default:
		readiness.Summary = "Choose how this should work."
		readiness.ErrorCategory = ErrorCategoryNotConfigured
	}
	return readiness, nil
}

func (a *optionAdapter) Confirm(ctx context.Context, req StepRequest, action StepAction) (StepReadiness, error) {
	a.confirms++
	// The service records the choice; the adapter sees it on the next read.
	return a.Evaluate(ctx, req)
}

func modeWizard() *workspace.SetupWizard {
	return &workspace.SetupWizard{
		Version: workspace.SetupWizardSchemaVersion,
		Title:   "Set up",
		Steps: []workspace.SetupWizardStep{
			{ID: "mode", Kind: workspace.SetupStepKindPluginReadiness, RequirementKey: "reaper-plugin", Required: true, Adapter: "downloads_janitor"},
		},
	}
}

// TestService_RecordsTheChoiceAStepOffers covers the property a mode choice
// needs and readiness alone cannot provide: the user's answer outlives the
// click. Re-deriving it from whatever the domain looks like afterwards is how
// "they chose the simpler path" becomes indistinguishable from "they never
// finished".
func TestService_RecordsTheChoiceAStepOffers(t *testing.T) {
	adapter := &optionAdapter{id: "downloads_janitor"}
	service, store := newTestService(t, modeWizard(), adapter)
	ctx := context.Background()

	before, err := service.Status(ctx, "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	step := statusStep(t, before, "mode")
	if step.Status != workspace.SetupStepStatusActive || len(step.Options) != 2 {
		t.Fatalf("an unanswered choice is the outstanding step: %+v", step)
	}
	if step.SelectedOption != "" {
		t.Fatalf("nothing is chosen yet: %q", step.SelectedOption)
	}

	after, err := service.Confirm(ctx, "ws-1", "mode", StepAction{Type: ActionConfirm, Option: "simple"})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if !after.Ready() {
		t.Fatalf("the chosen mode satisfies the step: %+v", after)
	}
	chosen := statusStep(t, after, "mode")
	if chosen.SelectedOption != "simple" {
		t.Fatalf("the choice was not recorded: %+v", chosen)
	}

	// It survives a plain re-read, and the adapter is told what was chosen
	// rather than having to guess.
	adapter.lastSeen = ""
	reread, err := service.Status(ctx, "ws-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if adapter.lastSeen != "simple" {
		t.Fatalf("the adapter was not given the recorded choice, got %q", adapter.lastSeen)
	}
	if !reread.Ready() || statusStep(t, reread, "mode").SelectedOption != "simple" {
		t.Fatalf("the recorded choice did not survive a re-read: %+v", reread)
	}

	// And it survives the SQLite-shaped write path, like every other part of
	// setup progress.
	if err := store.Update("ws-1", func(w *workspace.Workspace) error {
		w.Name = "Renamed"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	final, err := service.Status(ctx, "ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if statusStep(t, final, "mode").SelectedOption != "simple" {
		t.Fatal("an unrelated write erased the recorded choice")
	}
}

func TestService_ChangingTheChosenOptionReRunsTheAdapter(t *testing.T) {
	adapter := &optionAdapter{id: "downloads_janitor"}
	service, _ := newTestService(t, modeWizard(), adapter)
	ctx := context.Background()

	if _, err := service.Confirm(ctx, "ws-1", "mode", StepAction{Option: "simple"}); err != nil {
		t.Fatal(err)
	}
	confirmsAfterFirst := adapter.confirms

	// Re-confirming the same choice changes nothing: it is already satisfied.
	if _, err := service.Confirm(ctx, "ws-1", "mode", StepAction{Option: "simple"}); err != nil {
		t.Fatal(err)
	}
	if adapter.confirms != confirmsAfterFirst {
		t.Fatalf("re-confirming the same choice acted again: %d -> %d", confirmsAfterFirst, adapter.confirms)
	}

	// Switching to the other one is a real change, and must reach the domain.
	after, err := service.Confirm(ctx, "ws-1", "mode", StepAction{Option: "full"})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.confirms == confirmsAfterFirst {
		t.Fatal("switching modes must reach the adapter")
	}
	if statusStep(t, after, "mode").SelectedOption != "full" {
		t.Fatalf("the new choice was not recorded: %+v", statusStep(t, after, "mode"))
	}
}

// TestStatus_OptionsCrossTheWireInSnakeCase pins the payload's shape. The
// browser reads these keys, and Go's default marshalling would emit its own
// field names — a mismatch that no server-side assertion about StepReadiness
// can see, and that shows up only as an empty choice on screen.
func TestStatus_OptionsCrossTheWireInSnakeCase(t *testing.T) {
	encoded, err := json.Marshal(StepStatus{
		ID:   "mode",
		Kind: workspace.SetupStepKindPluginReadiness,
		Options: []StepOption{{
			ID:          "file_only",
			Label:       "File only",
			Description: "Installs nothing.",
			Selected:    true,
		}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Options []struct {
			ID          string `json:"id"`
			Label       string `json:"label"`
			Description string `json:"description"`
			Selected    bool   `json:"selected"`
		} `json:"options"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded.Options) != 1 {
		t.Fatalf("options = %s", encoded)
	}
	option := decoded.Options[0]
	if option.ID != "file_only" || option.Label != "File only" {
		t.Fatalf("a client cannot read the choice it must echo back: %s", encoded)
	}
	if option.Description != "Installs nothing." || !option.Selected {
		t.Fatalf("option detail did not survive the wire: %s", encoded)
	}
}
