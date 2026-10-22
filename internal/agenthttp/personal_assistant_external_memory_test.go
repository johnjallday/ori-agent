package agenthttp

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/externalmemory"
	"github.com/johnjallday/ori-agent/internal/llm"
)

type fakeRecall struct{ queries []externalmemory.Query }

func (f *fakeRecall) Recall(q externalmemory.Query) string {
	f.queries = append(f.queries, q)
	return "<<<external-memory-results\nRECALL_SENTINEL for " + q.Text + "\nexternal-memory-results>>>"
}

const externalBlockSentinel = "EXTERNAL_BLOCK_SENTINEL Codex remembers the user prefers net returns"

func newExternalMemoryFixture(t *testing.T) (*readerFixture, *fakeRecall) {
	t.Helper()
	f, _, _ := newResearchHostFixture(t)
	f.work.ExternalMemory = externalmemory.ContextHeading + "\n" + externalmemory.DataNotice + "\n- [Codex] " + externalBlockSentinel
	recall := &fakeRecall{}
	f.handler.SetExternalMemory(recall)
	return f, recall
}

func (f *readerFixture) askAssistant(refs *HomeAssistantRouteContext) {
	f.handler.Ask(context.Background(), HomeAssistantAskRequest{
		Prompt: "What did I decide about EMA exits?", Intent: homeAssistantConversationIntent.Key,
		Context: refs, Conversation: &HomeAssistantConversationRef{ID: "canonical"},
	})
}

func TestExternalMemory_HomeAndDrawerTurnsGetTheBlockAndTheTool(t *testing.T) {
	for name, pinned := range map[string]bool{"drawer on a workspace page": true, "home": false} {
		t.Run(name, func(t *testing.T) {
			f, recall := newExternalMemoryFixture(t)
			f.provider.script = []llm.ChatResponse{
				toolCall(externalmemory.RecallToolName, map[string]any{"query": "EMA exits", "source": "codex", "limit": 3}),
				{Content: "Codex remembers that EMA20 exits beat fixed stops after fees."},
			}
			refs := f.refs
			if !pinned {
				refs = nil
			}
			f.askAssistant(refs)
			if len(f.provider.requests) < 2 {
				t.Fatalf("expected a tool round, got %d requests", len(f.provider.requests))
			}
			first := f.provider.requests[0]
			if !slices.Contains(offeredTools(first), externalmemory.RecallToolName) {
				t.Fatalf("memory_recall not offered: %v", offeredTools(first))
			}
			user := first.Messages[len(first.Messages)-1].Content
			if !strings.Contains(user, "<untrusted_external_memory>") || !strings.Contains(user, externalBlockSentinel) {
				t.Fatalf("the external block is missing from the turn's data:\n%s", user)
			}
			if strings.Contains(first.SystemPrompt, externalBlockSentinel) {
				t.Fatal("the external block must travel with the user-message data, not the system prompt")
			}
			if !strings.Contains(user, "memory_recall searches what the user's other coding agents") {
				t.Fatal("the turn does not tell the model when to search other agents' memory")
			}
			if len(recall.queries) != 1 || recall.queries[0].Text != "EMA exits" || recall.queries[0].Source != "codex" || recall.queries[0].Limit != 3 {
				t.Fatalf("recall queries = %+v", recall.queries)
			}
			if result := lastToolResult(f.provider.requests[1]); !strings.Contains(result, "RECALL_SENTINEL for EMA exits") {
				t.Fatalf("tool result not returned to the model: %s", result)
			}
		})
	}
}

func TestExternalMemory_PausedOrUnwiredAssistantGetsNoTool(t *testing.T) {
	t.Run("paused", func(t *testing.T) {
		f, recall := newExternalMemoryFixture(t)
		f.work.State = "paused"
		f.provider.script = []llm.ChatResponse{{Content: "ok"}}
		f.askAssistant(f.refs)
		for _, request := range f.provider.requests {
			if slices.Contains(offeredTools(request), externalmemory.RecallToolName) {
				t.Fatal("a paused assistant was offered memory_recall")
			}
		}
		if len(recall.queries) != 0 {
			t.Fatal("a paused assistant recalled")
		}
	})
	t.Run("unwired", func(t *testing.T) {
		f, _, _ := newResearchHostFixture(t)
		f.provider.script = []llm.ChatResponse{{Content: "ok"}}
		f.askAssistant(f.refs)
		if slices.Contains(offeredTools(f.provider.requests[0]), externalmemory.RecallToolName) {
			t.Fatal("memory_recall offered without a recaller")
		}
		if strings.Contains(f.provider.requests[0].Messages[len(f.provider.requests[0].Messages)-1].Content, "memory_recall") {
			t.Fatal("the recall instruction was sent without the tool")
		}
		if strings.Contains(f.provider.requests[0].Messages[len(f.provider.requests[0].Messages)-1].Content, "<untrusted_external_memory>") {
			t.Fatal("an empty external block was rendered")
		}
	})
}

func TestExternalMemoryRegistry_RefusesBadArgumentsAndPassesOtherTools(t *testing.T) {
	base := newHomeToolRegistry(HomeSnapshotSources{})
	registry := &externalMemoryRegistry{base: base, recall: &fakeRecall{}}
	if _, err := registry.Execute(context.Background(), externalmemory.RecallToolName, `{"query":"", "source":"nope"}`); err == nil {
		t.Fatal("an empty query should be refused")
	}
	if _, err := registry.Execute(context.Background(), externalmemory.RecallToolName, `{"query":"x","source":"nope"}`); err == nil {
		t.Fatal("an unknown source should be refused")
	}
	names := map[string]bool{}
	for _, tool := range registry.Definitions() {
		names[tool.Name] = true
	}
	if !names[externalmemory.RecallToolName] || len(names) != len(base.Definitions())+1 {
		t.Fatalf("definitions = %v", names)
	}
}
