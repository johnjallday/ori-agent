package agenthttp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// Actual Codex adapter and Ask, but a controlled executable, never the user's
// CLI/config/credentials/model. The broker supplies constrained output; the
// hire proposal arrives as one brokered tool call and is rendered by Ori.
func TestHireProposalCodexBrokerUsesSchemaWithoutNativeAuthority(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("CODEX_HOME", root)
	t.Setenv("PATH", root)
	script := `#!/bin/sh
case "$*" in *"--sandbox read-only"*) ;; *) exit 91 ;; esac
case "$*" in *workspace-write*|*--profile*|*--add-dir*|*approval_policy*) exit 92 ;; esac
previous=''
schema=''
for argument in "$@"; do
  if [ "$previous" = '--output-schema' ]; then schema="$argument"; fi
  previous="$argument"
done
[ -f "$schema" ] || exit 93
input=''
while IFS= read -r line || [ -n "$line" ]; do input="$input$line"; done
case "$input" in *assistant_propose_hire*okgo*) ;; *) exit 94 ;; esac
case "$input" in *assistant_propose_workspace*) exit 95 ;; *) ;; esac
printf '%s' '{"kind":"tool_call","content":"Sounds like a job for a Researcher.","tool_name":"assistant_propose_hire","arguments_json":"{\"role\":\"researcher\",\"name\":\"Ruth\",\"brief\":\"Research how OK Go started; verify early interviews and dates.\",\"workspace_name\":\"OK Go origins\"}"}'
`
	if err := os.WriteFile(filepath.Join(root, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	provider, err := llm.NewCodexProvider()
	if err != nil {
		t.Fatal(err)
	}
	f, _, _, proposals := newHireProposalFixture(t)
	f.handler.LLMFactory.Register("fake", provider)
	response := f.handler.Ask(context.Background(), HomeAssistantAskRequest{Prompt: "wanna research on okgo band. how they started. can someone look into it for me", Intent: homeAssistantConversationIntent.Key, Context: f.refs, Conversation: &HomeAssistantConversationRef{ID: "canonical"}})
	if response.Confirmation == nil || response.Confirmation.ActionType != HomeActionHireAgent || response.Conversation == nil || !response.Conversation.Stored {
		t.Fatalf("real adapter did not deliver the hire card: %+v", response)
	}
	args := response.Confirmation.Arguments
	if args["role"] != "researcher" || args["name"] != "Ruth" || args["workspace_name"] != "OK Go origins" || args["blueprint_label"] != "Research Project" || len(proposals.order) != 1 {
		t.Fatalf("brokered proposal: %+v", args)
	}
}

func TestHireProposalNaturalAndPreparationRoutesStayConversational(t *testing.T) {
	h, _ := newHiredRouteHandler(t, "active")
	for _, prompt := range []string{"how they started. can we create a workspace", "Prepare a workspace review for researching how OK Go started.", "help me plan the product launch for March"} {
		route, err := h.RoutePrompt(context.Background(), prompt, homePanelRouteContext())
		if err != nil {
			t.Fatal(err)
		}
		requireConversationRoute(t, route)
	}
}
