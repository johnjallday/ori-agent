package agenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// fakeHireProposals is an in-memory HireProposalStore.
type fakeHireProposals struct {
	proposals map[string]personalassistant.HireProposal
	order     []string
	createErr error
	listErr   error
}

func newFakeHireProposals() *fakeHireProposals {
	return &fakeHireProposals{proposals: map[string]personalassistant.HireProposal{}}
}

func (s *fakeHireProposals) Create(_ context.Context, proposal personalassistant.HireProposal) (personalassistant.HireProposal, error) {
	if s.createErr != nil {
		return personalassistant.HireProposal{}, s.createErr
	}
	proposal.CreatedAt, proposal.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	s.proposals[proposal.ID] = proposal
	s.order = append(s.order, proposal.ID)
	return proposal, nil
}

func (s *fakeHireProposals) Get(_ context.Context, id string) (personalassistant.HireProposal, error) {
	proposal, ok := s.proposals[id]
	if !ok {
		return personalassistant.HireProposal{}, personalassistant.ErrHireProposalNotFound
	}
	return proposal, nil
}

func (s *fakeHireProposals) ByConversation(_ context.Context, userID, conversationID string) ([]personalassistant.HireProposal, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	var out []personalassistant.HireProposal
	for _, id := range s.order {
		if p := s.proposals[id]; p.UserID == userID && p.ConversationID == conversationID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *fakeHireProposals) Update(_ context.Context, id string, mutate func(*personalassistant.HireProposal) error) (personalassistant.HireProposal, error) {
	current, ok := s.proposals[id]
	if !ok {
		return personalassistant.HireProposal{}, personalassistant.ErrHireProposalNotFound
	}
	next := current
	if err := mutate(&next); err != nil {
		return personalassistant.HireProposal{}, err
	}
	next.ID, next.UserID, next.ConversationID, next.CreatedAt = current.ID, current.UserID, current.ConversationID, current.CreatedAt
	s.proposals[id] = next
	return next, nil
}

func (s *fakeHireProposals) first() personalassistant.HireProposal {
	if len(s.order) == 0 {
		return personalassistant.HireProposal{}
	}
	return s.proposals[s.order[0]]
}

// rosterNames is a Home agents source that only knows names.
type rosterNames []string

func (r rosterNames) AgentRoster() ([]HomeAgentSummary, bool) {
	out := make([]HomeAgentSummary, 0, len(r))
	for _, name := range r {
		out = append(out, HomeAgentSummary{Name: name})
	}
	return out, true
}

const hireProposalFixture = `{"role":"researcher","name":"Ruth","brief":"Compare the three CRMs we're evaluating and recommend one by Friday.","workspace_name":"CRM comparison"}`

func hireProposalReply(arguments string) llm.ChatResponse {
	return llm.ChatResponse{Content: "Sounds like a job for a Researcher.", ToolCalls: []llm.ToolCall{{Name: hireProposalTool, Arguments: arguments}}}
}

// newHireProposalFixture is the research host fixture with a proposal store,
// which is what turns the hire tool on.
func newHireProposalFixture(t *testing.T) (*readerFixture, *researchHostStore, *researchSourceFixture, *fakeHireProposals) {
	t.Helper()
	f, store, source := newResearchHostFixture(t)
	proposals := newFakeHireProposals()
	f.handler.HireProposals = proposals
	return f, store, source, proposals
}

func askHire(f *readerFixture, prompt string) HomeAssistantAskResponse {
	return f.handler.Ask(context.Background(), HomeAssistantAskRequest{Prompt: prompt, Intent: homeAssistantConversationIntent.Key, Context: f.refs, Conversation: &HomeAssistantConversationRef{ID: "canonical"}})
}

// systemText is the rules the model was given, wherever the provider carries
// them: the request's system prompt or its leading system message.
func systemText(request llm.ChatRequest) string {
	text := request.SystemPrompt
	for _, message := range request.Messages {
		if message.Role == llm.RoleSystem {
			text += "\n" + message.Content
		}
	}
	return text
}

func toolNamesOf(request llm.ChatRequest) []string {
	names := make([]string, 0, len(request.Tools))
	for _, tool := range request.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func TestHireProposalRouteHintIsCurrentExplicitPreparationNotHistoricalConsent(t *testing.T) {
	for _, prompt := range []string{"Prepare a workspace review for this idea", "Please prepare a workspace proposal", "Let's set up a workspace for this plan", "Yes, prepare a workspace review"} {
		if !isAssistantWorkspaceReviewRequest(prompt) {
			t.Fatalf("explicit route hint missed: %q", prompt)
		}
	}
	for _, prompt := range []string{"yes", "use that", "Should we set up a workspace for this?", "Don't prepare a workspace review", `Explain "prepare a workspace review"`, "Prepare a workspace reviewable report"} {
		if isAssistantWorkspaceReviewRequest(prompt) {
			t.Fatalf("discussion became explicit route hint: %q", prompt)
		}
	}
	policy := buildAssistantConversationSystemPrompt(nil)
	for _, rule := range []string{"respect a decline", "hire proposal tool", "do not require a particular phrase", "Hire and start", "Hire an agent for this chip"} {
		if !strings.Contains(policy, rule) {
			t.Fatalf("hire guidance missing %q", rule)
		}
	}
	if strings.Contains(policy, "workspace proposal") || strings.Contains(policy, "workspace review") {
		t.Fatal("the system prompt still pitches the workspace proposal")
	}
}

func TestHireProposalToolReplacesTheWorkspaceProposalOnThePanelPath(t *testing.T) {
	f, _, _, _ := newHireProposalFixture(t)
	f.provider.script = []llm.ChatResponse{{Content: "Here is a short answer."}}
	response := askHire(f, "what is a CRM?")
	if response.Confirmation != nil || len(f.provider.requests) != 1 {
		t.Fatalf("an answer carried a card: %+v", response)
	}
	request := f.provider.requests[0]
	names := strings.Join(toolNamesOf(request), ",")
	if !strings.Contains(names, hireProposalTool) || strings.Contains(names, "assistant_propose_workspace") {
		t.Fatalf("tools offered: %s", names)
	}
	rules := systemText(request)
	if !strings.Contains(rules, hireProposalInstructions) || strings.Contains(rules, "Review workspace setup") {
		t.Fatal("the hire instructions were not placed with the rules, or the old ones remain")
	}
	for _, tool := range request.Tools {
		if tool.Name != hireProposalTool {
			continue
		}
		encoded, _ := json.Marshal(tool.Parameters)
		for _, field := range []string{`"role"`, `"name"`, `"brief"`, `"workspace_name"`, `"domain"`, `"specialist"`, `"additionalProperties":false`} {
			if !strings.Contains(string(encoded), field) {
				t.Fatalf("tool schema lacks %s: %s", field, encoded)
			}
		}
	}
}

func TestHireProposalWithoutAStoreOffersNoToolAndSaysSo(t *testing.T) {
	f, _, _ := newResearchHostFixture(t)
	f.handler.HireProposals = nil
	f.provider.script = []llm.ChatResponse{{Content: "I can help plan that here."}}
	response := askHire(f, "help me plan the product launch for March")
	if response.Confirmation != nil || len(f.provider.requests) != 1 {
		t.Fatalf("no store, yet a card: %+v", response)
	}
	request := f.provider.requests[0]
	if strings.Contains(strings.Join(toolNamesOf(request), ","), hireProposalTool) || !strings.Contains(systemText(request), hireProposalManualInstructions) {
		t.Fatal("the hire tool was offered without a store to keep its proposal, or the manual rules are missing")
	}
}

func TestHireProposalNaturalRequestUsesOneModelTurnAndIsSavedOnTheConversation(t *testing.T) {
	for _, prompt := range []string{"I need to compare the three CRMs we're evaluating and recommend one by Friday", "can we create a workspace for the CRM comparison"} {
		for _, failedSave := range []bool{false, true} {
			f, store, source, proposals := newHireProposalFixture(t)
			store.seed("canonical", f.work.HQWorkspaceID, f.work.ConversationAgent,
				PersonalAssistantConversationMessage{Role: "user", Content: "we are evaluating three CRMs"},
				PersonalAssistantConversationMessage{Role: "assistant", Content: "Which three?"})
			store.failSave = failedSave
			f.provider.script = []llm.ChatResponse{hireProposalReply(hireProposalFixture)}
			response := askHire(f, prompt)
			if source.localCalls != 0 || source.externalCalls != 0 || len(f.provider.requests) != 1 {
				t.Fatal("proposal caused extra model, source or discovery work")
			}
			request := f.provider.requests[0]
			serialized, _ := json.Marshal(request)
			if request.WorkspaceID != "" || request.ExecutionScope != nil || len(request.MCPServers) != 0 || !strings.Contains(string(serialized), "three CRMs") || !strings.Contains(string(serialized), hireProposalTool) {
				t.Fatal("missing canonical history/tool or inherited native authority")
			}
			if failedSave {
				if response.RequiresConfirmation || response.Confirmation != nil || len(proposals.order) != 0 {
					t.Fatal("failed save supplied a card or a saved proposal")
				}
				continue
			}
			if !response.RequiresConfirmation || response.Confirmation == nil || response.Confirmation.ActionType != HomeActionHireAgent || response.Confirmation.ActionID != "hire-agent" || response.Conversation == nil || !response.Conversation.Stored || response.Conversation.ID != "canonical" {
				t.Fatalf("missing canonical hire card: %+v", response)
			}
			if homeMutatingActionTypes[HomeActionHireAgent] {
				t.Fatal("the hire card acquired mutation authority")
			}
			args := response.Confirmation.Arguments
			saved := proposals.first()
			if saved.ID == "" || args["proposal_id"] != saved.ID || saved.ConversationID != "canonical" || saved.MessageID == "" || saved.MessageID != response.Conversation.AssistantMessageID || saved.State != personalassistant.HireProposalOffered || saved.UserID != "local" {
				t.Fatalf("proposal not saved on the stored turn: %+v / %+v", saved, args)
			}
			if args["role"] != "researcher" || args["role_label"] != "Researcher" || args["name"] != "Ruth" || args["workspace_name"] != "CRM comparison" || args["blueprint"] != "research-project" || args["blueprint_label"] != "Research Project" || args["open_roles"] != 2 || args["state"] != "offered" || args["conversation_id"] != "canonical" {
				t.Fatalf("card arguments: %+v", args)
			}
			if !strings.Contains(args["brief"].(string), "recommend one by Friday") {
				t.Fatalf("brief lost: %+v", args)
			}
			catalog, _ := args["catalog"].(map[string]any)
			hire, _ := catalog["hire"].(map[string]any)
			if hire == nil || hire["domain_brief"] == nil || catalog["entries"] == nil {
				t.Fatalf("card lacks the catalog it draws the role selector from: %+v", args["catalog"])
			}
			if !strings.HasPrefix(response.Response, "Sounds like a job for a Researcher.") || !strings.Contains(response.Response, "Offer: hire Ruth, a Researcher") || !strings.Contains(response.Response, "Nothing is hired until you press Hire and start") {
				t.Fatalf("reply text: %q", response.Response)
			}
			for _, message := range store.sessions["canonical"].messages {
				if strings.Contains(message.Content, "hire-agent") || strings.Contains(message.Content, "action_type") || strings.Contains(message.Content, saved.ID) {
					t.Fatal("executable card saved as history")
				}
			}
		}
	}
}

func TestHireProposalValidatesLikeTheHire(t *testing.T) {
	taken := func(name string) bool { return strings.EqualFold(name, "Researcher") }
	for name, tc := range map[string]struct {
		args                                   string
		valid                                  bool
		role, name, domain, brief, workspace   string
		briefPrefix, workspacePrefix, nameWant string
	}{
		"kept when valid":    {args: hireProposalFixture, valid: true, role: "researcher", name: "Ruth", brief: "Compare the three CRMs we're evaluating and recommend one by Friday.", workspace: "CRM comparison"},
		"unknown role":       {args: `{"role":"wizard","name":"Ruth","brief":"Plan it.","workspace_name":"Plan"}`},
		"not JSON":           {args: "not JSON"},
		"null":               {args: "null"},
		"unknown field":      {args: `{"role":"researcher","name":"Ruth","brief":"Plan it.","workspace_name":"Plan","create":true}`},
		"two values":         {args: hireProposalFixture + " {}"},
		"secret in brief":    {args: `{"role":"researcher","name":"Ruth","brief":"use api_key=private-sentinel to log in","workspace_name":"Plan"}`},
		"control character":  {args: `{"role":"researcher","name":"Ruth","brief":"unsafe\u0000text","workspace_name":"Plan"}`},
		"format character":   {args: `{"role":"researcher","name":"Ruth","brief":"unsafe‮text","workspace_name":"Plan"}`},
		"long brief bounded": {args: `{"role":"researcher","name":"Ruth","brief":"` + strings.Repeat("word ", 100) + `","workspace_name":"Plan"}`, valid: true, briefPrefix: "word word", workspace: "Plan"},
		"empty brief":        {args: `{"role":"validator","name":"Rita","brief":"  ","workspace_name":""}`, valid: true, role: "validator", name: "Rita", brief: personalassistant.HireDefaultBrief("validator", ""), workspace: personalassistant.HireWorkspaceName(personalassistant.HireDefaultBrief("validator", ""))},
		"invalid name":       {args: `{"role":"synthesizer","name":"R/u:th","brief":"Write the launch post.","workspace_name":"Launch post"}`, valid: true, role: "synthesizer", name: "Writer", brief: "Write the launch post.", workspace: "Launch post"},
		"reserved name":      {args: `{"role":"synthesizer","name":"hire","brief":"Write the launch post.","workspace_name":"Launch post"}`, valid: true, name: "Writer"},
		"taken name":         {args: `{"role":"researcher","name":"Researcher","brief":"Compare CRMs.","workspace_name":"CRMs"}`, valid: true, name: "Researcher 2"},
		"derived workspace":  {args: `{"role":"analyzer","name":"Ada","brief":"figure out why the checkout funnel drops at the address step","workspace_name":""}`, valid: true, role: "analyzer", workspace: "Figure Out Why the Checkout"},
		"long workspace":     {args: `{"role":"analyzer","name":"Ada","brief":"Look at the funnel.","workspace_name":"` + strings.Repeat("Checkout ", 20) + `"}`, valid: true, workspacePrefix: "Checkout Checkout"},
		"specialist domain":  {args: `{"role":"specialist","name":"Sol","domain":"  tax  law ","brief":"","workspace_name":"Tax"}`, valid: true, role: "specialist", domain: "tax law", brief: "Act as the tax law expert for this workspace."},
		"domain dropped":     {args: `{"role":"researcher","name":"Ruth","domain":"tax","brief":"Compare CRMs.","workspace_name":"CRMs"}`, valid: true, domain: ""},
		"blank workspace":    {args: `{"role":"orchestrator","name":"Cora","brief":"...","workspace_name":""}`, valid: true, role: "orchestrator", workspace: "Cora Workspace"},
	} {
		t.Run(name, func(t *testing.T) {
			plan, ok := decodeHireProposal(tc.args, taken)
			if ok != tc.valid {
				t.Fatalf("valid = %v, want %v (%+v)", ok, tc.valid, plan)
			}
			if !ok {
				return
			}
			if tc.role != "" && string(plan.entry.Slug) != tc.role {
				t.Fatalf("role %q", plan.entry.Slug)
			}
			if tc.name != "" && plan.name != tc.name {
				t.Fatalf("name %q, want %q", plan.name, tc.name)
			}
			if tc.domain != "" && plan.domain != tc.domain || name == "domain dropped" && plan.domain != "" {
				t.Fatalf("domain %q", plan.domain)
			}
			if tc.brief != "" && plan.brief != tc.brief {
				t.Fatalf("brief %q, want %q", plan.brief, tc.brief)
			}
			if tc.briefPrefix != "" && (!strings.HasPrefix(plan.brief, tc.briefPrefix) || len([]rune(plan.brief)) > personalassistant.WorkspaceGoalMaxRunes) {
				t.Fatalf("brief not bounded: %q", plan.brief)
			}
			if tc.workspace != "" && plan.workspaceName != tc.workspace {
				t.Fatalf("workspace %q, want %q", plan.workspaceName, tc.workspace)
			}
			if tc.workspacePrefix != "" && (!strings.HasPrefix(plan.workspaceName, tc.workspacePrefix) || len([]rune(plan.workspaceName)) > personalassistant.HireWorkspaceNameMaxRunes) {
				t.Fatalf("workspace not bounded: %q", plan.workspaceName)
			}
			if plan.blueprint.ID == "" || plan.blueprint.ID != hireBlueprintFor(plan.entry.Slug).ID {
				t.Fatalf("blueprint %+v", plan.blueprint)
			}
		})
	}
}

func TestHireProposalRefusesInvalidAndMixedToolOutputWithoutSavingAnything(t *testing.T) {
	for _, args := range []string{"not JSON", `{"role":"wizard","name":"X","brief":"Plan.","workspace_name":"P"}`, `{"role":"researcher","name":"X","brief":"api_key=private-sentinel","workspace_name":"P"}`} {
		f, store, _, proposals := newHireProposalFixture(t)
		before := store.messageCount()
		f.provider.script = []llm.ChatResponse{hireProposalReply(args)}
		response := askHire(f, "help me compare three CRMs")
		if response.Confirmation != nil || store.messageCount() != before || len(proposals.order) != 0 || !response.ModelUnavailable || response.FailureReason != "invalid_hire_proposal" || response.Conversation == nil || response.Conversation.ID != "canonical" {
			t.Fatalf("invalid proposal was saved or lost failure category/thread: %+v", response)
		}
		if !strings.Contains(response.Response, "Nothing was hired or created") {
			t.Fatalf("degrade copy: %q", response.Response)
		}
	}
	f, _, source, proposals := newHireProposalFixture(t)
	mixed := hireProposalReply(hireProposalFixture)
	mixed.ToolCalls = append(mixed.ToolCalls, llm.ToolCall{Name: "assistant_installed_capabilities", Arguments: `{}`})
	f.provider.script = []llm.ChatResponse{mixed}
	response := askHire(f, "help me compare three CRMs")
	if response.FailureReason != "invalid_hire_proposal" || source.localCalls != 0 || len(proposals.order) != 0 {
		t.Fatal("mixed batch ran a call or saved a proposal")
	}
}

func TestHireProposalSuggestsAFreeNameFromTheRoster(t *testing.T) {
	f, _, _, proposals := newHireProposalFixture(t)
	f.handler.Sources.Agents = rosterNames{"Ruth", "ruth 2"}
	f.provider.script = []llm.ChatResponse{hireProposalReply(hireProposalFixture)}
	response := askHire(f, "help me compare three CRMs")
	if response.Confirmation == nil || response.Confirmation.Arguments["name"] != "Ruth 3" || proposals.first().Name != "Ruth 3" {
		t.Fatalf("taken name kept: %+v", response.Confirmation)
	}
}

func TestHireProposalLeadFallsBackToTheRoleWhenTheModelWroteNothing(t *testing.T) {
	f, _, _, _ := newHireProposalFixture(t)
	reply := hireProposalReply(`{"role":"analyzer","name":"Ada","brief":"Explain the funnel drop.","workspace_name":"Funnel"}`)
	reply.Content = ""
	f.provider.script = []llm.ChatResponse{reply}
	response := askHire(f, "work out why the funnel drops")
	if response.Confirmation == nil || !strings.HasPrefix(response.Response, "This looks like work for an Analyzer.") {
		t.Fatalf("lead: %q", response.Response)
	}
}

func TestHireProposalSavedProposalIsReturnedOnReloadAndToldToTheModel(t *testing.T) {
	f, _, _, proposals := newHireProposalFixture(t)
	f.provider.script = []llm.ChatResponse{hireProposalReply(hireProposalFixture)}
	first := askHire(f, "help me compare three CRMs")
	if first.Confirmation == nil {
		t.Fatalf("no card: %+v", first)
	}
	id := first.Confirmation.Arguments["proposal_id"].(string)
	if _, err := proposals.Update(context.Background(), id, func(p *personalassistant.HireProposal) error {
		p.State = personalassistant.HireProposalDeclined
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// A reload carries the card in its current state, with the catalog.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/home-assistant/conversations/canonical", nil)
	req.SetPathValue("id", "canonical")
	f.handler.ConversationHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("reload %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	cards, _ := body["hire_proposals"].([]any)
	if len(cards) != 1 {
		t.Fatalf("reload cards: %v", body["hire_proposals"])
	}
	card := cards[0].(map[string]any)
	if card["proposal_id"] != id || card["state"] != "declined" || card["message_id"] != first.Conversation.AssistantMessageID || card["role_label"] != "Researcher" || card["tagline"] == "" {
		t.Fatalf("reload card: %v", card)
	}
	catalog, _ := body["hire_catalog"].(map[string]any)
	if catalog == nil || catalog["entries"] == nil {
		t.Fatalf("reload lacks the catalog: %v", body["hire_catalog"])
	}
	if strings.Contains(rr.Body.String(), "prepare_workspace") {
		t.Fatal("the old proposal shape is back")
	}

	// The next turn tells the model what was put aside, in the user message.
	f.provider.script = []llm.ChatResponse{{Content: "Understood, I will not offer that again."}}
	second := askHire(f, "about those CRMs, which is cheapest?")
	if second.Confirmation != nil {
		t.Fatalf("prose became a card: %+v", second)
	}
	last := f.provider.requests[len(f.provider.requests)-1]
	user := last.Messages[len(last.Messages)-1].Content
	if !strings.Contains(user, "Hires already offered in this conversation") || !strings.Contains(user, `Ruth (Researcher) for "CRM comparison"`) || !strings.Contains(user, "put aside by the user") {
		t.Fatalf("declined proposal not told to the model: %q", user)
	}
	if strings.Contains(user, id) {
		t.Fatal("the proposal id reached the model")
	}
}

func TestHireProposalUnreadableHistoryStillOffersTheTool(t *testing.T) {
	f, _, _, proposals := newHireProposalFixture(t)
	proposals.listErr = errors.New("database is locked")
	f.provider.script = []llm.ChatResponse{hireProposalReply(hireProposalFixture)}
	response := askHire(f, "help me compare three CRMs")
	if response.Confirmation == nil || response.Confirmation.ActionType != HomeActionHireAgent {
		t.Fatalf("an unreadable history withheld the hire: %+v", response)
	}
}

func TestHireProposalUnsavedProposalStillPresentsTheCardOnce(t *testing.T) {
	f, _, _, proposals := newHireProposalFixture(t)
	proposals.createErr = errors.New("database is locked")
	f.provider.script = []llm.ChatResponse{hireProposalReply(hireProposalFixture)}
	response := askHire(f, "help me compare three CRMs")
	if response.Confirmation == nil || response.Confirmation.Arguments["proposal_id"] == "" || !response.Conversation.Stored {
		t.Fatalf("a save failure hid the card from the stored reply: %+v", response)
	}
}

type hireProposalMutationProvider struct {
	llm.Provider
	after func(context.Context, llm.ChatRequest)
	err   error
}

func (p *hireProposalMutationProvider) Chat(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	result, err := p.Provider.Chat(ctx, request)
	if p.after != nil {
		p.after(ctx, request)
	}
	if p.err != nil {
		return nil, p.err
	}
	return result, err
}

func TestHireProposalInFlightMutationCannotOfferOrSaveProposal(t *testing.T) {
	for _, mutation := range []string{"append", "delete", "owner", "relationship", "cancel"} {
		t.Run(mutation, func(t *testing.T) {
			f, store, _, proposals := newHireProposalFixture(t)
			// Independent of research wiring: this proposal owns its own pin.
			f.handler.Discovery = nil
			f.provider.script = []llm.ChatResponse{hireProposalReply(hireProposalFixture)}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.handler.LLMFactory.Register("fake", &hireProposalMutationProvider{Provider: f.provider, after: func(context.Context, llm.ChatRequest) {
				switch mutation {
				case "append":
					_, _ = store.Append(ctx, "canonical", "user", "A newer correction.")
				case "delete":
					_ = store.Discard(ctx, "canonical")
				case "owner":
					store.sessions["canonical"].record.AgentName = "Foreign"
				case "relationship":
					f.work.StateVersion++
				case "cancel":
					cancel()
				}
			}})
			response := f.handler.Ask(ctx, HomeAssistantAskRequest{Prompt: "help me compare three CRMs", Intent: homeAssistantConversationIntent.Key, Context: f.refs, Conversation: &HomeAssistantConversationRef{ID: "canonical"}})
			if response.Confirmation != nil || response.RequiresConfirmation || (response.Conversation != nil && response.Conversation.Stored) || len(proposals.order) != 0 {
				t.Fatalf("stale proposal retained a card: %+v", response)
			}
		})
	}
}

func TestHireProposalProviderFailuresAreDistinctAndUseExistingDeadline(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{{context.DeadlineExceeded, "model_timeout"}, {context.Canceled, "request_cancelled"}, {errors.New("private-provider-error"), "provider_unavailable"}} {
		f, store, _, _ := newHireProposalFixture(t)
		before := store.messageCount()
		f.handler.LLMFactory.Register("fake", &hireProposalMutationProvider{Provider: f.provider, err: test.err, after: func(ctx context.Context, _ llm.ChatRequest) {
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < 20*time.Second {
				t.Fatal("proposal still has its old 12-second deadline")
			}
		}})
		response := askHire(f, "help me compare three CRMs")
		if response.FailureReason != test.code || !response.ModelUnavailable || response.Conversation == nil || store.messageCount() != before || strings.Contains(response.Response, "private-provider-error") {
			t.Fatalf("failure not safe/distinct: %+v", response)
		}
	}
}

func TestHireProposalSnapshotOnlyPathDoesNotAddAnotherModelOrTools(t *testing.T) {
	f, _, _, _ := newHireProposalFixture(t)
	snapshot := &scriptedProvider{answers: []string{"The drawer offers a Hire an agent for this chip under this reply."}}
	f.handler.LLMFactory.Register("fake", snapshot)
	response := askHire(f, "help me compare three CRMs")
	if response.Confirmation != nil || len(snapshot.requests) != 1 || len(snapshot.requests[0].Tools) != 0 || !strings.Contains(systemText(snapshot.requests[0]), hireProposalManualInstructions) {
		t.Fatal("snapshot path escalated or added proposal generation")
	}
}

func TestHireProposalProseIsNeverAnExecutableProposal(t *testing.T) {
	f, _, _, proposals := newHireProposalFixture(t)
	f.provider.script = []llm.ChatResponse{{Content: hireProposalFixture}}
	response := askHire(f, "yes")
	if response.Confirmation != nil || len(proposals.order) != 0 {
		t.Fatal("prose became an action")
	}
}

func TestHireProposalViewNamesWhatAHireBecame(t *testing.T) {
	view := hireProposalView(personalassistant.HireProposal{ID: "p", Role: "synthesizer", Name: "Wren", State: personalassistant.HireProposalHired, HireID: "h", AgentName: "Wren", WorkspaceID: "ws"})
	if view["role_label"] != "Writer" || view["hire_id"] != "h" || view["workspace_id"] != "ws" || view["tagline"] == "" {
		t.Fatalf("hired view: %v", view)
	}
	offered := hireProposalView(personalassistant.HireProposal{ID: "p", Role: "researcher", State: personalassistant.HireProposalOffered})
	if _, present := offered["hire_id"]; present {
		t.Fatalf("an offered card names a hire: %v", offered)
	}
	if fmt.Sprint(hireRoleLabel("unknown-role")) != "unknown-role" {
		t.Fatal("unknown role label")
	}
}
