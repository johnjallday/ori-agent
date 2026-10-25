package agenthttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/assistantcontext"
	"github.com/johnjallday/ori-agent/internal/modelview"
)

type modelConversationOwnerStore struct{ *fakeConversationStore }

func (s modelConversationOwnerStore) ReadConversationOwner(ctx context.Context, id string, _ assistantcontext.SaveOwner) (PersonalAssistantConversationRecord, error) {
	return s.Get(ctx, id)
}

func assistantModel(t *testing.T, h *HomeAssistantAskHandler, query string) (int, modelview.Selection) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ModelHandler(w, httptest.NewRequest(http.MethodGet, "/api/home-assistant/model"+query, nil))
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("model inspection must not be cached")
	}
	var got modelview.Selection
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(w.Body.String(), "system_prompt") || strings.Contains(w.Body.String(), "api_key") {
		t.Fatal("private configuration escaped")
	}
	return w.Code, got
}

func TestAssistantModel_ReadOnlyAndAgreesWithConversation(t *testing.T) {
	for _, model := range []string{"sonnet", "", "different-default"} {
		t.Run(model, func(t *testing.T) {
			f := newConversationFixture(t, "hello")
			f.handler.SystemModel = stubSystemModel{provider: "scripted", model: model}
			code, view := assistantModel(t, f.handler, "")
			if code != 200 || view.State != "configured" || view.Source != "default" || view.SettingsHref != "/settings#system-model" {
				t.Fatalf("projection = %d %+v", code, view)
			}
			if len(f.provider.requests) != 0 || len(f.store.sessions) != 0 || f.mutator.writes != 0 {
				t.Fatal("inspection ran work or created a session")
			}
			f.say("Write a greeting", "")
			if len(f.provider.requests) != 1 || view.Provider != f.provider.Name() || view.Model != f.provider.last().Model {
				t.Fatalf("projection %+v disagrees with requests %+v", view, f.provider.requests)
			}
			if model != "" && view.Model != model {
				t.Fatal("model alias was rewritten")
			}
		})
	}
}

func TestAssistantModel_UnavailableAndScopedReads(t *testing.T) {
	for _, tc := range []struct {
		name, provider, model, relationship, query, state string
		code                                              int
	}{
		{"cleared", "", "", "active", "", "unconfigured", 200},
		{"unknown provider", "missing", "old-model", "active", "", "unavailable", 200},
		{"not hired", "scripted", "old-model", "needs_hire", "", "unavailable", 200},
		{"paused relationship still permits conversation", "scripted", "sonnet", "paused", "", "configured", 200},
		{"owned session", "scripted", "sonnet", "active", "?conversation_id=mine", "configured", 200},
		{"foreign session", "scripted", "sonnet", "active", "?conversation_id=foreign", "", 404},
		{"missing session", "scripted", "sonnet", "active", "?conversation_id=missing", "", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newConversationFixture(t)
			f.handler.SetConversationStore(modelConversationOwnerStore{f.store})
			f.context.State = tc.relationship
			f.handler.SystemModel = stubSystemModel{provider: tc.provider, model: tc.model}
			f.store.seed("mine", "hq-owned", "nova-profile-key")
			f.store.seed("foreign", "other-workspace", "nova-profile-key")
			code, view := assistantModel(t, f.handler, tc.query)
			if code != tc.code || view.State != tc.state {
				t.Fatalf("got %d %+v", code, view)
			}
			if view.State != "configured" && (view.Model != "" || view.Provider != "") {
				t.Fatal("unavailable projection advertises an old model")
			}
			if len(f.provider.requests) != 0 || len(f.store.sessions) != 2 || f.store.messageCount() != 0 || f.mutator.writes != 0 {
				t.Fatal("inspection mutated conversation state")
			}
		})
	}
}
