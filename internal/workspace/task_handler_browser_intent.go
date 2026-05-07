package workspace

import (
	"regexp"
	"strings"
)

// Browser-intent detection. The task system uses these to decide whether a
// task description is asking the agent to do live web automation, and to
// detect refusals when the assigned agent admits it cannot. This logic was
// extracted from task_handler.go to keep its detection vocabulary (regex
// patterns + the file-extension allowlist) close to its consumers.

var (
	browserIntentWordPattern = regexp.MustCompile(`\b(open|visit|navigate|browse|click|fill|type|extract)\b`)
	browserIntentGoToPattern = regexp.MustCompile(`\bgo\s+to\b`)
	browserDomainPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(\.[a-z0-9-]+)+$`)
)

// browserLikeFileExtensions lists strings that look like a domain TLD but
// are actually file extensions ("script.go", "doc.pdf"). Used to suppress
// false positives in isLikelyBrowserAutomationIntent.
var browserLikeFileExtensions = map[string]struct{}{
	"app": {}, "csv": {}, "doc": {}, "docx": {}, "gif": {}, "go": {}, "gz": {}, "heic": {}, "jpeg": {}, "jpg": {},
	"js": {}, "json": {}, "key": {}, "md": {}, "mov": {}, "mp3": {}, "mp4": {}, "numbers": {}, "pages": {}, "pdf": {}, "png": {},
	"ppt": {}, "pptx": {}, "py": {}, "rb": {}, "sh": {}, "svg": {}, "tar": {}, "ts": {}, "txt": {}, "wav": {}, "webp": {}, "xlsx": {}, "xls": {}, "zip": {},
}

// agentSupportsBrowserAutomation reports whether the resolved agent has any
// path to actually open a URL — via declared capability, an MCP server whose
// name implies browser tooling, or a registered utility/MCP tool.
func (h *LLMTaskHandler) agentSupportsBrowserAutomation(ag *resolvedTaskAgent) bool {
	if ag == nil || ag.Agent == nil {
		return false
	}
	if !ag.Settings.IsWebSearchAllowed() {
		return false
	}

	for _, capability := range ag.Capabilities {
		switch strings.ToLower(strings.TrimSpace(capability)) {
		case "browser", "browser_automation", "web_search", "web_fetch":
			return true
		}
	}

	for _, serverName := range ag.MCPServers {
		name := strings.ToLower(strings.TrimSpace(serverName))
		if name == "" {
			continue
		}
		if strings.Contains(name, "playwright") || strings.Contains(name, "browserbase") || strings.Contains(name, "puppeteer") || strings.Contains(name, "browser") {
			return true
		}
	}

	for _, tool := range h.getAgentUtilityTools(ag) {
		if tool == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(tool.Definition().Name))
		if name == "web_search" || name == "web_fetch" || name == "browser" {
			return true
		}
	}

	for _, tool := range h.getAgentMCPTools(ag) {
		if tool == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(tool.Definition().Name))
		if strings.HasPrefix(name, "browser") ||
			strings.HasPrefix(name, "web_fetch") ||
			strings.HasPrefix(name, "web_search") ||
			name == "navigate" ||
			name == "open_url" {
			return true
		}
	}

	return false
}

// isLikelyBrowserAutomationIntent applies the verb + domain heuristics to a
// description. Returns true only when the text both uses a browse-shaped
// verb AND mentions something that looks like a URL or bare domain.
func isLikelyBrowserAutomationIntent(description string) bool {
	lower := strings.ToLower(strings.TrimSpace(description))
	if lower == "" {
		return false
	}

	if !browserIntentWordPattern.MatchString(lower) && !browserIntentGoToPattern.MatchString(lower) {
		return false
	}

	if strings.Contains(lower, "http://") || strings.Contains(lower, "https://") || strings.Contains(lower, "www.") || strings.Contains(lower, "localhost:") {
		return true
	}

	for _, token := range strings.Fields(lower) {
		cleaned := strings.Trim(token, " \t\r\n,.;:!?\"'`()[]{}<>")
		if strings.Count(cleaned, ".") < 1 || strings.Contains(cleaned, "/") {
			continue
		}
		if !browserDomainPattern.MatchString(cleaned) {
			continue
		}
		parts := strings.Split(cleaned, ".")
		tld := parts[len(parts)-1]
		if _, isFileExtension := browserLikeFileExtensions[tld]; isFileExtension {
			continue
		}
		if len(tld) >= 2 && len(tld) <= 12 {
			return true
		}
	}

	return false
}

// taskRequiresBrowserAutomation tests the (possibly overall-mission) task
// description for browser-shaped intent. Used to detect when an agent that
// lacks browser capability is being asked to do something it cannot.
func taskRequiresBrowserAutomation(task Task) bool {
	return isLikelyBrowserAutomationIntent(taskBrowserIntentDescription(task))
}

func taskBrowserIntentDescription(task Task) string {
	if task.Context != nil {
		if overall, ok := task.Context["execution_overall_task_description"].(string); ok {
			if trimmed := strings.TrimSpace(overall); trimmed != "" {
				return trimmed
			}
		}
	}
	return task.Description
}

// looksLikeBrowserCapabilityRefusal pattern-matches an agent's response for
// the canonical "I can't browse" phrasings that signal it should be re-routed
// to a browser-capable agent rather than answered as-is.
func looksLikeBrowserCapabilityRefusal(response string) bool {
	lower := strings.ToLower(strings.TrimSpace(response))
	if lower == "" {
		return false
	}

	markers := []string{
		"i don't have the capability",
		"i do not have the capability",
		"i can't open websites",
		"i cannot open websites",
		"cannot open websites directly",
		"can't access websites directly",
		"cannot access websites directly",
		"i'm unable to open websites",
		"i am unable to open websites",
		"i can't browse",
		"i cannot browse",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}

	return false
}
