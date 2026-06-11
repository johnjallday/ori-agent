package agenthttp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// --- fakes for the ask handler ---

type fakeProvider struct {
	content string
}

func (f *fakeProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: f.content}, nil
}
func (f *fakeProvider) StreamChat(_ context.Context, _ llm.ChatRequest) (llm.StreamReader, error) {
	return nil, errors.New("unused")
}
func (f *fakeProvider) Name() string           { return "fake" }
func (f *fakeProvider) Type() llm.ProviderType { return llm.ProviderTypeLocal }
func (f *fakeProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{SupportsTools: true}
}
func (f *fakeProvider) ValidateConfig(_ llm.ProviderConfig) error { return nil }
func (f *fakeProvider) DefaultModels() []string                   { return []string{"fake-model"} }

type stubSystemModel struct {
	provider string
	model    string
}

func (s stubSystemModel) GetSystemModel() (string, string) { return s.provider, s.model }

type fakeMutator struct {
	created string
}

func (m *fakeMutator) CreateWorkspace(_ context.Context, name, _ string) (string, string, error) {
	m.created = name
	return "ws-new", "/workspaces/ws-new", nil
}
func (m *fakeMutator) CreateTask(_ context.Context, wsID, _ string) (string, string, error) {
	return "t1", "/workspaces/" + wsID, nil
}
func (m *fakeMutator) StartTask(_ context.Context, wsID, _ string) (string, error) {
	return "/workspaces/" + wsID, nil
}
func (m *fakeMutator) AssignAgent(_ context.Context, wsID, _ string) (string, error) {
	return "/workspaces/" + wsID, nil
}

func newAskHandlerWithProvider(t *testing.T, content string) *HomeAssistantAskHandler {
	t.Helper()
	store := workspace.NewInMemoryStore()
	makeTestWorkspace(t, store, "ws-1", "Alpha", []workspace.Task{
		{Description: "do x", Status: workspace.TaskStatusInProgress, CreatedAt: time.Now()},
	})
	factory := llm.NewFactory()
	factory.Register("fake", &fakeProvider{content: content})
	return NewHomeAssistantAskHandler(
		HomeSnapshotSources{Workspaces: store, Now: time.Now},
		factory,
		stubSystemModel{provider: "fake", model: "fake-model"},
	)
}

func TestAsk_IntrospectionAnswerWithGroundedActions(t *testing.T) {
	h := newAskHandlerWithProvider(t, "You have 1 workspace and 1 active task.")
	resp := h.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "summarize my activity", Intent: "app_introspection"})

	if resp.Response != "You have 1 workspace and 1 active task." {
		t.Errorf("response = %q", resp.Response)
	}
	if resp.SnapshotMeta == nil || resp.SnapshotMeta.WorkspaceCount != 1 {
		t.Fatalf("snapshot meta missing/wrong: %+v", resp.SnapshotMeta)
	}
	foundOpen := false
	for _, a := range resp.Actions {
		if a.RequiresConfirmation {
			t.Errorf("introspection action unexpectedly requires confirmation: %+v", a)
		}
		if a.Type == HomeActionOpenWorkspace && a.Href == "/workspaces/ws-1" {
			foundOpen = true
		}
	}
	if !foundOpen {
		t.Errorf("expected grounded open_workspace action, got %+v", resp.Actions)
	}
}

func TestAsk_ConfirmationRequiredForCreateWorkspace(t *testing.T) {
	h := newAskHandlerWithProvider(t, "irrelevant")
	resp := h.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "create a workspace called Beta", Intent: "app_introspection"})

	if !resp.RequiresConfirmation || resp.Confirmation == nil {
		t.Fatalf("expected confirmation, got %+v", resp)
	}
	if resp.Confirmation.ActionType != HomeActionCreateWorkspace {
		t.Errorf("action type = %q, want %q", resp.Confirmation.ActionType, HomeActionCreateWorkspace)
	}
	if name, _ := resp.Confirmation.Arguments["name"].(string); name != "Beta" {
		t.Errorf("name arg = %v, want Beta", resp.Confirmation.Arguments["name"])
	}
}

func TestAsk_ExecuteConfirmedCreateWorkspace(t *testing.T) {
	h := newAskHandlerWithProvider(t, "irrelevant")
	mut := &fakeMutator{}
	h.SetMutator(mut)

	resp := h.Ask(context.Background(), HomeAssistantAskRequest{
		Intent:          "app_introspection",
		ConfirmedAction: &HomeAction{Type: HomeActionCreateWorkspace, Arguments: map[string]any{"name": "Beta"}},
	})
	if mut.created != "Beta" {
		t.Errorf("CreateWorkspace not called with Beta: %q", mut.created)
	}
	foundOpen := false
	for _, a := range resp.Actions {
		if a.Type == HomeActionOpenWorkspace && a.Href == "/workspaces/ws-new" {
			foundOpen = true
		}
	}
	if !foundOpen {
		t.Errorf("expected open_workspace action after create, got %+v", resp.Actions)
	}
}

func TestAsk_ModelUnavailableGuidesToSettings(t *testing.T) {
	store := workspace.NewInMemoryStore()
	h := NewHomeAssistantAskHandler(HomeSnapshotSources{Workspaces: store, Now: time.Now}, nil, nil)
	resp := h.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "summarize my activity", Intent: "app_introspection"})

	if resp.Response == "" {
		t.Error("expected a graceful message when the model is unavailable")
	}
	foundSettings := false
	for _, a := range resp.Actions {
		if a.Href == "/settings" {
			foundSettings = true
		}
	}
	if !foundSettings {
		t.Errorf("expected a Settings next-step action, got %+v", resp.Actions)
	}
}

type spyTraceEmitter struct {
	traces []HomeAskTrace
}

func (s *spyTraceEmitter) RecordAskOutcome(_ context.Context, t HomeAskTrace) {
	s.traces = append(s.traces, t)
}

func TestAsk_EmitsTelemetry(t *testing.T) {
	h := newAskHandlerWithProvider(t, "ok")
	spy := &spyTraceEmitter{}
	h.SetTraceEmitter(spy)

	h.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "summarize my activity", Intent: "app_introspection"})
	if len(spy.traces) != 1 || spy.traces[0].Outcome != "answered" {
		t.Fatalf("expected one 'answered' trace, got %+v", spy.traces)
	}
	if spy.traces[0].Intent != "app_introspection" || spy.traces[0].Prompt != "summarize my activity" {
		t.Errorf("trace fields not recorded: %+v", spy.traces[0])
	}

	spy2 := &spyTraceEmitter{}
	h.SetTraceEmitter(spy2)
	h.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "create a workspace called Beta", Intent: "app_introspection"})
	if len(spy2.traces) != 1 || spy2.traces[0].Outcome != "confirmation_required" || spy2.traces[0].ConfirmedType != HomeActionCreateWorkspace {
		t.Fatalf("expected confirmation_required trace, got %+v", spy2.traces)
	}
}

func TestNavigateAndOpenSessionAreNotMutating(t *testing.T) {
	for _, typ := range []string{HomeActionNavigate, HomeActionOpenSession, HomeActionOpenWorkspace, HomeActionOpenTask} {
		if homeMutatingActionTypes[typ] {
			t.Errorf("%q must not be a mutating (confirmation-required) action", typ)
		}
	}
	for _, typ := range []string{HomeActionCreateWorkspace, HomeActionCreateTask, HomeActionStartTask} {
		if !homeMutatingActionTypes[typ] {
			t.Errorf("%q must be a mutating action", typ)
		}
	}
}
