package workspace

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/filecuratorguidance"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/types"
)

func TestCuratorHostContextSurvivesToolboxAndMCPUnavailable(t *testing.T) {
	for _, toolbox := range []bool{false, true} {
		t.Run(map[bool]string{false: "MCP unavailable", true: "empty toolbox"}[toolbox], func(t *testing.T) {
			ws := curatorWorkspace()
			if toolbox {
				assignToolbox(t, ws, "curator", "No skills", nil, nil)
			}
			agents := &resolverAgentStoreStub{agents: map[string]*agent.Agent{"Renamed helper": {Settings: types.Settings{SystemPrompt: "User instructions"}}}}
			s := newTestWorkspaceStore(t, ws)
			r := NewAgentRuntimeResolver(agents, s, nil, nil)
			if toolbox {
				r.mcpRegistry = &runtimeRegistryStub{}
				r.mcpConfigManager = &templateLookupStub{}
			}
			r.SetSkillResolver(&stubSkillResolver{agentSkills: map[string][]ResolvedSkill{"Renamed helper": {{Name: "file-janitor", Prompt: "Imported advice", Enabled: true, Trusted: false}}}})
			resolved, err := r.ResolveAgentForWorkspace("Renamed helper", ws.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(resolved.EffectiveSkills) != 0 || len(resolved.MCPServers) != 0 {
				t.Fatal("guidance became a selectable skill or granted a tool")
			}
			prompt := AppendHostContext("User instructions", resolved.HostContext)
			if strings.Count(prompt, filecuratorguidance.Heading) != 1 || strings.Contains(prompt, "Imported advice") {
				t.Fatal("host context missing, duplicated or replaced")
			}
			if agents.agents["Renamed helper"].Settings.SystemPrompt != "User instructions" {
				t.Fatal("base prompt changed")
			}
			if err := s.Update(ws.ID, func(w *Workspace) error { w.AgentInstances = w.AgentInstances[:1]; return nil }); err != nil {
				t.Fatal(err)
			}
			if got := AppendHostContext("base", resolved.HostContext); got != "base" {
				t.Fatal("cached context survived detachment")
			}
		})
	}
}

func TestCuratorHostContextRejectsAmbiguousAndWrongBindings(t *testing.T) {
	ws := curatorWorkspace()
	ws.AgentInstances = append(ws.AgentInstances, AgentInstance{ID: "other", Name: "Renamed helper", NodeID: "other-node"})
	r := NewAgentRuntimeResolver(&resolverAgentStoreStub{agents: map[string]*agent.Agent{"Renamed helper": {}, "Lead": {}}}, newTestWorkspaceStore(t, ws), nil, nil)
	for _, tc := range []struct {
		name, node string
		want       bool
	}{{"Renamed helper", "", false}, {"Renamed helper", "helper-node", true}, {"Renamed helper", "other-node", false}, {"Renamed helper", "absent", false}, {"Lead", "helper-node", false}} {
		resolved, err := r.ResolveAgentForTask(tc.name, Task{WorkspaceID: ws.ID, AssignedNodeID: tc.node})
		if err != nil {
			t.Fatal(err)
		}
		got := AppendHostContext("", resolved.HostContext)
		if (got != "") != tc.want {
			t.Errorf("%s/%s guidance = %v", tc.name, tc.node, got != "")
		}
	}
}

type curatorFactsStub struct {
	fact  filecuratorguidance.ProtectionFact
	after func()
	calls int
}

func (r *curatorFactsStub) ReadProtection(_ context.Context, _, _ string) (filecuratorguidance.ProtectionFact, error) {
	r.calls++
	if r.after != nil {
		r.after()
	}
	return r.fact, nil
}

func TestCuratorFactsRevalidateCachedAssociationAfterRead(t *testing.T) {
	for _, change := range []string{"detach", "remove", "owner", "ambiguous", "revoked"} {
		t.Run(change, func(t *testing.T) {
			ws := curatorWorkspace()
			s := newTestWorkspaceStore(t, ws)
			r := NewAgentRuntimeResolver(&resolverAgentStoreStub{agents: map[string]*agent.Agent{"Renamed helper": {}}}, s, nil, nil)
			facts := &curatorFactsStub{fact: filecuratorguidance.ProtectionFact{WorkspaceID: ws.ID, RootGeneration: "root", State: "covered", AsOf: time.Now()}}
			facts.after = func() {
				if err := s.Update(ws.ID, func(w *Workspace) error {
					switch change {
					case "detach":
						w.AgentInstances = w.AgentInstances[:1]
					case "remove":
						w.InstalledCapabilities = nil
					case "owner":
						w.OwnerUserID = "other"
					case "ambiguous":
						w.AgentInstances = append(w.AgentInstances, w.AgentInstances[1])
					case "revoked":
						facts.fact.State = "unavailable"
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			r.SetCuratorProtectionReader(facts)
			resolved, err := r.ResolveAgentForWorkspace("Renamed helper", ws.ID, "helper-node")
			if err != nil {
				t.Fatal(err)
			}
			got := AppendHostContext("", resolved.HostContext)
			if strings.Contains(got, "## File Janitor managed-folder protection") {
				t.Fatal("late fact leaked", got)
			}
			if (got != "") != (change == "revoked") {
				t.Fatal("generic guidance incorrectly retained/lost", got)
			}
		})
	}
}

func TestCuratorCapturedTaskRequestsKeepHostAdviceAndRuntimeSkillOverrides(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, toolbox := range []bool{false, true} {
			name := "ordinary"
			if native {
				name = "native"
			}
			if toolbox {
				name += " toolbox"
			}
			t.Run(name, func(t *testing.T) {
				ws := curatorWorkspace()
				ws.AllowNativeMCPCLI = native
				if toolbox {
					assignToolbox(t, ws, "curator", "Empty", nil, nil)
				}
				agents := &resolverAgentStoreStub{agents: map[string]*agent.Agent{"Renamed helper": {Settings: types.Settings{Provider: "codex", Model: "gpt-test", SystemPrompt: "My user-authored instructions for {{agent.name}}", AllowNativeMCPTools: &native}}}}
				s := newTestWorkspaceStore(t, ws)
				r := NewAgentRuntimeResolver(agents, s, nil, nil)
				facts := &curatorFactsStub{fact: filecuratorguidance.ProtectionFact{WorkspaceID: ws.ID, RootGeneration: "root", State: "configured", AsOf: time.Now()}}
				r.SetCuratorProtectionReader(facts)
				if toolbox {
					r.mcpRegistry = &runtimeRegistryStub{}
					r.mcpConfigManager = &templateLookupStub{}
				}
				provider := &fakeNativeProvider{caps: llm.ProviderCapabilities{SupportsNativeMCP: native, SupportsSystemPrompt: true}}
				factory := llm.NewFactory()
				factory.Register("codex", provider)
				h := NewLLMTaskHandler(agents, factory, s)
				h.SetRuntimeResolver(r)
				_, err := h.ExecuteTask(context.Background(), "Renamed helper", Task{ID: "filing-advice", To: "Renamed helper", WorkspaceID: ws.ID, AssignedNodeID: "helper-node", Description: "Explain backup versus undo", RuntimeSkillPrompts: []ResolvedSkill{{Name: "file-janitor", Prompt: "Runtime override advice", Enabled: true, Trusted: false}}})
				if err != nil {
					t.Fatal(err)
				}
				prompt := provider.gotReq.SystemPrompt
				if !provider.called || strings.Count(prompt, filecuratorguidance.Heading) != 1 || !strings.Contains(prompt, "My user-authored instructions") || !strings.Contains(prompt, "Runtime override advice") {
					t.Fatalf("captured prompt missing layered context: %q", prompt)
				}
				if strings.Count(prompt, "## File Janitor managed-folder protection") != 1 || facts.calls != 1 {
					t.Fatal("actual task request did not receive shared managed facts exactly once", prompt)
				}
				if len(provider.gotReq.MCPServers) != 0 {
					t.Fatal("host guidance granted MCP")
				}
			})
		}
	}
}
