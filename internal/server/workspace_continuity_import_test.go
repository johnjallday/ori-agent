package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/continuityprep"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/session"
)

func serveJSON(t *testing.T, handler http.Handler, method, target string, body any) (int, map[string]any) {
	t.Helper()
	return serveJSONWithHeaders(t, handler, method, target, body, nil)
}

func serveJSONWithHeaders(t *testing.T, handler http.Handler, method, target string, body any, headers map[string]string) (int, map[string]any) {
	t.Helper()
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var payload map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &payload)
	return rec.Code, payload
}

func briefRevisionCount(t *testing.T, builder *ServerBuilder, workspaceID string) int {
	t.Helper()
	var n int
	if err := builder.sessionStore.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM daily_brief_revision WHERE workspace_id=?`, workspaceID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// continuitySourceHQ hires Ada and builds her HQ through the canonical
// endpoints on a fresh server, returning that server and the HQ's ID.
func continuitySourceHQ(t *testing.T) (*ServerBuilder, string) {
	t.Helper()
	ctx := context.Background()
	source, handler := newDailyBriefTestServer(t)
	if code, payload := serveJSON(t, handler, http.MethodPost, "/api/personal-assistant/hire", map[string]any{
		"request_id": "continuity-hire", "if_version": 0, "display_name": "Ada", "mandate": "Keep commitments visible.",
		"focus_areas": []string{"plan_my_day"}}); code != http.StatusCreated {
		t.Fatalf("hire: %d %v", code, payload)
	}
	hired, err := source.personalAssistantStore.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if code, payload := serveJSON(t, handler, http.MethodPost, "/api/personal-assistant/hq", map[string]any{
		"request_id": "continuity-hq", "if_version": hired.StateVersion, "name": "My HQ", "timezone": "UTC"}); code != http.StatusCreated {
		t.Fatalf("hq: %d %v", code, payload)
	}
	built, err := source.personalAssistantStore.GetState(ctx, "local")
	if err != nil || built.HQWorkspaceID == "" {
		t.Fatalf("HQ not bound: %+v %v", built, err)
	}
	if source.continuityWorker == nil {
		t.Fatal("continuity preparation is not wired")
	}
	return source, built.HQWorkspaceID
}

// prepareAndCopyFolder waits for the source checkpoint to be Ready and copies
// only that workspace's folder somewhere else, as a user would.
func prepareAndCopyFolder(t *testing.T, source *ServerBuilder, workspaceID string) string {
	t.Helper()
	if status, err := source.continuityWorker.PrepareNow(context.Background(), workspaceID); err != nil || status.State != continuityprep.StateReady {
		t.Fatalf("source not ready: %+v %v", status, err)
	}
	folder, err := source.workspaceFileStore.GetFolderPath(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(t.TempDir(), "transfer", filepath.Base(folder))
	if err := os.MkdirAll(filepath.Dir(copied), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(copied, os.DirFS(folder)); err != nil {
		t.Fatal(err)
	}
	return copied
}

// importOnNewServer starts a different machine (its own HOME, data directory
// and working directory) and imports the copied folder with Import and continue.
func importOnNewServer(t *testing.T, copied string) (*ServerBuilder, http.Handler) {
	t.Helper()
	dest, handler := newDailyBriefTestServer(t)
	code, payload := serveJSON(t, handler, http.MethodGet, "/api/workspaces/import/check?path="+url.QueryEscape(copied), nil)
	review, _ := payload["continuity"].(map[string]any)
	if code != http.StatusOK || review["recommended_action"] != "continue" {
		t.Fatalf("review: %d %v", code, payload)
	}
	code, payload = serveJSON(t, handler, http.MethodPost, "/api/workspaces/import/continuity", map[string]any{
		"path": copied, "tree_digest": review["tree_digest"], "destination_digest": review["destination_digest"], "action": "continue"})
	if code != http.StatusCreated {
		t.Fatalf("import: %d %v", code, payload)
	}
	return dest, handler
}

// Two real servers, one folder between them: the destination adopts the
// assistant through the composed production wiring, and its real Daily Brief
// scheduler generates nothing for the imported HQ — even after the user
// resumes the assistant and turns the schedule on — until routines are
// enabled on this installation.
func TestImportedHQRunsNoBackgroundRoutinesUntilEnabledHere(t *testing.T) {
	ctx := context.Background()
	source, hq := continuitySourceHQ(t)
	if _, err := source.dailyBriefService.UpdateConfig(ctx, dailybrief.Config{WorkspaceID: hq, UserID: "local", Timezone: "UTC",
		ScheduleEnabled: true, ScheduleTime: "00:00", ScheduleDays: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}}); err != nil {
		t.Fatal(err)
	}
	dest, destHandler := importOnNewServer(t, prepareAndCopyFolder(t, source, hq))
	state, err := dest.personalAssistantStore.GetState(ctx, "local")
	if err != nil || state.HQWorkspaceID != hq || state.Status != personalassistant.StatusPaused {
		t.Fatalf("assistant not adopted paused: %+v %v", state, err)
	}
	cfg, err := dest.dailyBriefService.GetConfig(ctx, hq)
	if err != nil || cfg.ScheduleEnabled {
		t.Fatalf("imported schedule must arrive off: %+v %v", cfg, err)
	}

	// The user resumes the assistant and turns the schedule back on — manual
	// choices the imported workspace allows — but routines are still off here.
	resumed := state.Clone()
	resumed.Status = personalassistant.StatusActive
	if _, err := dest.personalAssistantStore.UpdateState(ctx, resumed, state.StateVersion); err != nil {
		t.Fatal(err)
	}
	cfg.ScheduleEnabled = true
	if _, err := dest.dailyBriefService.UpdateConfig(ctx, *cfg); err != nil {
		t.Fatal(err)
	}
	before := briefRevisionCount(t, dest, hq)
	dest.dailyBriefScheduler.Tick()
	if got := briefRevisionCount(t, dest, hq); got != before {
		t.Fatalf("scheduler generated %d brief(s) for an imported workspace with routines off", got-before)
	}
	if ids := dest.automaticWorkspaces([]string{hq}); len(ids) != 0 {
		t.Fatal("imported HQ offered to background catch-up loops")
	}

	// Enabling routines here is the one step that lets the schedule run.
	code, payload := serveJSON(t, destHandler, http.MethodGet, "/api/workspaces/"+hq+"/continuity", nil)
	status, _ := payload["continuity"].(map[string]any)
	if code != http.StatusOK || status["imported"] != true || status["background_allowed"] != false {
		t.Fatalf("status: %d %v", code, payload)
	}
	code, payload = serveJSON(t, destHandler, http.MethodPost, "/api/workspaces/"+hq+"/continuity/activate",
		map[string]any{"enable": true, "version": status["attachment_version"]})
	if code != http.StatusOK {
		t.Fatalf("activate: %d %v", code, payload)
	}
	dest.dailyBriefScheduler.Tick()
	if got := briefRevisionCount(t, dest, hq); got != before+1 {
		t.Fatalf("enabled routines produced %d scheduled brief(s), want exactly one", got-before)
	}
	dest.dailyBriefScheduler.Tick()
	if got := briefRevisionCount(t, dest, hq); got != before+1 {
		t.Fatal("a second tick generated a duplicate brief")
	}
}

// FR-29 through the real server: an older HQ copy (no checkpoint) imported
// with "continue with its assistant" leaves a working, paused assistant — not
// the repair loop an orphaned HQ presentation otherwise produces here.
func TestLegacyHQCopyAdoptsAssistantInsteadOfRepairLoop(t *testing.T) {
	source, hq := continuitySourceHQ(t)
	copied := prepareAndCopyFolder(t, source, hq)
	if err := os.RemoveAll(filepath.Join(copied, ".ori", "continuity")); err != nil {
		t.Fatal(err)
	}
	_, handler := newDailyBriefTestServer(t)
	code, payload := serveJSON(t, handler, http.MethodGet, "/api/workspaces/import/check?path="+url.QueryEscape(copied), nil)
	review, _ := payload["continuity"].(map[string]any)
	if code != http.StatusOK || review["legacy_assistant"] == nil {
		t.Fatalf("legacy HQ not offered: %d %v", code, payload)
	}
	code, payload = serveJSON(t, handler, http.MethodPost, "/api/workspaces/import", map[string]any{"path": copied, "adopt_assistant": true})
	adoption, _ := payload["assistant_adoption"].(map[string]any)
	if code != http.StatusCreated || adoption["adopted"] != true {
		t.Fatalf("legacy import: %d %v", code, payload)
	}
	code, payload = serveJSON(t, handler, http.MethodGet, "/api/personal-assistant", nil)
	assistant, _ := payload["personal_assistant"].(map[string]any)
	if code != http.StatusOK || assistant["state"] != "paused" || assistant["display_name"] != "Ada" {
		t.Fatalf("adopted legacy assistant is not usable: %d %v", code, assistant)
	}
	code, payload = serveJSON(t, handler, http.MethodGet, "/api/personal-hq/status", nil)
	status, _ := payload["status"].(map[string]any)
	if code != http.StatusOK || status["valid"] != true || status["workspace_id"] != hq {
		t.Fatalf("legacy HQ not this installation's HQ: %d %v", code, status)
	}
}

// capturingChatProvider records every request a chat turn sends to the model.
type capturingChatProvider struct {
	mu       sync.Mutex
	requests []llm.ChatRequest
}

func (p *capturingChatProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	return &llm.ChatResponse{Content: "Water the beds in the morning.", Model: "sonnet", Provider: "claude_code"}, nil
}

func (p *capturingChatProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return nil, errors.New("not implemented")
}
func (p *capturingChatProvider) Name() string                            { return "claude_code" }
func (p *capturingChatProvider) Type() llm.ProviderType                  { return llm.ProviderTypeCloud }
func (p *capturingChatProvider) ValidateConfig(llm.ProviderConfig) error { return nil }
func (p *capturingChatProvider) DefaultModels() []string                 { return []string{"sonnet"} }
func (p *capturingChatProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{SupportsSystemPrompt: true}
}

// An imported conversation continues on the new machine: the next explicit
// turn goes to the model with that conversation's own restored history —
// never another conversation's, never a same-agent chat that stayed behind —
// and the reply is saved back into it.
func TestImportedConversationContinuesWithOnlyItsOwnHistory(t *testing.T) {
	ctx := context.Background()
	source, hq := continuitySourceHQ(t)
	ws, err := source.workspaceFileStore.Get(hq)
	if err != nil {
		t.Fatal(err)
	}
	entry := ws.EntryAgentName()
	profile, found, err := source.workspaceFileStore.GetWorkspaceAgent(hq, entry)
	if err != nil || !found {
		t.Fatalf("entry profile %q: %v", entry, err)
	}
	profile.Settings.Provider, profile.Settings.Model = "claude_code", "sonnet"
	if err := source.workspaceFileStore.SaveWorkspaceAgent(hq, entry, profile); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	chat := func(id, folder string, lines ...string) {
		t.Helper()
		if err := source.sessionStore.CreateSession(ctx, &session.Session{ID: id, Title: id, AgentName: entry, FolderID: folder,
			CreatedAt: at, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
		for i, line := range lines {
			role := session.RoleUser
			if i%2 == 1 {
				role = session.RoleAssistant
			}
			if err := source.sessionStore.AddMessage(ctx, id, &session.Message{Role: role, Content: line,
				CreatedAt: at.Add(time.Duration(i) * time.Minute)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	chat("garden-chat", hq, "Where should the beds go?", "Along the south fence.")
	chat("budget-chat", hq, "What did the soil test cost?", "Forty dollars.")
	chat("stayed-behind", "", "A private note that never left the source.", "Understood.")

	dest, destHandler := importOnNewServer(t, prepareAndCopyFolder(t, source, hq))
	if _, err := dest.sessionStore.GetSession(ctx, "stayed-behind"); err == nil {
		t.Fatal("a chat outside the workspace travelled with it")
	}
	capture := &capturingChatProvider{}
	dest.llmFactory.Register("claude_code", capture)
	code, payload := serveJSONWithHeaders(t, destHandler, http.MethodPost, "/api/chat",
		map[string]any{"question": "And when should I water them?"}, map[string]string{"X-Session-ID": "garden-chat"})
	if code != http.StatusOK {
		t.Fatalf("chat: %d %v", code, payload)
	}

	var turn *llm.ChatRequest
	for i := range capture.requests {
		for _, message := range capture.requests[i].Messages {
			if message.Content == "And when should I water them?" {
				turn = &capture.requests[i]
			}
		}
	}
	if turn == nil {
		t.Fatalf("the new turn never reached the model: %d request(s)", len(capture.requests))
	}
	var sent []string
	for _, message := range turn.Messages {
		sent = append(sent, message.Content)
	}
	joined := strings.Join(sent, "\n")
	for _, want := range []string{"Where should the beds go?", "Along the south fence."} {
		if !strings.Contains(joined, want) {
			t.Fatalf("restored history missing %q from the turn: %q", want, sent)
		}
	}
	for _, leaked := range []string{"soil test", "Forty dollars", "never left the source"} {
		if strings.Contains(joined, leaked) {
			t.Fatalf("another conversation leaked into the turn (%q): %q", leaked, sent)
		}
	}
	continued, err := dest.sessionStore.GetSession(ctx, "garden-chat")
	if err != nil || len(continued.Messages) < 4 || !continued.Messages[0].CreatedAt.Equal(at) {
		t.Fatalf("the continued conversation was not saved in place: %+v %v", continued, err)
	}
}
