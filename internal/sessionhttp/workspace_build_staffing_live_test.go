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
	noFit := os.Getenv("ORI_LIVE_STAFFING_CASE") == "no_fit"
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
	purposes := map[string]string{
		"Scout":             "Coordinates music portfolio priorities, release milestones, blockers and deliverable handoffs. Does not control audio software.",
		"Portfolio Manager": "Reviews tax filings, accounting ledgers and personal investment portfolios. Does not coordinate music projects.",
	}
	if noFit {
		purposes["Scout"] = "Records physical warehouse inventories, invoices and stocktake dates. Does not coordinate music projects."
	}
	for name, purpose := range purposes {
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
	message := "Use the Music Planning blueprint and recommend a saved agent for its manager role from declared purpose. Only prepare the draft; do not create or assign anything."
	if noFit {
		message += " If no saved agent fits music coordination, report no suitable match and keep this blueprint's team undecided; do not switch blueprints or invent a saved fit."
	}
	status, body := f.turn(t, session, map[string]any{"text": message})
	if status != http.StatusOK {
		t.Fatalf("live turn status %d (response omitted)", status)
	}
	got := sessionOf(t, body)
	valid := got.Staffing != nil && len(got.Staffing.Roles) == 1
	if valid && noFit {
		valid = got.Staffing.State == rosterstaffing.NoSuitableMatch && len(got.Staffing.Roles[0].Choices) == 0
	} else if valid {
		valid = got.Staffing.State == rosterstaffing.Recommended && len(got.Staffing.Roles[0].Choices) > 0 && got.Staffing.Roles[0].Choices[0].Name == "Scout"
	}
	if provider.calls != 1 || !valid {
		state := "absent"
		if got.Staffing != nil {
			state = got.Staffing.State
		}
		t.Fatalf("live ranking contract failed: calls=%d state=%s expected_blueprint=%t reply_decoded=%t proposal_roles=%d proposal_state=%s (prose omitted)", provider.calls, state, got.Draft.TemplateID == "music-planning", provider.replyDecoded, provider.proposalRoles, provider.proposalState)
	}
	if len(f.handler.agentStore.ListAgents()) != before || len(got.Draft.RoleStaffing) != 0 || got.CreateNow {
		t.Fatal("advisory live probe changed staffing")
	}
	if noFit {
		t.Logf("provider=anthropic model=%s calls=%d synthetic_profiles=%d state=no_suitable_match choices=0 no_assignment=true", model, provider.calls, before)
	} else {
		t.Logf("provider=anthropic model=%s calls=%d synthetic_profiles=%d recommended=Scout evidence_refs=%d no_assignment=true", model, provider.calls, before, len(got.Staffing.Roles[0].Choices[0].Reasons))
	}
}

type oneCallStaffingProvider struct {
	llm.Provider
	calls         int
	replyDecoded  bool
	proposalRoles int
	proposalState string
}

func (p *oneCallStaffingProvider) Chat(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	if p.calls != 0 || !request.Isolated || len(request.Tools) != 0 || request.MaxTokens > 2000 {
		return nil, errors.New("live staffing call budget or isolation refused")
	}
	p.calls++
	response, err := p.Provider.Chat(ctx, request)
	if err == nil && response != nil {
		if reply, decodeErr := decodeBuildReply(response.Content); decodeErr == nil {
			p.replyDecoded = true
			p.proposalRoles = len(reply.Staffing.Roles)
			if p.proposalRoles > 0 {
				switch state := reply.Staffing.Roles[0].State; state {
				case rosterstaffing.Recommended, rosterstaffing.NeedsChoice, rosterstaffing.NoSuitableMatch, rosterstaffing.Unavailable, rosterstaffing.Stale:
					p.proposalState = state
				default:
					p.proposalState = "unsupported"
				}
			}
		}
	}
	return response, err
}
