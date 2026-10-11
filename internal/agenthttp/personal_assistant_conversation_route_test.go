package agenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
)

type routeOwnerReader struct {
	*fakeConversationStore
	reads     int
	afterRead func()
}

func (s *routeOwnerReader) ReadConversationOwner(ctx context.Context, id string, owner assistantcontext.SaveOwner) (PersonalAssistantConversationRecord, error) {
	s.reads++
	record, err := s.Get(ctx, id)
	if s.afterRead != nil {
		s.afterRead()
	}
	return record, err
}

func TestRouteConversation_ValidatesReferenceBeforeAnyOtherRouting(t *testing.T) {
	for _, tc := range []struct {
		name, id, workspace, agent, origin, code string
		storeErr                                 error
		status                                   int
	}{
		{name: "canonical", id: "owned", workspace: "hq-a", agent: "nova-profile-key", status: http.StatusOK},
		{name: "foreign workspace", id: "foreign", workspace: "private-project", agent: "nova-profile-key", code: PersonalAssistantConversationOutOfScope, status: http.StatusConflict},
		{name: "foreign agent", id: "foreign", workspace: "hq-a", agent: "Other", code: PersonalAssistantConversationOutOfScope, status: http.StatusConflict},
		{name: "deleted", id: "deleted", code: PersonalAssistantConversationNotFound, status: http.StatusNotFound},
		{name: "unavailable", id: "owned", storeErr: errors.New("private store diagnostic"), code: PersonalAssistantConversationUnavailable, status: http.StatusServiceUnavailable},
		{name: "wrong surface", id: "owned", workspace: "hq-a", agent: "nova-profile-key", origin: "workspace_execution", code: PersonalAssistantConversationOutOfScope, status: http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newConversationFixture(t)
			f.context.HQWorkspaceID = "hq-a"
			reader := &routeOwnerReader{fakeConversationStore: f.store}
			f.handler.Conversations = reader
			if tc.workspace != "" {
				f.store.seed(tc.id, tc.workspace, tc.agent, PersonalAssistantConversationMessage{Role: "user", Content: "PRIVATE_HISTORY_SENTINEL"})
			}
			f.store.getErr = tc.storeErr
			// Fail if a route tries to use the transcript convenience method.
			f.store.messagesErr = errors.New("must not read history while routing")
			route, _ := newHiredRouteHandler(t, "active")
			route.ConversationRoute = f.handler.ValidateRouteConversation
			contextReads := 0
			route.PanelContext = func(context.Context, string, *HomeAssistantRouteContext) (*assistantcontext.Attribution, error) {
				contextReads++
				return nil, nil
			}
			body, err := json.Marshal(map[string]any{"prompt": "Create a workspace called Community", "conversation": map[string]any{"id": tc.id}, "context": map[string]any{"origin": tc.origin}, "history": []string{"FORGED_BROWSER_HISTORY"}})
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			route.RouteHandler(w, httptest.NewRequest(http.MethodPost, "/api/home-assistant/route", strings.NewReader(string(body))))
			if w.Code != tc.status || (tc.code != "" && !strings.Contains(w.Body.String(), tc.code)) {
				t.Fatalf("response %d: %s", w.Code, w.Body.String())
			}
			if tc.code != "" && contextReads != 0 {
				t.Fatal("invalid conversation reached workspace routing")
			}
			if strings.Contains(w.Body.String(), "PRIVATE") || strings.Contains(w.Body.String(), "FORGED") || len(f.provider.requests) != 0 || f.mutator.writes != 0 || f.store.nextID != 0 {
				t.Fatal("route leaked context or performed work")
			}
		})
	}
}

func TestRouteConversation_ChangedOwnerAndUnavailableReaderFailClosed(t *testing.T) {
	for _, change := range []string{"relationship version", "HQ", "profile", "state", "no reader"} {
		t.Run(change, func(t *testing.T) {
			f := newConversationFixture(t)
			f.store.seed("owned", f.context.HQWorkspaceID, f.context.ConversationAgent)
			reader := &routeOwnerReader{fakeConversationStore: f.store}
			reader.afterRead = func() {
				switch change {
				case "relationship version":
					f.context.StateVersion++
				case "HQ":
					f.context.HQWorkspaceID = "replacement"
				case "profile":
					f.context.ConversationAgent = "replacement"
				case "state":
					f.context.State = "repair_needed"
				}
			}
			if change != "no reader" {
				f.handler.Conversations = reader
			}
			if err := f.handler.ValidateRouteConversation(context.Background(), &HomeAssistantConversationRef{ID: "owned"}, homePanelRouteContext()); err == nil {
				t.Fatal("changed or unsupported ownership accepted")
			}
		})
	}
}

func TestRouteConversation_NewReferenceDoesNotInferLatestSession(t *testing.T) {
	f := newConversationFixture(t)
	reader := &routeOwnerReader{fakeConversationStore: f.store}
	f.handler.Conversations = reader
	f.store.seed("latest-foreign", "other", "other")
	for _, ref := range []*HomeAssistantConversationRef{nil, {ID: ""}} {
		if err := f.handler.ValidateRouteConversation(context.Background(), ref, homePanelRouteContext()); err != nil {
			t.Fatal(err)
		}
	}
	if reader.reads != 0 || f.store.nextID != 0 {
		t.Fatal("new route read or created a conversation")
	}
}
