package workspace

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// Token budgeting for task prompts. Local models have small context windows and
// silently truncate anything that overflows, so we estimate the assembled prompt
// size and deterministically trim the least-load-bearing sections until it fits
// (PRD WS2). Estimation is character-based (ceil(bytes/4)) — good enough for v1;
// logged trims make estimation error observable (PRD Decision 3).
const (
	// bytesPerTokenEstimate divides byte length to approximate token count.
	bytesPerTokenEstimate = 4

	// outputHeadroomFraction reserves part of the window for generated tokens.
	outputHeadroomFraction = 0.25
	// minOutputHeadroomTokens floors the reserved output headroom.
	minOutputHeadroomTokens = 1024

	// imageTokenCost is the fixed nominal token cost charged per attached image;
	// base64 bytes/4 would be wildly wrong, so images are charged flat (WS2.5).
	imageTokenCost = 768

	// Attachment injection caps (bytes), applied before budgeting (WS2.8).
	maxAttachmentBytesPerFile = 64 * 1024
	maxAttachmentBytesTotal   = 128 * 1024

	// Tool-result message caps (bytes) appended to the conversation (WS2.9).
	maxToolResultBytesLocal = 4 * 1024
	maxToolResultBytesCloud = 16 * 1024

	// minSegmentKeepBytes is the head+tail sliver a trimmable segment is reduced
	// toward before the next segment is trimmed, so the model still sees a hint
	// of what was cut.
	minSegmentKeepBytes = 256
)

// estimateTokens approximates the token count of s as ceil(len(s)/4).
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return int(math.Ceil(float64(len(s)) / float64(bytesPerTokenEstimate)))
}

// estimateToolTokens approximates the token cost of the tool schemas that are
// serialized into the request (name + description + JSON parameters).
func estimateToolTokens(tools []llm.Tool) int {
	total := 0
	for _, t := range tools {
		total += estimateTokens(t.Name) + estimateTokens(t.Description)
		if len(t.Parameters) > 0 {
			if b, err := json.Marshal(t.Parameters); err == nil {
				total += estimateTokens(string(b))
			}
		}
	}
	return total
}

// estimateMessageTokens approximates a conversation message's token cost,
// including a flat charge per attached image (WS2.5).
func estimateMessageTokens(msg llm.Message) int {
	total := estimateTokens(msg.Content)
	total += len(msg.Images) * imageTokenCost
	for _, tc := range msg.ToolCalls {
		total += estimateTokens(tc.Name) + estimateTokens(tc.Arguments)
	}
	return total
}

// estimateConversationTokens sums the token cost of a whole conversation.
func estimateConversationTokens(messages []llm.Message) int {
	total := 0
	for _, m := range messages {
		total += estimateMessageTokens(m)
	}
	return total
}

// reservedOutputHeadroom returns the tokens held back from the window for the
// model's generation (WS2.5).
func reservedOutputHeadroom(window int) int {
	h := int(float64(window) * outputHeadroomFraction)
	if h < minOutputHeadroomTokens {
		h = minOutputHeadroomTokens
	}
	return h
}

// promptBudget returns the token budget available for the prompt (window minus
// reserved output headroom). A non-positive window means "unknown" and disables
// budgeting (returns 0).
func promptBudget(window int) int {
	if window <= 0 {
		return 0
	}
	budget := window - reservedOutputHeadroom(window)
	if budget <= 0 {
		// Pathologically small window: keep a minimal positive budget so we still
		// trim toward *something* rather than divide the prompt to nothing.
		budget = window
	}
	return budget
}

// reconcileGenerationCap lowers an explicit generation cap to the reserved
// headroom so generated output cannot overrun the context window (WS2.5a).
// A cap of 0 (provider default) is left untouched. Returns the effective cap and
// whether it was lowered.
func reconcileGenerationCap(maxTokens, headroom int) (int, bool) {
	if maxTokens > 0 && headroom > 0 && maxTokens > headroom {
		return headroom, true
	}
	return maxTokens, false
}

// trimMarker renders the marker inserted where content was cut.
func trimMarker(cutBytes int) string {
	return fmt.Sprintf("\n…[trimmed %d bytes to fit model context]…\n", cutBytes)
}

// truncateHeadTailBytes shrinks s to roughly maxBytes, keeping a head and tail
// slice around an inserted marker that reports how many bytes were removed
// (WS2.7). Cuts fall on UTF-8 rune boundaries. Returns s unchanged (and cut 0)
// when it already fits. The result may slightly exceed maxBytes by the marker
// length — acceptable for estimate-based budgeting.
func truncateHeadTailBytes(s string, maxBytes int) (string, int) {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s, 0
	}

	// First pass with an approximate marker to size the head/tail.
	approxCut := len(s) - maxBytes
	keep := maxBytes - len(trimMarker(approxCut))
	if keep < 0 {
		keep = 0
	}
	head := safePrefix(s, keep/2)
	tail := safeSuffix(s, keep-len(head))

	cut := len(s) - len(head) - len(tail)
	if cut <= 0 {
		return s, 0
	}
	return head + trimMarker(cut) + tail, cut
}

// safePrefix returns the longest prefix of s that is at most n bytes and ends on
// a rune boundary.
func safePrefix(s string, n int) string {
	if n >= len(s) {
		return s
	}
	if n <= 0 {
		return ""
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// safeSuffix returns the longest suffix of s that is at most n bytes and starts
// on a rune boundary.
func safeSuffix(s string, n int) string {
	if n >= len(s) {
		return s
	}
	if n <= 0 {
		return ""
	}
	start := len(s) - n
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}

// promptSegment is one region of the assembled task user-prompt. trimOrder 0
// means protected (never trimmed — task description, output-format, headers);
// segments with trimOrder > 0 are trimmed in ascending order until the prompt
// fits (WS2.6): attachments (1), upstream results (2), workspace snapshot (3),
// memory (4).
type promptSegment struct {
	label     string
	text      string
	trimOrder int
}

// budgetTrim records a single trim/eviction for logging and observability.
type budgetTrim struct {
	Label    string
	BytesCut int
}

// renderPromptSegments concatenates segments in order.
func renderPromptSegments(segs []promptSegment) string {
	var b []byte
	for _, s := range segs {
		b = append(b, s.text...)
	}
	return string(b)
}

// budgetPromptSegments trims the trimmable segments in place until the assembled
// prompt (system prompt + tool schemas + segments) fits the window's budget, in
// the fixed WS2.6 order. It returns the trims performed and whether the prompt
// still overflows after all trims (WS2.10). A non-positive window disables
// budgeting.
func budgetPromptSegments(window int, systemPrompt string, tools []llm.Tool, segs []promptSegment) (trims []budgetTrim, overflow bool) {
	budget := promptBudget(window)
	if budget <= 0 {
		return nil, false
	}

	fixed := estimateTokens(systemPrompt) + estimateToolTokens(tools)
	total := fixed + segmentsTokens(segs)
	if total <= budget {
		return nil, false
	}

	for _, idx := range trimmableOrder(segs) {
		if total <= budget {
			break
		}
		seg := &segs[idx]
		cur := estimateTokens(seg.text)
		minKeep := estimateTokens(safePrefix(seg.text, minSegmentKeepBytes))
		reducible := cur - minKeep
		if reducible <= 0 {
			continue
		}
		reduce := min(reducible, total-budget)
		targetBytes := (cur - reduce) * bytesPerTokenEstimate
		newText, cut := truncateHeadTailBytes(seg.text, targetBytes)
		if cut > 0 {
			seg.text = newText
			trims = append(trims, budgetTrim{Label: seg.label, BytesCut: cut})
			total = fixed + segmentsTokens(segs)
		}
	}

	return trims, total > budget
}

// segmentsTokens sums the estimated tokens of all segments.
func segmentsTokens(segs []promptSegment) int {
	total := 0
	for _, s := range segs {
		total += estimateTokens(s.text)
	}
	return total
}

// trimmableSegmentLabels returns the labels of trimmable segments, for naming
// the offending sections in a context_overflow error (WS2.10).
func trimmableSegmentLabels(segs []promptSegment) []string {
	labels := make([]string, 0, len(segs))
	for _, s := range segs {
		if s.trimOrder > 0 {
			labels = append(labels, s.label)
		}
	}
	return labels
}

// evictConversationForContext keeps a growing tool-loop conversation within the
// window budget by compacting the oldest tool-result messages (head+tail marker)
// until it fits (WS2.10a). It mutates message content in place — never removing a
// message, so assistant tool_call / tool_result pairing stays intact — and never
// touches the system prompt (index 0), the initial task message (index 1), or the
// most recent message. Returns how many messages were compacted and whether the
// conversation still overflows after compacting every eligible one. A
// non-positive window disables eviction.
func evictConversationForContext(conversation []llm.Message, window int, tools []llm.Tool) (compacted int, overflow bool) {
	budget := promptBudget(window)
	if budget <= 0 {
		return 0, false
	}

	total := estimateToolTokens(tools) + estimateConversationTokens(conversation)
	if total <= budget {
		return 0, false
	}

	const protectedHead = 2 // system prompt + initial task message
	lastIdx := len(conversation) - 1
	for i := protectedHead; i < lastIdx && total > budget; i++ {
		if conversation[i].Role != llm.RoleTool {
			continue
		}
		before := estimateMessageTokens(conversation[i])
		newContent, cut := truncateHeadTailBytes(conversation[i].Content, minSegmentKeepBytes)
		if cut <= 0 {
			continue
		}
		conversation[i].Content = newContent
		total -= before - estimateMessageTokens(conversation[i])
		compacted++
	}

	return compacted, total > budget
}

// trimmableOrder returns segment indices with trimOrder > 0, sorted ascending by
// trimOrder (stable on original position for ties).
func trimmableOrder(segs []promptSegment) []int {
	idx := make([]int, 0, len(segs))
	for i := range segs {
		if segs[i].trimOrder > 0 {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return segs[idx[a]].trimOrder < segs[idx[b]].trimOrder
	})
	return idx
}
