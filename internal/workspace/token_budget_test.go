package workspace

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

func TestEstimateTokens(t *testing.T) {
	if got := estimateTokens(""); got != 0 {
		t.Fatalf("empty = %d, want 0", got)
	}
	// 8 bytes -> ceil(8/4) = 2.
	if got := estimateTokens("abcdefgh"); got != 2 {
		t.Fatalf("8 bytes = %d, want 2", got)
	}
	// 9 bytes -> ceil(9/4) = 3.
	if got := estimateTokens("abcdefghi"); got != 3 {
		t.Fatalf("9 bytes = %d, want 3", got)
	}
}

func TestReservedOutputHeadroomAndBudget(t *testing.T) {
	// Small window: floored at min headroom.
	if got := reservedOutputHeadroom(2000); got != minOutputHeadroomTokens {
		t.Fatalf("headroom(2000) = %d, want %d", got, minOutputHeadroomTokens)
	}
	// Large window: 25%.
	if got := reservedOutputHeadroom(40000); got != 10000 {
		t.Fatalf("headroom(40000) = %d, want 10000", got)
	}
	// Unknown window disables budgeting.
	if got := promptBudget(0); got != 0 {
		t.Fatalf("budget(0) = %d, want 0", got)
	}
	if got := promptBudget(40000); got != 30000 {
		t.Fatalf("budget(40000) = %d, want 30000", got)
	}
}

func TestReconcileGenerationCap(t *testing.T) {
	// No explicit cap stays 0.
	if got, lowered := reconcileGenerationCap(0, 1024); got != 0 || lowered {
		t.Fatalf("cap(0,1024) = (%d,%v), want (0,false)", got, lowered)
	}
	// Cap below headroom untouched.
	if got, lowered := reconcileGenerationCap(500, 1024); got != 500 || lowered {
		t.Fatalf("cap(500,1024) = (%d,%v), want (500,false)", got, lowered)
	}
	// Cap above headroom lowered.
	if got, lowered := reconcileGenerationCap(5000, 1024); got != 1024 || !lowered {
		t.Fatalf("cap(5000,1024) = (%d,%v), want (1024,true)", got, lowered)
	}
}

func TestTruncateHeadTailBytes(t *testing.T) {
	// Fits: unchanged.
	if got, cut := truncateHeadTailBytes("short", 100); got != "short" || cut != 0 {
		t.Fatalf("fits = (%q,%d), want (short,0)", got, cut)
	}

	s := strings.Repeat("A", 500) + strings.Repeat("B", 500)
	got, cut := truncateHeadTailBytes(s, 200)
	if cut <= 0 {
		t.Fatalf("expected a cut, got %d", cut)
	}
	if !strings.Contains(got, "trimmed") {
		t.Fatalf("expected trim marker in %q", got)
	}
	// Keeps a head (A's) and a tail (B's).
	if !strings.HasPrefix(got, "A") || !strings.HasSuffix(got, "B") {
		t.Fatalf("expected head+tail preserved, got %q", got)
	}
}

func TestTruncateHeadTailBytes_RuneSafe(t *testing.T) {
	// Multibyte runes must not be split.
	s := strings.Repeat("世", 400) // 3 bytes each = 1200 bytes
	got, cut := truncateHeadTailBytes(s, 300)
	if cut <= 0 {
		t.Fatalf("expected a cut")
	}
	// The head+tail portions (excluding the ASCII marker) must be valid UTF-8.
	if strings.ContainsRune(got, '�') {
		t.Fatalf("result contains replacement char (split rune): %q", got)
	}
}

func makeSeg(label string, order, bytes int) promptSegment {
	return promptSegment{label: label, text: strings.Repeat("x", bytes), trimOrder: order}
}

func TestBudgetPromptSegments_TrimsAttachmentsFirst(t *testing.T) {
	segs := []promptSegment{
		makeSeg("header", 0, 400),
		makeSeg("memory", 4, 4000),
		makeSeg("workspace_snapshot", 3, 4000),
		makeSeg("attachments", 1, 400000), // the outlier (~100k tokens, over budget)
		makeSeg("upstream_inputs", 2, 4000),
		makeSeg("tail", 0, 400),
	}
	// window 40000 -> budget 30000 tokens. Everything but attachments is small;
	// trimming attachments alone should suffice.
	trims, overflow := budgetPromptSegments(40000, "sys", nil, segs)
	if overflow {
		t.Fatalf("did not expect overflow")
	}
	if len(trims) != 1 || trims[0].Label != "attachments" {
		t.Fatalf("expected only attachments trimmed, got %+v", trims)
	}
	// Protected + untouched segments keep their original size.
	if len(segs[0].text) != 400 || len(segs[5].text) != 400 {
		t.Fatalf("protected header/tail were modified")
	}
	if len(segs[1].text) != 4000 || len(segs[2].text) != 4000 || len(segs[4].text) != 4000 {
		t.Fatalf("non-attachment trimmables were touched before needed")
	}
}

func TestBudgetPromptSegments_CloudWindowNoTrim(t *testing.T) {
	segs := []promptSegment{makeSeg("attachments", 1, 200000)}
	// A 200k-token cloud window easily holds a 50k-token attachment.
	trims, overflow := budgetPromptSegments(200000, "sys", nil, segs)
	if overflow || len(trims) != 0 {
		t.Fatalf("cloud window should not trim: trims=%+v overflow=%v", trims, overflow)
	}
	if len(segs[0].text) != 200000 {
		t.Fatalf("segment modified on a large window")
	}
}

func TestBudgetPromptSegments_OverflowWhenProtectedTooBig(t *testing.T) {
	segs := []promptSegment{
		makeSeg("header", 0, 200000), // protected + alone exceeds budget
		makeSeg("attachments", 1, 8000),
	}
	trims, overflow := budgetPromptSegments(40000, "sys", nil, segs)
	if !overflow {
		t.Fatalf("expected overflow when protected content exceeds budget")
	}
	// Attachments should still have been trimmed toward the floor while trying.
	_ = trims
}

func TestEvictConversationForContext(t *testing.T) {
	big := strings.Repeat("T", 160000) // ~40000 tokens each, together over budget
	conversation := []llm.Message{
		llm.NewUserMessage("do the task"),             // 0 protected
		{Role: llm.RoleAssistant, Content: "calling"}, // 1
		llm.NewToolMessage("t1", big),                 // 2 evictable
		llm.NewToolMessage("t2", big),                 // 3 evictable
		llm.NewUserMessage("continue"),                // 4 (most recent, protected)
	}
	compacted, overflow := evictConversationForContext("system", conversation, 40000, nil)
	if compacted == 0 {
		t.Fatalf("expected at least one message compacted")
	}
	if overflow {
		t.Fatalf("did not expect overflow after compaction")
	}
	// Task message untouched.
	if conversation[0].Content != "do the task" {
		t.Fatalf("protected head message was modified")
	}
	// At least one tool message shrank and carries a marker.
	if len(conversation[2].Content) >= 40000 && len(conversation[3].Content) >= 40000 {
		t.Fatalf("no tool message was compacted")
	}
}

// The system prompt travels apart from the conversation but still fills the
// window: it is counted, and it is never what gets compacted.
func TestEvictConversationForContext_CountsTheSystemPrompt(t *testing.T) {
	conversation := func() []llm.Message {
		return []llm.Message{
			llm.NewUserMessage("do the task"),
			{Role: llm.RoleAssistant, Content: "calling"},
			llm.NewToolMessage("t1", strings.Repeat("T", 100000)), // ~25000 tokens
			llm.NewUserMessage("continue"),
		}
	}
	// 40000 window → 30000 budget: the conversation alone fits.
	if compacted, overflow := evictConversationForContext("", conversation(), 40000, nil); compacted != 0 || overflow {
		t.Fatalf("a fitting conversation was compacted=%d overflow=%v", compacted, overflow)
	}
	// A ~10000-token system prompt pushes it over, so the tool result is cut.
	withPrompt := conversation()
	if compacted, overflow := evictConversationForContext(strings.Repeat("S", 40000), withPrompt, 40000, nil); compacted != 1 || overflow {
		t.Fatalf("the system prompt was not counted: compacted=%d overflow=%v", compacted, overflow)
	}
	if len(withPrompt[2].Content) >= 100000 {
		t.Fatal("the tool result was not compacted")
	}
	// A system prompt larger than the budget overflows whatever is compacted.
	if _, overflow := evictConversationForContext(strings.Repeat("S", 140000), conversation(), 40000, nil); !overflow {
		t.Fatal("an oversized system prompt did not overflow")
	}
}

func TestEvictConversationForContext_UnknownWindowDisabled(t *testing.T) {
	conversation := []llm.Message{
		llm.NewUserMessage("task"),
		llm.NewToolMessage("t1", strings.Repeat("X", 100000)),
		llm.NewUserMessage("last"),
	}
	compacted, overflow := evictConversationForContext("system", conversation, 0, nil)
	if compacted != 0 || overflow {
		t.Fatalf("window 0 should disable eviction, got compacted=%d overflow=%v", compacted, overflow)
	}
}
