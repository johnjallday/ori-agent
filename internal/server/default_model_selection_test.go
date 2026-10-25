package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/modelview"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

func TestDefaultModel_ConversationUsesDefaultWhileBuildKeepsProfileFirst(t *testing.T) {
	f := newConversationServerFixture(t)
	cfg := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
	if err := cfg.SetSystemModel("claude_code", "conversation-alias"); err != nil {
		t.Fatal(err)
	}
	f.handler.SystemModel = cfg
	st, err := store.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAgent("Atlas", &store.CreateAgentConfig{Model: "sonnet", LLMProvider: "claude_code"}); err != nil {
		t.Fatal(err)
	}
	factory := llm.NewFactory()
	factory.Register("claude_code", f.provider)

	read := func() modelview.Selection {
		rr := httptest.NewRecorder()
		f.handler.ModelHandler(rr, httptest.NewRequest(http.MethodGet, "/api/home-assistant/model", nil))
		if rr.Code != http.StatusOK {
			t.Fatal(rr.Body.String())
		}
		var view modelview.Selection
		if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		return view
	}
	if got := read(); got.Model != "conversation-alias" {
		t.Fatalf("projection = %+v", got)
	}
	if len(f.provider.requests) != 0 {
		t.Fatal("model metadata invoked provider")
	}
	if result := f.say("say hello", ""); result.Conversation == nil || !result.Conversation.Stored {
		t.Fatalf("conversation: %+v", result)
	}
	if got := f.provider.requests[len(f.provider.requests)-1].Model; got != "conversation-alias" {
		t.Fatalf("actual conversational request = %q", got)
	}
	build, err := resolveWorkspaceBuildModel(st, cfg, factory, "Atlas")
	if err != nil || build.Model != "sonnet" {
		t.Fatalf("build=%+v err=%v", build, err)
	}
	if err := cfg.SetSystemModel("", ""); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.State != "unconfigured" || got.Model != "" {
		t.Fatalf("cleared = %+v", got)
	}
	build, err = resolveWorkspaceBuildModel(st, cfg, factory, "Atlas")
	if err != nil || build.Model != "sonnet" {
		t.Fatalf("build changed after clear: %+v %v", build, err)
	}
	profile, _ := st.GetAgent("Atlas")
	if profile.Settings.Model != "sonnet" {
		t.Fatal("conversation metadata changed profile")
	}
}

func TestDefaultModel_ProductionMetadataRejectsStaleCachedOwner(t *testing.T) {
	f := newConversationServerFixture(t)
	ctx := context.Background()
	chat := &session.Session{AgentName: "Atlas", FolderID: f.hq}
	if err := f.sessions.CreateSession(ctx, chat); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sessions.GetSession(ctx, chat.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sessions.DB().ExecContext(ctx, `UPDATE sessions SET agent_name='Other' WHERE id=?`, chat.ID); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	f.handler.ModelHandler(rr, httptest.NewRequest(http.MethodGet, "/api/home-assistant/model?conversation_id="+chat.ID, nil))
	if rr.Code == http.StatusOK {
		t.Fatalf("cached foreign owner advertised a model: %s", rr.Body.String())
	}
	if len(f.provider.requests) != 0 {
		t.Fatal("inspection invoked provider")
	}
}
