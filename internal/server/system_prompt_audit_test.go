package server

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A request's instructions travel in llm.ChatRequest.SystemPrompt, which every
// provider sends. A system-role message is not part of every provider's
// contract: the Anthropic API takes its system text apart from the messages,
// and its provider dropped such a message without an error until the
// assistant panel, the task runner, the result normaliser, the continuity
// recap and the orchestration planner had all lost their instructions on
// claude-* API models. The provider now folds system-role messages into its
// system text as a backstop; production code outside internal/llm still never
// builds one.
func TestProductionCodeSendsInstructionsAsSystemPromptNotSystemMessages(t *testing.T) {
	// Each allowed file says why it builds a system-role message.
	allowed := map[string]string{
		// Replays a stored chat session's own system-role turns in place, as the
		// OpenAI chat path does; the agent's prompt travels in SystemPrompt.
		"internal/chathttp/conversation_history.go": "replays stored session history",
	}
	pattern := regexp.MustCompile(`llm\.NewSystemMessage\(|Role:\s*llm\.RoleSystem\b`)
	root := filepath.Clean(filepath.Join("..", ".."))
	seen := map[string]bool{}
	var findings []string
	err := fs.WalkDir(os.DirFS(root), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		clean := filepath.ToSlash(path)
		if entry.IsDir() {
			if clean == ".git" || clean == "tasks" || clean == "internal/llm" ||
				strings.Contains(clean, "/testdata") || strings.Contains(clean, "/node_modules") {
				return fs.SkipDir
			}
			return nil
		}
		if (!strings.HasPrefix(clean, "internal/") && !strings.HasPrefix(clean, "cmd/")) ||
			!strings.HasSuffix(clean, ".go") || strings.HasSuffix(clean, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(clean))) // #nosec G304 -- repository-relative source audit path
		if err != nil {
			return err
		}
		if !pattern.Match(data) {
			return nil
		}
		if _, ok := allowed[clean]; ok {
			seen[clean] = true
			return nil
		}
		findings = append(findings, clean)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("production code builds a system-role llm message; send the text as ChatRequest.SystemPrompt instead:\n%s", strings.Join(findings, "\n"))
	}
	// An allowance that no longer applies is removed, not kept as a loophole.
	for path := range allowed {
		if !seen[path] {
			t.Errorf("%s no longer builds a system-role message; remove its allowance", path)
		}
	}
}
