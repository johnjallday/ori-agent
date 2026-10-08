package llm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Execute the real CLI adapters against controlled executables, not the user's
// CLI/authentication or native MCP. This proves broker decoding and text-only
// posture; it deliberately does not claim real-model behavior.
func TestAssistantWorkspaceProviderContract_CodexBrokerWithoutNativeAuthority(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("CODEX_HOME", root)
	script := `#!/bin/sh
case "$*" in
  *"--sandbox read-only"*) ;;
  *) exit 91 ;;
esac
case "$*" in
  *workspace-write*|*--profile*|*approval_policy*) exit 92 ;;
esac
input=''
while IFS= read -r line || [ -n "$line" ]; do input="$input$line"; done
case "$input" in
  *workspace_notes*arguments_json*) ;;
  *) exit 93 ;;
esac
printf '%s' '{"kind":"tool_call","content":"","tool_name":"workspace_notes","arguments_json":"{}"}'
`
	cli := filepath.Join(root, "controlled-codex")
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	provider := &CodexProvider{cliPath: cli}
	response, err := provider.Chat(context.Background(), ChatRequest{
		Model: "fixture", Messages: []Message{NewUserMessage("List notes")},
		Tools: []Tool{{Name: "workspace_notes", Parameters: map[string]any{"type": "object"}}},
	})
	if err != nil || response == nil || len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "workspace_notes" || response.ToolCalls[0].Arguments != "{}" {
		t.Fatalf("broker response: %+v %v", response, err)
	}
}

func TestAssistantWorkspaceProviderContract_ClaudeCodeSnapshotOnly(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	script := `#!/bin/sh
case "$*" in
  *"--permission-mode dontAsk"*) ;;
  *) exit 91 ;;
esac
case "$*" in
  *bypassPermissions*|*--mcp-config*|*--add-dir*) exit 92 ;;
esac
previous=''
found=''
for argument in "$@"; do
  if [ "$previous" = '--tools' ] && [ -z "$argument" ]; then found=yes; fi
  previous="$argument"
done
[ "$found" = yes ] || exit 93
printf '%s' '{"result":"Validated snapshot only; deeper reads unavailable here."}'
`
	cli := filepath.Join(root, "controlled-claude")
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	provider := &ClaudeCodeProvider{cliPath: cli}
	if provider.Capabilities().SupportsTools || !provider.Capabilities().SupportsNativeMCP {
		t.Fatal("snapshot/native capability contract changed")
	}
	response, err := provider.Chat(context.Background(), ChatRequest{
		Model: "fixture", Messages: []Message{NewUserMessage("Discuss validated snapshot")},
	})
	if err != nil || response == nil || len(response.ToolCalls) != 0 || !strings.Contains(response.Content, "deeper reads unavailable") {
		t.Fatalf("snapshot response: %+v %v", response, err)
	}
}

func TestAssistantWorkspaceProviderContract_DisplayIDIsNotExecutionPermission(t *testing.T) {
	// Even without MCP servers, a display ID in WorkspaceID would elevate the
	// run. Context IDs must live in a separate host contract, not ChatRequest.
	codex := &CodexProvider{}
	claude := &ClaudeCodeProvider{}
	for _, id := range []string{"", "display-only-workspace"} {
		c, err := codex.prepareNativeMCP(nil, id, "", nil)
		if err != nil || (c != nil) != (id != "") {
			t.Fatalf("codex: %q %+v %v", id, c, err)
		}
		a, err := claude.prepareNativeMCP(nil, id, "", nil)
		if err != nil || (a != nil) != (id != "") {
			t.Fatalf("claude: %q %+v %v", id, a, err)
		}
	}
}
