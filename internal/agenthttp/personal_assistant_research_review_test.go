package agenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/assistantdiscovery"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type researchRelationshipMetadata struct {
	work      *PersonalAssistantWorkContext
	fullReads int
}

func (p *researchRelationshipMetadata) ResolvePersonalAssistantContext(context.Context, string) (*PersonalAssistantWorkContext, error) {
	p.fullReads++
	return nil, errors.New("must not load private Profile/HQ bodies")
}
func (p *researchRelationshipMetadata) ResolvePersonalAssistantRelationship(context.Context, string) (*PersonalAssistantWorkContext, error) {
	copy := *p.work
	return &copy, nil
}

func TestResearchReviewCanonicalOwnerBindingUsesNoTranscriptProfileOrMemoryBodies(t *testing.T) {
	f := newConversationFixture(t)
	resolver, _, _, alpha, _ := workspaceResolverFixture(t)
	f.handler.UserID = "local"
	f.handler.WorkspaceContext = resolver
	metadata := &researchRelationshipMetadata{work: f.context}
	f.handler.PersonalAssistantContext = metadata
	reader := &routeOwnerReader{fakeConversationStore: f.store}
	f.handler.Conversations = reader
	f.store.messagesErr = errors.New("must not materialize history")
	f.store.seed("canonical", f.context.HQWorkspaceID, f.context.ConversationAgent, PersonalAssistantConversationMessage{Role: "user", Content: "PRIVATE_HISTORY_SENTINEL"}, PersonalAssistantConversationMessage{Role: "assistant", Content: "PRIVATE_REPLY_SENTINEL"})
	refs := &HomeAssistantRouteContext{ContextVersion: 1, Origin: "personal_assistant_panel", WorkspaceID: alpha.ID, WorkspaceSlug: alpha.FolderSlug, PagePath: "/workspaces/" + alpha.FolderSlug}
	scope, err := f.handler.ResolveResearchScope(context.Background(), &HomeAssistantConversationRef{ID: "canonical"}, refs)
	if err != nil || scope.Location != alpha.ID || scope.Subject != alpha.ID || scope.HQ != f.context.HQWorkspaceID || scope.Profile != f.context.ConversationAgent || scope.ConversationRevision == "" || metadata.fullReads != 0 {
		t.Fatalf("scope: %+v %v (full reads=%d)", scope, err, metadata.fullReads)
	}
	review, err := f.handler.ResearchReviews.Prepare(context.Background(), scope, assistantdiscovery.Lookup{Operation: "skills_catalog", Query: "Telegram community management"})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(review)
	for _, secret := range []string{"PRIVATE_", alpha.ID, f.context.HQWorkspaceID, f.context.ConversationAgent} {
		if strings.Contains(string(payload), secret) {
			t.Fatalf("scope/private data leaked to review: %s", secret)
		}
	}
	if _, err := f.handler.ResearchReviews.Approve(context.Background(), scope, review); err != nil {
		t.Fatal(err)
	}
	if f.store.nextID != 0 || f.mutator.writes != 0 || len(f.provider.requests) != 0 || metadata.fullReads != 0 || f.store.messageCount() != 2 {
		t.Fatal("review performed model/mutation/transcript work")
	}
}

func TestResearchReviewRejectsUnpersistedForeignChangedConversationAndRevokedWorkspace(t *testing.T) {
	for _, alteration := range []string{"deleted", "foreign conversation", "new thread", "empty thread", "relationship", "HQ", "profile", "repair", "workspace owner", "workspace version", "new canonical turn"} {
		t.Run(alteration, func(t *testing.T) {
			f := newConversationFixture(t)
			resolver, store, _, alpha, _ := workspaceResolverFixture(t)
			f.handler.UserID = "local"
			f.handler.WorkspaceContext = resolver
			f.handler.PersonalAssistantContext = &researchRelationshipMetadata{work: f.context}
			f.handler.Conversations = &routeOwnerReader{fakeConversationStore: f.store}
			f.store.seed("canonical", f.context.HQWorkspaceID, f.context.ConversationAgent, PersonalAssistantConversationMessage{Role: "user", Content: "question"}, PersonalAssistantConversationMessage{Role: "assistant", Content: "answer"})
			refs := &HomeAssistantRouteContext{Origin: "personal_assistant_panel", WorkspaceID: alpha.ID, PagePath: "/workspaces/" + alpha.FolderSlug}
			scope, err := f.handler.ResolveResearchScope(context.Background(), &HomeAssistantConversationRef{ID: "canonical"}, refs)
			if err != nil {
				t.Fatal(err)
			}
			review, err := f.handler.ResearchReviews.Prepare(context.Background(), scope, assistantdiscovery.Lookup{Operation: "public_document", URL: "https://example.com/docs"})
			if err != nil {
				t.Fatal(err)
			}
			switch alteration {
			case "deleted":
				delete(f.store.sessions, "canonical")
			case "foreign conversation":
				f.store.sessions["canonical"].record.WorkspaceID = "foreign"
			case "new thread":
				scope.Conversation = ""
			case "empty thread":
				f.store.sessions["canonical"].record.MessageCount = 0
			case "relationship":
				f.context.StateVersion++
			case "HQ":
				f.context.HQWorkspaceID = "changed"
			case "profile":
				f.context.ConversationAgent = "changed"
			case "repair":
				f.context.State = "repair_needed"
			case "workspace owner":
				if err := store.Update(alpha.ID, func(ws *workspace.Workspace) error { ws.OwnerUserID = "foreign"; return nil }); err != nil {
					t.Fatal(err)
				}
			case "workspace version":
				if err := store.Update(alpha.ID, func(ws *workspace.Workspace) error { ws.Description = "changed"; ws.Version++; return nil }); err != nil {
					t.Fatal(err)
				}
			case "new canonical turn":
				if _, err := f.store.Append(context.Background(), "canonical", "user", "changed question"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.handler.ResearchReviews.Approve(context.Background(), scope, review); err == nil {
				t.Fatal("changed/foreign scope acquired authorization")
			}
		})
	}
}

func TestResearchReviewHTTPValidatesBoundedExactInputAndNeverStartsLookup(t *testing.T) {
	f := newConversationFixture(t)
	resolver, _, _, _, _ := workspaceResolverFixture(t)
	f.handler.UserID = "local"
	f.handler.WorkspaceContext = resolver
	f.handler.PersonalAssistantContext = &researchRelationshipMetadata{work: f.context}
	f.handler.Conversations = &routeOwnerReader{fakeConversationStore: f.store}
	f.store.seed("canonical", f.context.HQWorkspaceID, f.context.ConversationAgent, PersonalAssistantConversationMessage{Role: "user", Content: "question"}, PersonalAssistantConversationMessage{Role: "assistant", Content: "answer"})
	base := `{"conversation":{"id":"canonical"},"context":{"origin":"personal_assistant_panel","page_path":"/settings"},"lookup":{"operation":"skills_catalog","query":"Telegram community management"}}`
	for _, fixture := range []struct {
		body   string
		status int
	}{
		{base, http.StatusOK}, {base + ` {}`, http.StatusBadRequest},
		{strings.Replace(base, `"lookup":`, `"owner":"forged","lookup":`, 1), http.StatusBadRequest},
		{strings.Replace(base, "canonical", "foreign", 1), http.StatusConflict},
		{strings.Replace(base, "Telegram community management", "api_key=secret", 1), http.StatusConflict},
		{strings.Repeat("x", 4097), http.StatusBadRequest},
	} {
		w := httptest.NewRecorder()
		f.handler.ResearchReviewHandler(w, httptest.NewRequest(http.MethodPost, "/api/home-assistant/research/review", strings.NewReader(fixture.body)))
		if w.Code != fixture.status {
			t.Fatalf("HTTP %d wanted %d: %s", w.Code, fixture.status, w.Body.String())
		}
		if fixture.status == http.StatusOK && w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("approval token response is cacheable")
		}
		if f.store.nextID != 0 || f.mutator.writes != 0 || len(f.provider.requests) != 0 || f.store.messageCount() != 2 {
			t.Fatal("review started work")
		}
	}
}
