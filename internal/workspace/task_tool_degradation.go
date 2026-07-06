package workspace

import (
	"encoding/json"
	"os"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// localToolCap is the default maximum number of tools offered to a local
// provider per task; the full set is pruned to this when it exceeds it (WS4.18).
const localToolCap = 12

// workspaceCoreToolNames are always kept when pruning — an agent needs them to
// read/update workspace state regardless of the task (WS4.18).
var workspaceCoreToolNames = map[string]struct{}{
	"workspace_notes":       {},
	"workspace_tasks":       {},
	"workspace_files":       {},
	"workspace_directories": {},
	"workspace_sessions":    {},
}

// toolKeywordStopwords are ignored when scoring tool relevance so common words
// don't dominate the keyword overlap.
var toolKeywordStopwords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "with": {}, "this": {}, "that": {},
	"from": {}, "into": {}, "your": {}, "you": {}, "are": {}, "was": {},
	"task": {}, "please": {}, "should": {}, "must": {}, "will": {},
}

// pruneToolsForLocal reduces tools to at most cap for local providers when the
// full set exceeds it (WS4.18). Workspace core tools are always kept; the
// remaining slots go to the tools whose name/description best overlap the task
// description (ties broken by name for determinism). Returns the kept tools and
// the sorted names of dropped tools. When tools already fit, it returns them
// unchanged with no drops.
func pruneToolsForLocal(tools []llm.Tool, taskDescription string, cap int) ([]llm.Tool, []string) {
	if cap <= 0 || len(tools) <= cap {
		return tools, nil
	}

	var kept, candidates []llm.Tool
	for _, t := range tools {
		if _, core := workspaceCoreToolNames[strings.ToLower(strings.TrimSpace(t.Name))]; core {
			kept = append(kept, t)
		} else {
			candidates = append(candidates, t)
		}
	}

	keywords := extractToolKeywords(taskDescription)
	sort.SliceStable(candidates, func(i, j int) bool {
		si, sj := toolRelevanceScore(candidates[i], keywords), toolRelevanceScore(candidates[j], keywords)
		if si != sj {
			return si > sj
		}
		return candidates[i].Name < candidates[j].Name
	})

	slots := cap - len(kept) // may be <= 0 if core alone exceeds cap; core is never dropped
	var dropped []string
	for idx, t := range candidates {
		if idx < slots {
			kept = append(kept, t)
		} else {
			dropped = append(dropped, t.Name)
		}
	}
	sort.Strings(dropped)
	return kept, dropped
}

// extractToolKeywords returns the distinct lowercase words (length >= 3, minus
// stopwords) of the task description.
func extractToolKeywords(description string) map[string]struct{} {
	keywords := map[string]struct{}{}
	for _, word := range strings.FieldsFunc(strings.ToLower(description), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if len(word) < 3 {
			continue
		}
		if _, stop := toolKeywordStopwords[word]; stop {
			continue
		}
		keywords[word] = struct{}{}
	}
	return keywords
}

// toolRelevanceScore counts how many distinct task keywords appear in the tool's
// name or description.
func toolRelevanceScore(tool llm.Tool, keywords map[string]struct{}) int {
	haystack := strings.ToLower(tool.Name + " " + tool.Description)
	score := 0
	for kw := range keywords {
		if strings.Contains(haystack, kw) {
			score++
		}
	}
	return score
}

// isToolsRejectedError reports whether a provider error indicates the model
// rejected the tools parameter (e.g. Ollama "<model> does not support tools"),
// so the handler can retry the round without tools (WS4.17).
func isToolsRejectedError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "does not support tools"):
		return true
	case strings.Contains(s, "does not support tool"):
		return true
	case strings.Contains(s, "tools are not supported"):
		return true
	case strings.Contains(s, "tool") && strings.Contains(s, "not support"):
		return true
	default:
		return false
	}
}

// localTextToolProtocolEnabled reports whether the feature-flagged prompt-based
// tool protocol is on (default off), read from ORI_LOCAL_TEXT_TOOL_PROTOCOL
// (WS4.19).
func localTextToolProtocolEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ORI_LOCAL_TEXT_TOOL_PROTOCOL"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// textToolProtocolInstruction is appended to the system prompt (when the flag is
// on and native tool calling was rejected) telling the model how to request a
// tool via a fenced JSON block the handler can parse (WS4.19).
const textToolProtocolInstruction = "\n\nThis model cannot call tools natively. When you need a tool, respond with ONLY a single fenced JSON block of the form ```json\n{\"tool_call\": {\"name\": \"<tool_name>\", \"arguments\": { ... }}}\n``` and nothing else. When you have the final answer, respond normally without a tool_call block."

// textToolCallEnvelope is the wire shape the model emits under the text protocol.
type textToolCallEnvelope struct {
	ToolCall struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"tool_call"`
}

// parseTextToolCall extracts a text-protocol tool call from a model response
// (WS4.19). Returns the tool name, its arguments as a JSON string, and whether a
// well-formed call was found. Tolerates code fences and surrounding prose.
func parseTextToolCall(content string) (name string, arguments string, ok bool) {
	candidate := strings.TrimSpace(llm.StripCodeFence(content))

	var env textToolCallEnvelope
	if err := json.Unmarshal([]byte(candidate), &env); err != nil {
		sub, found := extractFirstJSONObject(candidate)
		if !found {
			return "", "", false
		}
		if err := json.Unmarshal([]byte(sub), &env); err != nil {
			return "", "", false
		}
	}

	name = strings.TrimSpace(env.ToolCall.Name)
	if name == "" {
		return "", "", false
	}
	arguments = strings.TrimSpace(string(env.ToolCall.Arguments))
	if arguments == "" || arguments == "null" {
		arguments = "{}"
	}
	return name, arguments, true
}

// extractFirstJSONObject returns the substring from the first '{' to its matching
// closing '}' (brace-balanced, string-aware), or false if none.
func extractFirstJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
			// skip
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}
