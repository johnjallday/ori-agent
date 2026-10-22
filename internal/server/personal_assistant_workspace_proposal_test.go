package server

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/session"
)

func TestAssistantWorkspaceProposalProductionSaveAndConcurrentRefusal(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved", true: "newer_correction"}[changed], func(t *testing.T) {
			f := newWorkspaceReaderFixture(t)
			first := f.ask(t, "", "wanna research on okgo band")
			id := first["conversation"].(map[string]any)["id"].(string)
			before := continuityCount(t, f, "messages", id)
			workspaces := len(f.builder.workspaceFileStore.CachedWorkspaces())
			f.provider.script = []llm.ChatResponse{{Content: "first turn already answered"}, readerCall("assistant_propose_hire", map[string]any{"role": "researcher", "name": "Ruth", "brief": "Research how the band started; verify dates and early interviews.", "workspace_name": "OK Go origins"})}
			if changed {
				f.provider.before[2] = func() {
					if err := f.builder.sessionStore.AddMessage(context.Background(), id, &session.Message{Role: session.RoleUser, Content: "Actually, only cover their first album."}); err != nil {
						t.Fatal(err)
					}
				}
			}
			reply := f.ask(t, id, "how they started. can we create a workspace")
			state := reply["conversation"].(map[string]any)
			if len(f.provider.requests) != 2 || len(f.builder.workspaceFileStore.CachedWorkspaces()) != workspaces {
				t.Fatal("proposal called another model or created workspace")
			}
			assertNoPanelExecutionAuthority(t, f.provider.requests[1])
			if changed {
				if state["stored"] == true || reply["confirmation"] != nil || continuityCount(t, f, "messages", id) != before+1 {
					t.Fatal("stale proposal saved or offered", reply)
				}
				return
			}
			confirmation, _ := reply["confirmation"].(map[string]any)
			if state["stored"] != true || confirmation["action_type"] != "hire_agent" {
				t.Fatal(reply)
			}
			args, _ := confirmation["arguments"].(map[string]any)
			if args["proposal_id"] == "" || args["role"] != "researcher" || args["blueprint_label"] != "Research Project" || args["conversation_id"] != id {
				t.Fatalf("hire card arguments: %v", args)
			}
			// The saved proposal comes back on a reload, in its state, with the
			// catalog the card draws its role selector from.
			reloaded := reloadedConversation(t, f.draftServerFixture, id)
			cards, _ := reloaded["hire_proposals"].([]any)
			if len(cards) != 1 || cards[0].(map[string]any)["proposal_id"] != args["proposal_id"] || cards[0].(map[string]any)["state"] != "offered" || reloaded["hire_catalog"] == nil {
				t.Fatalf("reload lost the card: %v", reloaded["hire_proposals"])
			}
			restarted := session.NewHybridStoreWithDB(f.builder.sessionStore.DB(), 10)
			stored, err := restarted.GetSession(context.Background(), id)
			if err != nil || len(stored.Messages) != before+2 {
				t.Fatal("canonical save/restart failed", err)
			}
			for _, message := range stored.Messages {
				if strings.Contains(message.Content, "hire-agent") || strings.Contains(message.Content, "action_type") || strings.Contains(message.Content, args["proposal_id"].(string)) {
					t.Fatal("executable approval saved as history")
				}
			}
		})
	}
}
