package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

func TestAssistantContextFullFolderSelectionSurvivesBoundedReadsAndRestart(t *testing.T) {
	db, store, chat, owner := assistantContextFixture(t)
	event := folderEventFixture()
	event.Observation.Entries = foldercontext.MaxTreeNodes
	event.Observation.Tree = &foldercontext.Tree{}
	for i := range foldercontext.MaxTreeNodes {
		id := fmt.Sprintf("entry-%d", i)
		event.Observation.Tree.Nodes = append(event.Observation.Tree.Nodes, foldercontext.TreeNode{ID: id, Name: fmt.Sprintf("Topic %d", i), Kind: "file"})
		event.FocusIDs = append(event.FocusIDs, id)
	}
	// Keep the observation within 8 KiB but exceed the former event reader's
	// 8,448-byte bound when all 64 independently selected IDs are included.
fill:
	for i := range event.Observation.Tree.Nodes {
		for len(event.Observation.Tree.Nodes[i].Name) < foldercontext.MaxNameRunes {
			previous := event.Observation.Tree.Nodes[i].Name
			event.Observation.Tree.Nodes[i].Name += "<"
			data, err := json.Marshal(event.Observation)
			if err != nil {
				t.Fatal(err)
			}
			if len(data) > foldercontext.MaxBytes {
				event.Observation.Tree.Nodes[i].Name = previous
				break fill
			}
		}
	}
	data, err := json.Marshal(event)
	if err != nil || len(data) <= foldercontext.MaxBytes+256 || len(data) > foldercontext.MaxEventBytes {
		t.Fatal("fixture does not cover the expanded envelope", len(data), err)
	}
	rows, err := store.(AssistantTurnStore).AppendAttributedTurn(t.Context(), chat.ID, owner, &event, "", "Discuss all selected topics", "Metadata only", nil)
	if err != nil || len(rows) != 3 {
		t.Fatal("attributed write", err)
	}
	restarted := NewHybridStoreWithDB(db, 10)
	check := func(message *Message) {
		t.Helper()
		if message == nil || message.ID != rows[0].ID || message.FolderContext == nil || strings.Join(message.FolderContext.FocusIDs, ",") != strings.Join(event.FocusIDs, ",") {
			t.Fatal("bounded read lost the full canonical selection", message)
		}
	}
	for _, older := range []bool{false, true} {
		if older {
			for range 50 {
				if err := restarted.AddMessage(t.Context(), chat.ID, &Message{Role: RoleUser, Content: "Another discussion."}); err != nil {
					t.Fatal(err)
				}
			}
		}
		_, exact, err := restarted.(AssistantContextMessageStore).ReadAssistantContextFolderEvent(t.Context(), chat.ID, owner, "", event.Observation.ID)
		if err != nil {
			t.Fatal("exact canonical reader", err)
		}
		check(exact)
		snapshot, err := restarted.(AssistantConversationContextStore).ReadAssistantConversationContext(t.Context(), chat.ID, owner)
		if err != nil {
			t.Fatal("model context reader", err)
		}
		var selected *Message
		for i := range snapshot.Recent {
			if snapshot.Recent[i].ID == rows[0].ID {
				selected = &snapshot.Recent[i]
			}
		}
		check(selected)
		display, _, err := restarted.(AssistantConversationDisplayStore).ReadAssistantConversationDisplay(t.Context(), chat.ID, owner)
		if err != nil {
			t.Fatal("display reader", err)
		}
		selected = nil
		for i := range display.Messages {
			if display.Messages[i].ID == rows[0].ID {
				selected = &display.Messages[i]
			}
		}
		check(selected)
		ref, err := restarted.(AssistantResearchFolderStore).ReadAssistantResearchFolder(t.Context(), chat.ID, owner)
		if err != nil || ref.Revision != rows[0].ID || strings.Join(ref.FocusIDs, ",") != strings.Join(event.FocusIDs, ",") {
			t.Fatal("research review binding lost selection", ref, err)
		}
	}
}
