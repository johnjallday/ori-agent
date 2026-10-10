package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/session"
)

type continuityProbeProvider struct {
	llm.Provider
	onSummary func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error)
	tools     bool
}

func (p *continuityProbeProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{SupportsSystemPrompt: true, SupportsTools: p.tools}
}
func (p *continuityProbeProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if len(req.Messages) > 0 && strings.Contains(req.Messages[0].Content, "Select a compact conversation recap") {
		return p.onSummary(ctx, req)
	}
	return p.Provider.Chat(ctx, req)
}

func longContinuityFixture(t *testing.T) (*workspaceReaderFixture, string) {
	t.Helper()
	f := newWorkspaceReaderFixture(t)
	id := f.ask(t, "", "My goal is membership, not coaching.")["conversation"].(map[string]any)["id"].(string)
	for range 50 {
		if err := f.builder.sessionStore.AddMessage(t.Context(), id, &session.Message{Role: session.RoleUser, Content: strings.Repeat("A separate gardening discussion. ", 80)}); err != nil {
			t.Fatal(err)
		}
	}
	return f, id
}

func continuityCount(t *testing.T, f *workspaceReaderFixture, table, id string) int {
	t.Helper()
	query := "SELECT COUNT(*) FROM messages WHERE session_id=?"
	if table == "checkpoints" {
		query = "SELECT COUNT(*) FROM assistant_conversation_checkpoints WHERE session_id=?"
	}
	var count int
	if err := f.builder.sessionStore.DB().QueryRowContext(t.Context(), query, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestAssistantContinuityInFlightMutationDoesNotSaveOrResurrect(t *testing.T) {
	for _, mode := range []string{"source_edit", "append", "delete", "relationship", "owner_move"} {
		t.Run(mode, func(t *testing.T) {
			f, id := longContinuityFixture(t)
			before := continuityCount(t, f, "messages", id)
			probe := &continuityProbeProvider{Provider: f.provider, tools: true}
			probe.onSummary = func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
				if len(req.Tools) != 0 || req.WorkspaceID != "" || len(req.MCPServers) != 0 {
					t.Fatal("summary acquired authority")
				}
				db := f.builder.sessionStore.DB()
				var err error
				switch mode {
				case "source_edit":
					_, err = db.ExecContext(ctx, `UPDATE messages SET content='Changed original constraint.' WHERE rowid=(SELECT MIN(rowid) FROM messages WHERE session_id=?)`, id)
				case "append":
					err = f.builder.sessionStore.AddMessage(ctx, id, &session.Message{Role: session.RoleUser, Content: "Another tab's exact newer correction."})
				case "delete":
					err = f.builder.sessionStore.DeleteSession(ctx, id)
				case "relationship":
					_, err = db.ExecContext(ctx, `UPDATE personal_assistant_state SET state_version=state_version+1 WHERE user_id='local'`)
				case "owner_move":
					_, err = db.ExecContext(ctx, `UPDATE sessions SET workspace_id=? WHERE id=?`, f.project.ID, id)
				}
				if err != nil {
					t.Fatal(err)
				}
				return &llm.ChatResponse{Content: `{"version":1,"items":[]}`}, nil
			}
			f.builder.llmFactory.Register("claude_code", probe)
			reply := f.ask(t, id, "Return to community membership options.")
			state := reply["conversation"].(map[string]any)
			if state["stored"] == true || state["error"] != "context_save_failed" || continuityCount(t, f, "checkpoints", id) != 0 {
				t.Fatal("in-flight mutation saved or resurrected context", reply)
			}
			want := before
			switch mode {
			case "append":
				want++
			case "delete":
				want = 0
			}
			if count := continuityCount(t, f, "messages", id); count != want {
				t.Fatal("losing turn changed transcript", count, want)
			}
		})
	}
}

func TestAssistantContinuitySnapshotOnlySummaryFailureStillAnswersWithoutTools(t *testing.T) {
	for _, summaryErr := range []error{context.DeadlineExceeded, errors.New("controlled provider failure")} {
		t.Run(summaryErr.Error(), func(t *testing.T) {
			f, id := longContinuityFixture(t)
			called := 0
			probe := &continuityProbeProvider{Provider: f.provider, onSummary: func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
				called++
				if len(req.Tools) != 0 || req.Model != "sonnet" {
					t.Fatal("summary changed configured model or gained tools")
				}
				return nil, summaryErr
			}}
			f.builder.llmFactory.Register("claude_code", probe)
			reply := f.ask(t, id, "Return to community membership options.")
			state := reply["conversation"].(map[string]any)
			if called != 1 || state["stored"] != true || state["continuity"].(map[string]any)["recap_unavailable"] != true {
				t.Fatal("bounded summary failure broke ordinary answer", reply)
			}
			if len(f.provider.requests[len(f.provider.requests)-1].Tools) != 0 || continuityCount(t, f, "checkpoints", id) != 0 {
				t.Fatal("snapshot provider escalated or invalid recap persisted")
			}
		})
	}
}

func TestAssistantContinuityRequestCancellationWritesNothing(t *testing.T) {
	f, id := longContinuityFixture(t)
	before := continuityCount(t, f, "messages", id)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	probe := &continuityProbeProvider{Provider: f.provider, tools: true, onSummary: func(ctx context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
		cancel()
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	f.builder.llmFactory.Register("claude_code", probe)
	data, err := json.Marshal(map[string]any{"prompt": "Continue our discussion.", "intent": "assistant_conversation", "conversation": map[string]string{"id": id}, "context": placementContext(f.project)})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/home-assistant/ask", bytes.NewReader(data)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	f.handler.ServeHTTP(httptest.NewRecorder(), req)
	if continuityCount(t, f, "messages", id) != before || continuityCount(t, f, "checkpoints", id) != 0 || len(f.provider.requests) != 1 {
		t.Fatal("cancelled request continued generation or wrote context")
	}
}
