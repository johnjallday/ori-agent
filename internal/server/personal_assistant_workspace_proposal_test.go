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
			f.provider.script = []llm.ChatResponse{{Content: "first turn already answered"}, readerCall("assistant_propose_workspace", map[string]any{"name": "OK Go origins", "description": "Research how the band started. Verify dates and early interviews; no research has run yet."})}
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
			if state["stored"] != true || reply["confirmation"].(map[string]any)["action_type"] != "prepare_workspace" {
				t.Fatal(reply)
			}
			restarted := session.NewHybridStoreWithDB(f.builder.sessionStore.DB(), 10)
			stored, err := restarted.GetSession(context.Background(), id)
			if err != nil || len(stored.Messages) != before+2 {
				t.Fatal("canonical save/restart failed", err)
			}
			for _, message := range stored.Messages {
				if strings.Contains(message.Content, "prepare-workspace") || strings.Contains(message.Content, "action_type") {
					t.Fatal("executable approval saved as history")
				}
			}
		})
	}
}
