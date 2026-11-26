package sessionhttp

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
)

// A Curator added by "Add a Curator" runs on the system model, as a hired
// template agent does. Created with no model, it got the store's fallback (a
// model with no provider), which chat and Filing suggestions refuse to run.
func TestCompanionAgent_IsCreatedOnTheSystemModel(t *testing.T) {
	cases := []struct {
		name                            string
		reader                          SystemModelReader
		wantProvider, wantModel, effort string
	}{
		{name: "system model set", reader: fakeSystemModelReader{provider: "claude", model: "fixture-system"},
			wantProvider: "claude", wantModel: "fixture-system"},
		{name: "a Codex system model carries its reasoning setting",
			reader:       fakeSystemModelReader{provider: "codex", model: "fixture-codex", reasoningEffort: "high"},
			wantProvider: "codex", wantModel: "fixture-codex", effort: "high"},
		{name: "no system model leaves the store default", reader: fakeSystemModelReader{}},
		{name: "no reader leaves the store default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := createTestHandler(t)
			defer cleanup()
			h.SetSystemModelReader(tc.reader)
			ws := &session.Workspace{Name: "Downloads", Kind: session.WorkspaceKindWorkspace, FolderSlug: "downloads"}
			if err := h.store.CreateWorkspace(context.Background(), ws); err != nil {
				t.Fatal(err)
			}

			id, created, err := NewCapabilityCompanionProvisioner(h).EnsureCompanionAgent(ws.ID, "Downloads Curator")
			if err != nil || id == "" || !created {
				t.Fatalf("EnsureCompanionAgent = %q, %v, %v", id, created, err)
			}
			saved, ok := h.agentStore.GetAgent("Downloads Curator")
			if !ok {
				t.Fatal("the Curator was not created")
			}
			got := saved.Settings
			if got.Provider != tc.wantProvider || got.Model != tc.wantModel || got.ReasoningEffort != tc.effort {
				t.Fatalf("Curator settings = provider %q model %q effort %q, want %q %q %q",
					got.Provider, got.Model, got.ReasoningEffort, tc.wantProvider, tc.wantModel, tc.effort)
			}
		})
	}
}

// An agent that already exists under the Curator's name is the user's: adding
// a Curator adopts it and never rewrites its model.
func TestCompanionAgent_AdoptsAnExistingAgentWithoutChangingItsModel(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	h.SetSystemModelReader(fakeSystemModelReader{provider: "claude", model: "fixture-system"})
	if err := h.agentStore.CreateAgent("Downloads Curator", &agentstore.CreateAgentConfig{Model: "fixture-own", LLMProvider: "ollama"}); err != nil {
		t.Fatal(err)
	}
	ws := &session.Workspace{Name: "Downloads", Kind: session.WorkspaceKindWorkspace, FolderSlug: "downloads"}
	if err := h.store.CreateWorkspace(context.Background(), ws); err != nil {
		t.Fatal(err)
	}
	if _, created, err := NewCapabilityCompanionProvisioner(h).EnsureCompanionAgent(ws.ID, "Downloads Curator"); err != nil || created {
		t.Fatalf("adopting an existing agent: created %v, err %v", created, err)
	}
	saved, _ := h.agentStore.GetAgent("Downloads Curator")
	if saved.Settings.Provider != "ollama" || saved.Settings.Model != "fixture-own" {
		t.Fatalf("the user's agent was changed: %+v", saved.Settings)
	}
}
