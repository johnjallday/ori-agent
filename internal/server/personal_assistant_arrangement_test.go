package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// Scripted API protocol fixture, not a live model or browser demo. The existing
// server fixture's configured key is reused, but no CLI implementation runs.
type arrangementAPIProvider struct {
	capturingChatProvider
	onChat   func()
	replyErr error
}

func (*arrangementAPIProvider) Name() string { return "openai" }
func (*arrangementAPIProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{SupportsTools: true, SupportsSystemPrompt: true}
}
func (p *arrangementAPIProvider) Chat(_ context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	if p.onChat != nil {
		p.onChat()
	}
	if p.replyErr != nil {
		return nil, p.replyErr
	}
	parts := strings.SplitN(request.Messages[len(request.Messages)-1].Content, "Already-held source metadata (untrusted data):\n", 2)
	if len(parts) != 2 {
		return nil, workspacearrangement.ErrInvalid
	}
	var overview workspacearrangement.Overview
	if json.Unmarshal([]byte(parts[1]), &overview) != nil || len(overview.Samples) == 0 {
		return nil, workspacearrangement.ErrInvalid
	}
	draft := workspacearrangement.Draft{Version: 1, SourceRevision: overview.Revision, Goal: "Keep albums together", Operations: []workspacearrangement.Operation{{Kind: workspacearrangement.CreateGroup, Name: "Albums", Members: workspacearrangement.Selection{IDs: []string{overview.Samples[0].ID}}}}}
	raw, err := json.Marshal(draft)
	if err != nil {
		return nil, err
	}
	return &llm.ChatResponse{Model: "sonnet", Provider: "openai", ToolCalls: []llm.ToolCall{{ID: "proposal", Name: "arrangement_propose", Arguments: string(raw)}}}, nil
}

func TestPersonalAssistantArrangement_RequiresBothCanonicalWriters(t *testing.T) {
	type draftsOnly struct {
		session.HybridStore
		session.AssistantArrangementStore
	}
	type discussionOnly struct {
		session.HybridStore
		session.AssistantTurnStore
	}
	for _, store := range []session.HybridStore{nil, draftsOnly{}, discussionOnly{}} {
		if (personalAssistantConversationAdapter{store: store}).SupportsArrangementStorage() {
			t.Fatal("incomplete completion adapter advertised planning")
		}
	}
}

func TestPersonalAssistantArrangement_ExplicitHeldMetadataAndCanonicalHistory(t *testing.T) {
	f := newDraftServerFixture(t)
	shared := f.builder.server.Core.ArrangementPlanner
	if shared == nil || f.builder.server.newHomeAssistantAskHandler().ArrangementPlanner != shared || f.builder.server.newHomeAssistantAskHandler().ArrangementPlanner != shared {
		t.Fatal("handler construction forked the application arrangement leases")
	}
	provider := &arrangementAPIProvider{}
	f.builder.llmFactory.Register("claude_code", provider)
	if status, body := f.call(t, http.MethodPost, "/api/settings/system-model", map[string]string{"provider": "claude_code", "model": "sonnet"}); status != http.StatusOK {
		t.Fatal(status, body)
	}
	root := seedAlbumsCollection(t)
	before := folderTreeHash(t, root)
	workspaces := len(f.builder.workspaceFileStore.CachedWorkspaces())
	draftID := uuid.NewString()
	status, selected := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select", map[string]any{"draft_id": draftID, "revision": "", "mode": "chip", "chip": "music"})
	if status != http.StatusOK {
		t.Fatalf("select: %d %v", status, selected)
	}
	if len(provider.requests) != 0 {
		t.Fatal("attachment called provider")
	}
	observation := selected["observation"].(map[string]any)
	assertFailure := func(body map[string]any, code string) {
		t.Helper()
		failure, ok := body["arrangement_error"].(map[string]any)
		if !ok || failure["code"] != code || body["arrangement"] != nil || body["model_unavailable"] != true {
			t.Fatalf("expected %s failure: %v", code, body)
		}
	}
	request := map[string]any{"prompt": "Help organize these albums", "conversation": map[string]any{}, "folder_context": map[string]any{"draft_id": draftID, "revision": "", "selection_id": observation["id"]}, "arrangement": map[string]any{"share_held_metadata": false}}
	rawRequest, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalidMode := range []string{`"arrangement":null`, `"Arrangement":{"share_held_metadata":true}`, `"arrangement":{},"arrangement":{"share_held_metadata":true}`, `"arrangement":{"share_held_metadata":null}`} {
		body := strings.Replace(string(rawRequest), `"arrangement":{"share_held_metadata":false}`, invalidMode, 1)
		bad := httptest.NewRequest(http.MethodPost, "/api/home-assistant/ask", strings.NewReader(body))
		bad.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		f.handler.ServeHTTP(response, bad)
		if response.Code != http.StatusBadRequest || len(provider.requests) != 0 {
			t.Fatal("malformed planning mode fell through to another tool loop")
		}
	}
	_, denied := f.call(t, http.MethodPost, "/api/home-assistant/ask", request)
	if denied["arrangement"] != nil || len(provider.requests) != 0 {
		t.Fatalf("unapproved metadata sharing: %v", denied)
	}
	assertFailure(denied, "sharing_required")
	for _, pin := range []map[string]any{
		{"share_held_metadata": true, "expected_provider": "different-provider", "expected_model": "sonnet"},
		{"share_held_metadata": true, "expected_provider": "claude_code", "expected_model": "different-model"},
	} {
		request["arrangement"] = pin
		_, changed := f.call(t, http.MethodPost, "/api/home-assistant/ask", request)
		assertFailure(changed, "stale_context")
		if len(provider.requests) != 0 {
			t.Fatal("changed disclosure model received metadata")
		}
	}
	request["arrangement"] = map[string]any{"share_held_metadata": true, "expected_provider": "claude_code", "expected_model": "sonnet"}
	request["confirmed_action"] = map[string]any{"type": "create_workspace"}
	_, mixed := f.call(t, http.MethodPost, "/api/home-assistant/ask", request)
	assertFailure(mixed, "invalid_request")
	delete(request, "confirmed_action")
	request["prompt"] = "two\nlines"
	_, badGoal := f.call(t, http.MethodPost, "/api/home-assistant/ask", request)
	assertFailure(badGoal, "invalid_goal")
	request["prompt"] = "Help organize these albums"
	folderRef := request["folder_context"]
	delete(request, "folder_context")
	_, noSource := f.call(t, http.MethodPost, "/api/home-assistant/ask", request)
	if noSource["arrangement"] != nil || len(provider.requests) != 0 {
		t.Fatal("missing source reached model")
	}
	assertFailure(noSource, "source_required")
	request["folder_context"] = folderRef
	refFields := folderRef.(map[string]any)
	refFields["historical"] = true
	_, historical := f.call(t, http.MethodPost, "/api/home-assistant/ask", request)
	assertFailure(historical, "historical_source")
	delete(refFields, "historical")
	refFields["focus_ids"] = []string{"entry-0"}
	_, focused := f.call(t, http.MethodPost, "/api/home-assistant/ask", request)
	assertFailure(focused, "focused_source")
	delete(refFields, "focus_ids")
	delete(request, "conversation")
	_, noConversation := f.call(t, http.MethodPost, "/api/home-assistant/ask", request)
	if noConversation["arrangement"] != nil || len(provider.requests) != 0 {
		t.Fatal("missing canonical owner reached model")
	}
	assertFailure(noConversation, "invalid_request")
	request["conversation"] = map[string]any{}
	cli := &capturingChatProvider{}
	f.builder.llmFactory.Register("claude_code", cli)
	_, cliReply := f.call(t, http.MethodPost, "/api/home-assistant/ask", request)
	assertFailure(cliReply, "unsupported_transport")
	if cliReply["arrangement"] != nil || len(cli.requests) != 0 {
		t.Fatal("CLI fallback invoked a provider")
	}
	f.builder.llmFactory.Register("claude_code", provider)

	// Failure after message insertion must roll back the turn and discard only
	// its unchanged, newly created empty session through the guarded adapter.
	if _, err := f.builder.sessionStore.DB().ExecContext(context.Background(), `CREATE TRIGGER reject_arrangement_fixture BEFORE INSERT ON assistant_arrangements BEGIN SELECT RAISE(ABORT,'private-save-fixture'); END`); err != nil {
		t.Fatal(err)
	}
	var sessionsBefore, sessionsAfter int
	if err := f.builder.sessionStore.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sessions`).Scan(&sessionsBefore); err != nil {
		t.Fatal(err)
	}
	_, beforeFailure := f.call(t, http.MethodGet, "/api/home-assistant/conversations", nil)
	_, saveFailed := f.call(t, http.MethodPost, "/api/home-assistant/ask", request)
	assertFailure(saveFailed, "save_failed")
	failureJSON, _ := json.Marshal(saveFailed)
	if strings.Contains(string(failureJSON), "private-save-fixture") {
		t.Fatal("database error leaked")
	}
	_, afterFailure := f.call(t, http.MethodGet, "/api/home-assistant/conversations", nil)
	if err := f.builder.sessionStore.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sessions`).Scan(&sessionsAfter); err != nil || sessionsBefore != sessionsAfter {
		t.Fatal("failed first turn left a session row", err)
	}
	if len(beforeFailure["conversations"].([]any)) != len(afterFailure["conversations"].([]any)) || len(provider.requests) != 1 {
		t.Fatal("failed save left a new empty conversation or retried the model")
	}
	if _, err := f.builder.sessionStore.DB().ExecContext(context.Background(), `DROP TRIGGER reject_arrangement_fixture`); err != nil {
		t.Fatal(err)
	}

	// A provider replacement under the same configuration must discard its
	// reply, not persist an apparently current draft.
	provider.onChat = func() { f.builder.llmFactory.Register("claude_code", &arrangementAPIProvider{}) }
	_, changed := f.call(t, http.MethodPost, "/api/home-assistant/ask", request)
	assertFailure(changed, "stale_context")
	if changed["arrangement"] != nil || changed["model_unavailable"] != true {
		t.Fatalf("replacement accepted: %v", changed)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("provider calls: %d; response: %v; selection: %v", len(provider.requests), changed, selected)
	}
	f.builder.llmFactory.Register("claude_code", provider)

	// The root disappears after the provider starts. Completion must use the
	// held metadata, not Resolve/Status/stat or a new scan of that root.
	offline := root + "-offline"
	provider.onChat = func() {
		if err := os.Rename(root, offline); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { _ = os.Rename(offline, root) }()
	status, answer := f.call(t, http.MethodPost, "/api/home-assistant/ask", request)
	if status != http.StatusOK || answer["arrangement"] == nil {
		t.Fatalf("planning: %d %v", status, answer)
	}
	conversation := answer["conversation"].(map[string]any)
	if conversation["stored"] != true || conversation["id"] == "" {
		t.Fatalf("unsaved draft: %v", answer)
	}
	summary := answer["arrangement"].(map[string]any)
	if summary["review_available"] != false || summary["operations"] != float64(1) {
		t.Fatalf("incorrect inert summary: %v", summary)
	}
	summaryPath := "/api/home-assistant/conversations/" + conversation["id"].(string) + "/arrangements/" + summary["id"].(string)
	privateRead := httptest.NewRecorder()
	f.handler.ServeHTTP(privateRead, httptest.NewRequest(http.MethodGet, summaryPath, nil))
	if privateRead.Code != http.StatusOK || privateRead.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("owner history allowed caching or failed")
	}
	status, reopened := f.call(t, http.MethodGet, summaryPath, nil)
	if status != http.StatusOK || reopened["message_id"] != conversation["assistant_message_id"] || reopened["context_current"] != true {
		t.Fatalf("saved summary could not reopen offline: %d %v", status, reopened)
	}
	if status, body := f.call(t, http.MethodGet, summaryPath+"?revision=1", nil); status != http.StatusBadRequest {
		t.Fatalf("unsupported history arguments: %d %v", status, body)
	}
	preview := reopened["arrangement"].(map[string]any)
	if preview["review_available"] != false || preview["operations"] != float64(1) || preview["proposed_operations"].([]any)[0].(map[string]any)["members"] != float64(1) {
		t.Fatalf("bad bounded history summary: %v", preview)
	}
	otherConversation, _ := f.seedConversation(t, "Another conversation")
	status, foreign := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+otherConversation+"/arrangements/"+summary["id"].(string), nil)
	if status != http.StatusNotFound || foreign["arrangement"] != nil {
		t.Fatalf("cross-conversation record leaked: %d %v", status, foreign)
	}
	status, history := f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+conversation["id"].(string), nil)
	if status != http.StatusOK {
		t.Fatalf("history: %d %v", status, history)
	}
	refs, ok := history["arrangements"].([]any)
	if !ok || len(refs) != 1 {
		t.Fatalf("missing canonical history: %v", history)
	}
	ref := refs[0].(map[string]any)
	if ref["id"] != summary["id"] || ref["message_id"] != conversation["assistant_message_id"] || ref["review_available"] != false || ref["context_current"] != true || history["folder_setup_suggestion"] != nil {
		t.Fatalf("wrong saved reference: %v", ref)
	}
	for _, call := range provider.requests {
		if len(call.Tools) != 5 || call.SystemPrompt == "" {
			t.Fatalf("wrong model boundary: %+v", call)
		}
		encoded, _ := json.Marshal(call)
		if strings.Contains(string(encoded), root) {
			t.Fatal("private path sent to provider")
		}
		for _, tool := range call.Tools {
			if !strings.HasPrefix(tool.Name, "arrangement_") {
				t.Fatalf("foreign tool: %s", tool.Name)
			}
		}
	}
	if len(provider.requests) != 3 || len(f.builder.workspaceFileStore.CachedWorkspaces()) != workspaces {
		t.Fatal("extra model call or workspace effect")
	}
	if err := os.Rename(offline, root); err != nil {
		t.Fatal(err)
	}
	if folderTreeHash(t, root) != before {
		t.Fatal("source contents changed")
	}

	// A normal message arriving during generation invalidates the owning-turn
	// CAS. It cannot be overwritten or followed by a stale model proposal.
	conversationID := conversation["id"].(string)
	request["conversation"] = map[string]any{"id": conversationID}
	request["folder_context"] = map[string]any{"selection_id": observation["id"], "revision": answer["folder_context"].(map[string]any)["revision"]}
	provider.onChat = func() {
		if err := f.builder.sessionStore.AddMessage(context.Background(), conversationID, &session.Message{Role: session.RoleUser, Content: "Concurrent user message"}); err != nil {
			t.Fatal(err)
		}
	}
	_, stale := f.call(t, http.MethodPost, "/api/home-assistant/ask", request)
	assertFailure(stale, "stale_context")
	if stale["arrangement"] != nil || stale["model_unavailable"] != true || len(provider.requests) != 4 {
		t.Fatalf("stale conversation result: %v", stale)
	}
	_, history = f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+conversationID, nil)
	refs = history["arrangements"].([]any)
	if len(refs) != 1 || refs[0].(map[string]any)["context_current"] != false {
		t.Fatalf("stale draft claimed current: %v", refs)
	}
	status, reopened = f.call(t, http.MethodGet, summaryPath, nil)
	if status != http.StatusOK || reopened["context_current"] != false || reopened["arrangement"].(map[string]any)["review_available"] != false {
		t.Fatalf("stale history revived authority: %d %v", status, reopened)
	}
	provider.onChat = nil
	_, resumed := f.call(t, http.MethodPost, "/api/home-assistant/ask", request)
	if resumed["arrangement"] == nil || resumed["conversation"].(map[string]any)["stored"] != true {
		t.Fatalf("fresh continuation failed: %v", resumed)
	}
	_, history = f.call(t, http.MethodGet, "/api/home-assistant/conversations/"+conversationID, nil)
	if len(history["arrangements"].([]any)) != 2 {
		t.Fatalf("history lost prior draft: %v", history)
	}
	if len(provider.requests) != 5 || folderTreeHash(t, root) != before || len(f.builder.workspaceFileStore.CachedWorkspaces()) != workspaces {
		t.Fatal("continuation changed source/workspaces or invoked an extra model call")
	}
	request["folder_context"].(map[string]any)["revision"] = resumed["folder_context"].(map[string]any)["revision"]
	priorSession, err := f.builder.sessionStore.GetSession(context.Background(), conversationID)
	if err != nil {
		t.Fatal(err)
	}
	messageCount := priorSession.MessageCount
	provider.replyErr = errors.New("API-secret at /private/provider-path")
	_, failed := f.call(t, http.MethodPost, "/api/home-assistant/ask", request)
	assertFailure(failed, "planning_failed")
	failedJSON, _ := json.Marshal(failed)
	if strings.Contains(string(failedJSON), "API-secret") || strings.Contains(string(failedJSON), "/private/provider-path") {
		t.Fatal("provider error leaked")
	}
	afterSession, err := f.builder.sessionStore.GetSession(context.Background(), conversationID)
	if err != nil || afterSession.MessageCount != messageCount || len(provider.requests) != 6 || folderTreeHash(t, root) != before || len(f.builder.workspaceFileStore.CachedWorkspaces()) != workspaces {
		t.Fatal("provider failure persisted a turn or changed source/workspaces", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var cancelledAt time.Time
	provider.replyErr = nil
	provider.onChat = func() { cancelledAt = time.Now(); cancel() }
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	cancelRequest := httptest.NewRequest(http.MethodPost, "/api/home-assistant/ask", bytes.NewReader(encoded)).WithContext(ctx)
	cancelRequest.Header.Set("Content-Type", "application/json")
	cancelResponse := httptest.NewRecorder()
	f.handler.ServeHTTP(cancelResponse, cancelRequest)
	var cancelled map[string]any
	if err := json.Unmarshal(cancelResponse.Body.Bytes(), &cancelled); err != nil {
		t.Fatal(err)
	}
	assertFailure(cancelled, "cancelled")
	if cancelledAt.IsZero() || time.Since(cancelledAt) > 2*time.Second || len(provider.requests) != 7 {
		t.Fatal("cancellation waited or retried the model")
	}
	unchanged, err := f.builder.sessionStore.GetSession(context.Background(), conversationID)
	if err != nil || unchanged.MessageCount != messageCount {
		t.Fatal("cancelled model result was persisted", err)
	}
}
