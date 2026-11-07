package sessionhttp

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// Explicit opt-in, one paid text call, synthetic metadata only. No credentials,
// raw provider errors, system prompts or model prose are written as evidence.
func TestWorkspaceBuildStaffingLiveClaude(t *testing.T) {
	if os.Getenv("ORI_LIVE_STAFFING") != "1" {
		t.Skip("opt-in live Claude staffing probe")
	}
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Fatal("live staffing probe requires ANTHROPIC_API_KEY through with-keys")
	}
	model := os.Getenv("ORI_LIVE_BUILD_MODEL")
	if model == "" {
		model = "claude-sonnet-5-5"
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ORI_DATA_DIR", home)
	f := newBuildFixture(t, false)
	f.catalog = []WorkspaceBuildCatalogEntry{readyEntry(projecttemplates.Template{
		ID: "music-planning", Name: "Music Planning", Description: "Coordinate music portfolio priorities, release milestones and deliverables without DAW or filesystem access.",
		Agents: []projecttemplates.AgentSpec{{Name: "Portfolio Manager", Role: "orchestrator"}},
	})}
	if err := f.handler.agentStore.CreateAgent("Portfolio Manager", &store.CreateAgentConfig{}); err != nil {
		t.Fatal(err)
	}
	for name, purpose := range map[string]string{
		"Scout":             "Coordinates music portfolio priorities, release milestones, blockers and deliverable handoffs. Does not control audio software.",
		"Portfolio Manager": "Reviews tax filings, accounting ledgers and personal investment portfolios. Does not coordinate music projects.",
	} {
		if err := f.handler.agentStore.UpdateAgent(name, func(a *agent.Agent) error {
			a.Settings.Provider, a.Settings.Model = "anthropic", model
			a.Metadata = &types.AgentMetadata{Description: purpose}
			a.Role = types.AgentRole("orchestrator")
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	f.handler.workspaceBuild.ProfileModelConfigured = func(provider, selected string) bool { return provider == "anthropic" && selected == model }
	provider := &oneCallStaffingProvider{Provider: llm.NewClaudeProvider(llm.ClaudeProviderConfig(key, os.Getenv("ANTHROPIC_WORKSPACE_ID")))}
	f.handler.workspaceBuild.ResolveModel = func(context.Context, string) (WorkspaceBuildModel, error) {
		return WorkspaceBuildModel{Provider: provider, ProviderName: "anthropic", Model: model}, nil
	}
	before := len(f.handler.agentStore.ListAgents())
	session := f.start(t)
	status, body := f.turn(t, session, map[string]any{"text": "Plan a music portfolio and recommend a saved agent for the manager role from its declared purpose. Only prepare the draft; do not create or assign anything."})
	if status != http.StatusOK {
		t.Fatalf("live turn status %d (response omitted)", status)
	}
	got := sessionOf(t, body)
	if provider.calls != 1 || got.Staffing == nil || got.Staffing.State != rosterstaffing.Recommended || len(got.Staffing.Roles) != 1 || len(got.Staffing.Roles[0].Choices) == 0 || got.Staffing.Roles[0].Choices[0].Name != "Scout" {
		state := "absent"
		if got.Staffing != nil {
			state = got.Staffing.State
		}
		t.Fatalf("live ranking contract failed: calls=%d state=%s (prose omitted)", provider.calls, state)
	}
	if len(f.handler.agentStore.ListAgents()) != before || len(got.Draft.RoleStaffing) != 0 || got.CreateNow {
		t.Fatal("advisory live probe changed staffing")
	}
	t.Logf("provider=anthropic model=%s calls=%d synthetic_profiles=%d recommended=Scout evidence_refs=%d no_assignment=true", model, provider.calls, before, len(got.Staffing.Roles[0].Choices[0].Reasons))
}

type oneCallStaffingProvider struct {
	llm.Provider
	calls int
}

func (p *oneCallStaffingProvider) Chat(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	if p.calls != 0 || !request.Isolated || len(request.Tools) != 0 || request.MaxTokens > 2000 {
		return nil, errors.New("live staffing call budget or isolation refused")
	}
	p.calls++
	return p.Provider.Chat(ctx, request)
}
