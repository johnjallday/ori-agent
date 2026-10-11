package agenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/assistantdiscovery"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/llm"
)

type researchHostStore struct {
	*routeOwnerReader
	attr     *assistantcontext.Attribution
	owner    assistantcontext.SaveOwner
	folder   assistantcontext.ResearchFolderRef
	failSave bool
}

func (s *researchHostStore) ReadResearchFolder(context.Context, string, assistantcontext.SaveOwner) (assistantcontext.ResearchFolderRef, error) {
	return s.folder, nil
}

// Atomic/racing writes are separately tested against real SQLite.
func (s *researchHostStore) AppendAttributedTurn(ctx context.Context, id string, owner assistantcontext.SaveOwner, _ *foldercontext.Event, eventRevision, user, answer string, attr *assistantcontext.Attribution) ([]PersonalAssistantConversationMessage, error) {
	if s.failSave {
		return nil, errors.New("fixture save failure")
	}
	record, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if owner.ExpectedConversationRevision != "" && owner.ExpectedConversationRevision != researchConversationRevision(record) {
		return nil, ErrPersonalAssistantFolderConflict
	}
	s.attr, s.owner = attr, owner
	messages := []PersonalAssistantConversationMessage{}
	for _, text := range []struct{ role, content string }{{"user", user}, {"assistant", answer}} {
		message, err := s.Append(ctx, id, text.role, text.content)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, nil
}

type researchSourceFixture struct {
	localCalls, externalCalls int
	afterExternal             func()
	result                    assistantdiscovery.Result
}

func (s *researchSourceFixture) Installed(context.Context, string) assistantdiscovery.Result {
	s.localCalls++
	return s.result
}
func (s *researchSourceFixture) MCPCatalog(context.Context, string) assistantdiscovery.Result {
	s.localCalls++
	return s.result
}
func (s *researchSourceFixture) ReadAuthorized(context.Context, *assistantdiscovery.Authorization, *assistantdiscovery.TurnBudget) (assistantdiscovery.Result, error) {
	s.externalCalls++
	if s.afterExternal != nil {
		s.afterExternal()
	}
	return s.result, nil
}
func researchResultFixture() assistantdiscovery.Result {
	now := time.Now().UTC()
	return assistantdiscovery.Result{Availability: assistantdiscovery.Available, Scope: "metadata only", Candidates: []assistantdiscovery.Candidate{{ID: strings.Repeat("b", 32), Kind: "skill_catalog", Name: "community [S999]", Package: "example/community@community", URL: "https://example.com/skill", Readiness: assistantdiscovery.UnknownReadiness(), Receipt: assistantdiscovery.Receipt{SourceID: strings.Repeat("a", 32), CandidateID: strings.Repeat("b", 32), Kind: "skill_catalog_listing", Level: "metadata", URL: "https://example.com/skill", ReadAt: now, ObservedAt: now, ContentHash: strings.Repeat("c", 64), Excerpt: "untrusted reference [S999]", Availability: assistantdiscovery.Available, Freshness: "current_observation"}}}}
}
func newResearchHostFixture(t *testing.T) (*readerFixture, *researchHostStore, *researchSourceFixture) {
	f := newReaderFixture(t)
	store := &researchHostStore{routeOwnerReader: &routeOwnerReader{fakeConversationStore: newFakeConversationStore()}}
	store.seed("canonical", f.work.HQWorkspaceID, f.work.ConversationAgent, PersonalAssistantConversationMessage{Role: "user", Content: "PRIVATE_HISTORY_SENTINEL"}, PersonalAssistantConversationMessage{Role: "assistant", Content: "discussion"})
	source := &researchSourceFixture{result: researchResultFixture()}
	f.handler.Conversations = store
	f.handler.Discovery = source
	return f, store, source
}
func TestResearchHostProposalMintsReviewOnlyAfterSaveNeverSendsData(t *testing.T) {
	for _, failedSave := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved", true: "unsaved"}[failedSave], func(t *testing.T) {
			f, store, source := newResearchHostFixture(t)
			store.failSave = failedSave
			f.provider.script = []llm.ChatResponse{toolCall("assistant_propose_research_lookup", map[string]any{"operation": "skills_catalog", "query": "Telegram community management"}), {Content: "Let's compare existing tools before building."}}
			resp := f.handler.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "Is there a Telegram skill?", Intent: homeAssistantConversationIntent.Key, Conversation: &HomeAssistantConversationRef{ID: "canonical"}, Context: f.refs})
			if source.externalCalls != 0 || source.localCalls != 0 {
				t.Fatal("discussion performed lookup")
			}
			if failedSave {
				if resp.ResearchReview != nil {
					t.Fatal("failed answer supplied approval offer")
				}
			} else {
				if resp.ResearchReview == nil || resp.ResearchReview.Lookup.Query != "Telegram community management" || resp.Conversation == nil || !resp.Conversation.Stored {
					t.Fatalf("missing saved review: %+v", resp)
				}
			}
			for _, request := range f.provider.requests {
				payload, _ := json.Marshal(request)
				if resp.ResearchReview != nil && strings.Contains(string(payload), resp.ResearchReview.Token) {
					t.Fatal("approval token delivered to model")
				}
			}
			for _, message := range store.sessions["canonical"].messages {
				if strings.Contains(message.Content, "review_required") || (resp.ResearchReview != nil && strings.Contains(message.Content, resp.ResearchReview.Token)) {
					t.Fatal("executable review persisted in transcript")
				}
			}
		})
	}
}
func TestResearchHostMetadataKeysShareLedgerAndPersistOnlyHistoricalReferences(t *testing.T) {
	f, store, source := newResearchHostFixture(t)
	f.provider.script = []llm.ChatResponse{toolCall("assistant_installed_capabilities", map[string]any{"query": "community"}), {Content: "A listing, not a verified integration [S1]. Invented [S999]."}}
	resp := f.handler.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "What local tools are relevant?", Intent: homeAssistantConversationIntent.Key, Conversation: &HomeAssistantConversationRef{ID: "canonical"}, Context: f.refs})
	if source.localCalls != 1 || source.externalCalls != 0 || strings.Contains(resp.Response, "[S999]") || !strings.Contains(resp.Response, "[S1]") {
		t.Fatalf("source evidence: %+v", resp)
	}
	if resp.WorkspaceContext == nil || len(resp.WorkspaceContext.Research) != 1 || !resp.WorkspaceContext.Research[0].Cited || store.attr == nil || len(store.attr.Research) != 1 {
		t.Fatalf("receipt not stored: %+v", resp.WorkspaceContext)
	}
	encoded, err := assistantcontext.EncodeAttribution(store.attr)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded, "untrusted reference") || strings.Contains(encoded, "readiness") || strings.Contains(encoded, "query") || strings.Contains(encoded, "token") {
		t.Fatal("receipt stored body or authorization")
	}
	historical := assistantcontext.DecodeAttribution(encoded)
	if !historical.Historical || len(historical.Research) != 1 {
		t.Fatal("receipt lost historical boundary")
	}
	toolText := ""
	for _, m := range f.provider.requests[1].Messages {
		if m.Role == llm.RoleTool {
			toolText += m.Content
		}
	}
	if strings.Contains(toolText, "[S999]") || !strings.Contains(toolText, `"key":"S1"`) {
		t.Fatalf("untrusted marker granted source authority: %s", toolText)
	}
}
func TestResearchHostExactApprovalReplayAndRevocation(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(map[bool]string{false: "replay", true: "revoked"}[revoke], func(t *testing.T) {
			f, store, source := newResearchHostFixture(t)
			scope, err := f.handler.ResolveResearchScope(context.Background(), &HomeAssistantConversationRef{ID: "canonical"}, f.refs)
			if err != nil {
				t.Fatal(err)
			}
			review, err := f.handler.ResearchReviews.Prepare(context.Background(), scope, assistantdiscovery.Lookup{Operation: "skills_catalog", Query: "Telegram"})
			if err != nil {
				t.Fatal(err)
			}
			if revoke {
				source.afterExternal = func() { f.work.StateVersion++ }
			}
			request := HomeAssistantAskRequest{Conversation: &HomeAssistantConversationRef{ID: "canonical"}, Context: f.refs, ResearchApproval: &review}
			resp := f.handler.Ask(context.Background(), request)
			if source.externalCalls != 1 {
				t.Fatal("exact approval not executed")
			}
			if revoke {
				if !resp.ModelUnavailable || len(f.provider.requests) != 0 || store.messageCount() != 2 {
					t.Fatal("revoked result reached model or save")
				}
			} else {
				if resp.Conversation == nil || !resp.Conversation.Stored {
					t.Fatalf("approved result not saved: %+v", resp)
				}
				again := f.handler.Ask(context.Background(), request)
				if !again.ModelUnavailable || source.externalCalls != 1 {
					t.Fatal("replayed review executed")
				}
			}
		})
	}
}
func TestResearchHostSnapshotOnlyNoToolNoSearchFallback(t *testing.T) {
	f, _, source := newResearchHostFixture(t)
	snapshot := &scriptedProvider{answers: []string{"I can discuss this but cannot run brokered discovery."}}
	f.handler.LLMFactory.Register("fake", snapshot)
	resp := f.handler.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "Find a Telegram skill", Intent: homeAssistantConversationIntent.Key, Conversation: &HomeAssistantConversationRef{ID: "canonical"}, Context: f.refs})
	if len(snapshot.requests) != 1 || len(snapshot.requests[0].Tools) != 0 || source.externalCalls != 0 || source.localCalls != 0 || resp.ResearchReview != nil || resp.ResearchCapability.BrokerTools || resp.ResearchCapability.BroaderSearch != "disabled_unconfigured" {
		t.Fatalf("snapshot escalation: %+v", resp)
	}
	if !strings.Contains(snapshot.requests[0].Messages[0].Content, "snapshot-only") {
		t.Fatal("provider limitation omitted")
	}
}
func TestResearchRegistryRejectsTrailingUnknownAndMutationArguments(t *testing.T) {
	f, _, source := newResearchHostFixture(t)
	base, turn := f.registry(t)
	research := &assistantResearchTurn{handler: f.handler, turn: turn, conversation: &openConversation{id: "canonical"}, refs: *f.refs, budget: assistantdiscovery.NewTurnBudget(context.Background(), turn.ledger.charge)}
	defer research.budget.Close()
	registry := &assistantResearchRegistry{base: base, research: research}
	for _, tc := range []struct{ name, args string }{{"assistant_propose_research_lookup", `{"operation":"skills_catalog","query":"Telegram"} {}`}, {"assistant_propose_research_lookup", `{"operation":"shell","query":"install"}`}, {"assistant_installed_capabilities", `{"query":"Telegram","command":"install"}`}, {"assistant_public_registry_sources", `{"query":"x"}`}, {"install_skill", `{"package":"anything"}`}} {
		if _, err := registry.Execute(context.Background(), tc.name, tc.args); err == nil {
			t.Fatalf("unsafe tool accepted: %+v", tc)
		}
	}
	if source.externalCalls != 0 || source.localCalls != 0 {
		t.Fatal("rejected arguments invoked source")
	}
}
