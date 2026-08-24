package runtimecapability

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type recordingAdapter struct {
	id           string
	durable      DurableResult
	live         LiveResult
	durableErr   error
	liveErr      error
	evaluations  []EvaluationRequest
	liveChecks   []EvaluationRequest
	actions      []ConfirmedActionRequest
	verifies     []VerificationRequest
	verification VerificationResult
	verifyErr    error
}

func (a *recordingAdapter) ID() string { return a.id }
func (a *recordingAdapter) EvaluateDurable(_ context.Context, request EvaluationRequest) (DurableResult, error) {
	a.evaluations = append(a.evaluations, request)
	return a.durable, a.durableErr
}
func (a *recordingAdapter) CheckLive(_ context.Context, request EvaluationRequest) (LiveResult, error) {
	a.liveChecks = append(a.liveChecks, request)
	return a.live, a.liveErr
}
func (a *recordingAdapter) ConfirmAction(_ context.Context, request ConfirmedActionRequest) error {
	a.actions = append(a.actions, request)
	return nil
}
func (a *recordingAdapter) Verify(_ context.Context, request VerificationRequest) (VerificationResult, error) {
	a.verifies = append(a.verifies, request)
	return a.verification, a.verifyErr
}

type runtimeStore struct {
	ws      *workspace.Workspace
	updates int
}

func (s *runtimeStore) GetFolderWorkspace(id string) (*workspace.Workspace, error) {
	if s.ws == nil || s.ws.ID != id {
		return nil, errors.New("workspace not found")
	}
	return s.ws, nil
}

func (s *runtimeStore) Update(id string, mutate func(*workspace.Workspace) error) error {
	if s.ws == nil || s.ws.ID != id {
		return errors.New("workspace not found")
	}
	s.updates++
	return mutate(s.ws)
}

func runtimeWorkspace(contract *workspace.RuntimeRequirementsContract) *workspace.Workspace {
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Runtime"})
	ws.ID = "ws-runtime"
	ws.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID:          "runtime-fixture",
		TemplateName:        "Runtime Fixture",
		RuntimeRequirements: contract,
	})
	return ws
}

func contractWithRequirements(keys ...string) *workspace.RuntimeRequirementsContract {
	requirements := make([]workspace.RuntimeRequirement, 0, len(keys))
	references := make([]string, 0, len(keys))
	for _, key := range keys {
		requirements = append(requirements, workspace.RuntimeRequirement{
			Key:         key,
			Label:       strings.ToUpper(key),
			Description: "Configure " + key + ".",
			Adapter:     key + "_adapter",
		})
		references = append(references, key)
	}
	return &workspace.RuntimeRequirementsContract{
		SchemaVersion: workspace.RuntimeRequirementsSchemaVersion,
		OperatingModes: []workspace.RuntimeOperatingMode{{
			ID:          "assisted",
			Label:       "Assisted",
			Description: "Use the assisted workflow.",
			Requires:    references,
		}},
		Requirements: requirements,
	}
}

func TestRegistryRegistrationAndDeterministicLookup(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(nil); err == nil {
		t.Fatal("nil adapter must be rejected")
	}
	if err := registry.Register(&recordingAdapter{id: "   "}); err == nil {
		t.Fatal("blank adapter ID must be rejected")
	}
	beta := &recordingAdapter{id: " BETA_ADAPTER "}
	alpha := &recordingAdapter{id: "alpha_adapter"}
	if err := registry.Register(beta); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(alpha); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&recordingAdapter{id: "beta_adapter"}); err == nil {
		t.Fatal("duplicate normalized adapter ID must be rejected")
	}
	if got := registry.IDs(); !reflect.DeepEqual(got, []string{"alpha_adapter", "beta_adapter"}) {
		t.Fatalf("registry IDs = %v", got)
	}
	if resolved, ok := registry.Lookup(" BETA_ADAPTER "); !ok || resolved != beta {
		t.Fatalf("normalized lookup = %T, %v", resolved, ok)
	}
	for _, unknown := range []string{"", "missing", "../adapter", "alpha_adapter_extra"} {
		if _, ok := registry.Lookup(unknown); ok {
			t.Errorf("unknown adapter %q resolved", unknown)
		}
	}
}

func TestBuiltinRegistryMatchesAuthoringAllowlist(t *testing.T) {
	registry, err := NewBuiltinRegistry()
	if err != nil {
		t.Fatalf("NewBuiltinRegistry: %v", err)
	}
	if got, want := registry.IDs(), append([]string(nil), projecttemplates.ValidRuntimeRequirementAdapters...); !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime adapter parity changed:\n registry %v\nauthoring %v", got, want)
	}
}

func TestServiceSelectedAndImplicitModes(t *testing.T) {
	adapter := &recordingAdapter{id: "runtime_adapter", durable: DurableResult{State: DurableConfigured, Summary: "Configured."}}
	registry := NewRegistry()
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}

	contract := contractWithRequirements("runtime")
	contract.Requirements[0].Adapter = adapter.ID()
	contract.OperatingModes = append(contract.OperatingModes, workspace.RuntimeOperatingMode{
		ID: "limited", Label: "Limited", Description: "Use files.",
	})
	store := &runtimeStore{ws: runtimeWorkspace(contract)}
	service := NewService(store, registry)

	status, err := service.Status(context.Background(), store.ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Applicable || !status.ModeSelectionRequired || status.SelectedModeID != "" || status.DurableState != DurableNotStarted {
		t.Fatalf("unselected multi-mode status = %+v", status)
	}
	if len(adapter.evaluations) != 0 {
		t.Fatal("requirements must not evaluate before the user chooses a mode")
	}

	selected, err := service.SelectMode(context.Background(), store.ws.ID, " LIMITED ")
	if err != nil {
		t.Fatal(err)
	}
	if selected.SelectedModeID != "limited" || selected.ModeSelectionRequired || selected.DurableState != DurableConfigured || len(selected.Requirements) != 0 {
		t.Fatalf("limited mode status = %+v", selected)
	}
	if got := store.ws.GetRuntimeState(); got == nil || got.SelectedModeID != "limited" {
		t.Fatalf("selected mode was not persisted: %+v", got)
	}
	if _, err := service.SelectMode(context.Background(), store.ws.ID, "missing"); !errors.Is(err, ErrUnknownMode) {
		t.Fatalf("unknown mode error = %v", err)
	}

	// A one-mode contract is implicit and does not force a meaningless choice.
	one := contractWithRequirements("runtime")
	one.Requirements[0].Adapter = adapter.ID()
	oneStore := &runtimeStore{ws: runtimeWorkspace(one)}
	oneService := NewService(oneStore, registry)
	implicit, err := oneService.Status(context.Background(), oneStore.ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if implicit.ModeSelectionRequired || implicit.SelectedModeID != "assisted" || implicit.DurableState != DurableConfigured {
		t.Fatalf("implicit mode status = %+v", implicit)
	}
}

func TestServicePreservesRequirementOrderAndFirstActionableBlocker(t *testing.T) {
	first := &recordingAdapter{id: "first_adapter", durable: DurableResult{
		State: DurableInProgress, ReasonCode: "first_missing", Summary: "Configure the first runtime.",
		Action: &Action{Token: "configure_first", Code: "configure_first", Label: "Configure first"},
	}}
	second := &recordingAdapter{id: "second_adapter", durable: DurableResult{
		State: DurableNeedsAttention, ReasonCode: "second_missing", Summary: "Repair the second runtime.",
		Action: &Action{Token: "repair_second", Code: "repair_second", Label: "Repair second"},
	}}
	registry := NewRegistry()
	if err := registry.Register(second); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(first); err != nil {
		t.Fatal(err)
	}
	store := &runtimeStore{ws: runtimeWorkspace(contractWithRequirements("first", "second"))}
	service := NewService(store, registry)

	status, err := service.Status(context.Background(), store.ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Requirements) != 2 || status.Requirements[0].Key != "first" || status.Requirements[1].Key != "second" {
		t.Fatalf("requirement declaration order changed: %+v", status.Requirements)
	}
	if status.FirstBlocker == nil || status.FirstBlocker.RequirementKey != "first" || status.FirstBlocker.ReasonCode != "first_missing" || status.FirstBlocker.Action == nil || status.FirstBlocker.Action.Code != "configure_first" {
		t.Fatalf("first actionable blocker = %+v", status.FirstBlocker)
	}
	if status.DurableState != DurableInProgress {
		t.Fatalf("overall durable state = %q", status.DurableState)
	}
	if len(first.evaluations) != 1 || len(second.evaluations) != 1 {
		t.Fatalf("all requirements should be evaluated read-only: first=%d second=%d", len(first.evaluations), len(second.evaluations))
	}
	if len(first.actions)+len(second.actions)+len(first.verifies)+len(second.verifies) != 0 {
		t.Fatal("reading status invoked a mutating adapter method")
	}
}

func TestServiceVerificationFailureReturnsActionableTransientStatus(t *testing.T) {
	adapter := &recordingAdapter{
		id: "runtime_adapter",
		durable: DurableResult{
			State: DurableConfigured, VerificationRequired: true,
			ReasonCode: ReasonVerificationRequired, Summary: "Run verification.",
		},
		verification: VerificationResult{
			LiveState: LiveCheckFailed, ReasonCode: "runner_failure", Summary: "The trusted runner did not complete.",
			Action: &Action{Token: "set_up_runner", Code: "set_up_runner", Label: "Check runner setup"},
		},
	}
	registry := NewRegistry()
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	contract := contractWithRequirements("runtime")
	contract.Requirements[0].Adapter = adapter.ID()
	store := &runtimeStore{ws: runtimeWorkspace(contract)}
	service := NewService(store, registry)

	status, err := service.Verify(context.Background(), store.ws.ID, "runtime")
	if err != nil {
		t.Fatalf("an expected verification failure should return status, not an opaque error: %v", err)
	}
	if status.DurableState != DurableInProgress || status.LiveState != LiveCheckFailed || status.FirstBlocker == nil ||
		status.FirstBlocker.ReasonCode != "runner_failure" || status.FirstBlocker.Action == nil || status.FirstBlocker.Action.Code != "set_up_runner" {
		t.Fatalf("verification failure status = %+v", status)
	}
	if state := store.ws.GetRuntimeState(); state == nil || len(state.RequirementStates) != 1 || state.RequirementStates[0].FirstVerifiedAt != nil {
		t.Fatalf("failed verification recorded success: %+v", state)
	}
}

func TestServiceUnknownAdapterAndAdapterErrorsFailClosedAndRedacted(t *testing.T) {
	contract := contractWithRequirements("unknown")
	store := &runtimeStore{ws: runtimeWorkspace(contract)}
	service := NewService(store, NewRegistry())
	status, err := service.Status(context.Background(), store.ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.FirstBlocker == nil || status.FirstBlocker.ReasonCode != ReasonAdapterUnavailable || status.DurableState == DurableConfigured {
		t.Fatalf("unknown adapter did not fail closed: %+v", status)
	}
	if status.FirstBlocker.Summary != ProviderUnavailableMessage || status.FirstBlocker.Action == nil || status.FirstBlocker.Action.Code != "review_plugins" {
		t.Fatalf("generic missing-provider projection = %+v", status.FirstBlocker)
	}

	secret := "/Users/alice/private/project.rpp localhost:8080 token=secret command=1234"
	adapter := &recordingAdapter{id: "unknown_adapter", durableErr: errors.New(secret)}
	registry := NewRegistry()
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	service = NewService(store, registry)
	status, err = service.Status(context.Background(), store.ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	serialized := status.Requirements[0].Summary + " " + status.Requirements[0].ReasonCode
	for _, forbidden := range []string{"/Users/", "8080", "secret", "1234", "project.rpp"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("safe status leaked %q: %+v", forbidden, status)
		}
	}
	if status.Requirements[0].ReasonCode != ReasonCheckFailed || status.Requirements[0].Summary != "This runtime requirement could not be checked." {
		t.Fatalf("adapter error was not safely projected: %+v", status.Requirements[0])
	}
}

func TestServiceLiveStateIsTransientAndCannotRegressConfiguredHistory(t *testing.T) {
	adapter := &recordingAdapter{
		id:      "runtime_adapter",
		durable: DurableResult{State: DurableConfigured, Summary: "Configured."},
		live:    LiveResult{State: LiveOffline, ReasonCode: "app_offline", Summary: "The application is offline."},
	}
	registry := NewRegistry()
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	contract := contractWithRequirements("runtime")
	contract.Requirements[0].Adapter = adapter.ID()
	store := &runtimeStore{ws: runtimeWorkspace(contract)}
	verified := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	store.ws.SetRuntimeState(&workspace.WorkspaceRuntimeState{
		SelectedModeID: "assisted",
		RequirementStates: []workspace.RuntimeRequirementState{{
			RequirementKey:     "runtime",
			ConfigurationState: workspace.RuntimeConfigurationConfigured,
			FirstVerifiedAt:    &verified,
			LastVerifiedAt:     &verified,
		}},
	})
	service := NewService(store, registry)

	status, err := service.Recheck(context.Background(), store.ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.DurableState != DurableConfigured || status.LiveState != LiveOffline || status.FirstVerifiedAt == nil || !status.FirstVerifiedAt.Equal(verified) {
		t.Fatalf("offline live check regressed durable history: %+v", status)
	}
	persisted := store.ws.GetRuntimeState()
	if persisted.RequirementStates[0].ConfigurationState != workspace.RuntimeConfigurationConfigured || persisted.RequirementStates[0].FirstVerifiedAt == nil || !persisted.RequirementStates[0].FirstVerifiedAt.Equal(verified) {
		t.Fatalf("transient result was persisted as regression: %+v", persisted)
	}

	adapter.live = LiveResult{State: LiveAvailable, Summary: "Available now."}
	available, err := service.Recheck(context.Background(), store.ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if available.LiveState != LiveAvailable {
		t.Fatalf("fresh live result = %+v", available)
	}
	// A normal status read does not reuse or persist that connected answer.
	notChecked, err := service.Status(context.Background(), store.ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if notChecked.LiveState != LiveNotChecked {
		t.Fatalf("live availability was persisted/cached as authority: %+v", notChecked)
	}
}
