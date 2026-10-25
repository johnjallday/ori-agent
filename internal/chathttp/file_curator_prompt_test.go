package chathttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/filecuratorguidance"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type capturedCuratorProtection struct{ calls int }

func (r *capturedCuratorProtection) ReadProtection(_ context.Context, owner, id string) (filecuratorguidance.ProtectionFact, error) {
	r.calls++
	return filecuratorguidance.ProtectionFact{WorkspaceID: id, RootGeneration: "root", State: "configured", AsOf: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}, nil
}

func TestCuratorAdviceReachesActualChatProviderRequests(t *testing.T) {
	for _, providerName := range []string{"claude", "codex", "claude_code"} {
		for _, scenario := range []string{"associated", "same-name skill", "unrelated", "ambiguous", "detached after resolution", "removed after resolution"} {
			t.Run(providerName+"/"+scenario, func(t *testing.T) {
				base := &agent.Agent{Settings: types.Settings{Provider: providerName, Model: "test-model", SystemPrompt: "USER AUTHORED PROMPT"}}
				agents := newPreflightStore("My helper", base)
				s := workspace.NewInMemoryStore()
				ws := &workspace.Workspace{ID: "curator-chat", OwnerUserID: "local", AgentInstances: []workspace.AgentInstance{{ID: "lead", Name: "Someone else", NodeID: "lead", EntryPoint: true}, {ID: "helper", Name: "My helper", NodeID: "helper"}}, InstalledCapabilities: []workspace.InstalledCapability{{ID: workspace.CapabilityFileJanitor, Version: 1, Source: workspace.InstallSourceInPlace, InstalledAt: time.Now(), OwnedResources: []workspace.CapabilityResource{{Kind: workspace.ResourceCompanionAgent, ID: "helper", Shared: true}}}}}
				if scenario == "unrelated" {
					ws.InstalledCapabilities[0].OwnedResources = nil
				}
				if scenario == "ambiguous" {
					ws.AgentInstances = append(ws.AgentInstances, workspace.AgentInstance{ID: "other", Name: "My helper", NodeID: "other"})
				}
				if err := s.Save(ws); err != nil {
					t.Fatal(err)
				}
				r := workspace.NewAgentRuntimeResolver(agents, s, nil, nil)
				protection := &capturedCuratorProtection{}
				r.SetCuratorProtectionReader(protection)
				h := NewHandler(agents, nil)
				h.SetRuntimeResolver(r)
				provider := &scriptedChatProvider{name: providerName, responses: []llm.ChatResponse{{Content: "Filing is not a backup."}}}
				factory := llm.NewFactory()
				factory.Register(providerName, provider)
				h.SetLLMFactory(factory)
				resolved, err := h.resolveEffectiveAgent("My helper", normalizedChatRouteContext{WorkspaceID: ws.ID})
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "same-name skill" {
					resolved.EffectiveSkills = []workspace.ResolvedSkill{{Name: "file-janitor", Prompt: "UNTRUSTED IMPORT", Enabled: true, Trusted: false}}
				}
				if scenario == "detached after resolution" || scenario == "removed after resolution" {
					if err := s.Update(ws.ID, func(w *workspace.Workspace) error {
						if scenario == "detached after resolution" {
							w.AgentInstances = w.AgentInstances[:1]
						} else {
							w.InstalledCapabilities = nil
						}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}
				request := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
				response := httptest.NewRecorder()
				if providerName == "claude" {
					h.handleClaudeChat(response, request, resolved, "Explain filing protection", nil, "My helper", context.Background(), nil, nil, nil, "")
				} else {
					h.handleCLIChat(response, request, providerName, resolved, "Explain filing protection", nil, "My helper", context.Background(), nil, nil, nil, "", normalizedChatRouteContext{WorkspaceID: ws.ID})
				}
				if len(provider.requests) != 1 {
					t.Fatalf("requests=%d response=%s", len(provider.requests), response.Body.String())
				}
				prompt := provider.requests[0].SystemPrompt
				want := 0
				if scenario == "associated" || scenario == "same-name skill" {
					want = 1
				}
				if strings.Count(prompt, "## File Janitor managed-folder protection") != want || protection.calls != want {
					t.Fatal("role-scoped managed facts missing or leaked", prompt, protection.calls)
				}
				if strings.Count(prompt, filecuratorguidance.Heading) != want || !strings.Contains(prompt, "USER AUTHORED PROMPT") {
					t.Fatalf("incorrect layered prompt: %q", prompt)
				}
				if scenario == "same-name skill" && (resolved.EffectiveSkills[0].Trusted || !strings.Contains(prompt, "UNTRUSTED IMPORT")) {
					t.Fatal("host guidance changed imported skill selection or trust")
				}
				if base.Settings.SystemPrompt != "USER AUTHORED PROMPT" || len(provider.requests[0].Tools) != 0 {
					t.Fatal("host guidance mutated shared prompt or granted tools")
				}
			})
		}
	}
}
