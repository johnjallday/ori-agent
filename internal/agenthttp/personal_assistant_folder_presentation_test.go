package agenthttp

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

func TestAssistantFolderPresentation_RequiresAnsweredCanonicalTurnInProviderWindow(t *testing.T) {
	observation := &foldercontext.Observation{ID: "snapshot"}
	turn := &preparedFolderTurn{observation: observation}
	base := []PersonalAssistantConversationMessage{
		{ID: "event", Role: "system", FolderContext: &foldercontext.Event{Version: 1, Observation: observation}},
		{ID: "user", Role: "user", Content: "Explore this folder"},
		{ID: "answer", Role: "assistant", Content: "A short answer."},
	}
	for _, kind := range []string{"answered", "event-only-review", "imported-event", "imported-user", "imported-answer", "different-snapshot", "wrong-event-role", "unsupported-event", "missing-event-id", "no-answer", "trimmed-window"} {
		t.Run(kind, func(t *testing.T) {
			rows := append([]PersonalAssistantConversationMessage(nil), base...)
			switch kind {
			case "event-only-review":
				rows = rows[:1]
			case "imported-event":
				rows[0].Imported = true
			case "imported-user":
				rows[1].Imported = true
			case "imported-answer":
				rows[2].Imported = true
			case "different-snapshot":
				rows[0].FolderContext = &foldercontext.Event{Version: 1, Observation: &foldercontext.Observation{ID: "other"}}
			case "wrong-event-role":
				rows[0].Role = "assistant"
			case "unsupported-event":
				rows[0].FolderContext = &foldercontext.Event{Version: 2, Observation: observation}
			case "missing-event-id":
				rows[0].ID = ""
			case "no-answer":
				rows[2].Content = ""
			case "trimmed-window":
				for i := 0; i < personalAssistantConversationHistoryMessages; i++ {
					rows = append(rows, PersonalAssistantConversationMessage{ID: "later", Role: "user", Content: "Later text"})
				}
			}
			history, truncated, ids := conversationHistoryWindowWithIDs(rows)
			conversation := &openConversation{messages: rows, history: history, truncated: truncated, historyMessageIDs: ids}
			text := folderReplyPresentation(conversation, turn)
			if strings.Contains(text, "Treat this as a follow-up") != (kind == "answered") {
				t.Fatalf("wrong canonical reply context: %s", text)
			}
		})
	}
}

func TestAssistantFolderPresentation_RootFlagIsDataNotAnIndependentTotal(t *testing.T) {
	text, err := folderObservationPrompt(&preparedFolderTurn{observation: &foldercontext.Observation{Projects: []foldercontext.Project{
		{ID: "private-id", Name: "Root", Files: 2, Root: true},
		{ID: "child-id", Name: "Child", Files: 2},
	}}})
	if err != nil || !strings.Contains(text, `"root":true`) || strings.Contains(text, "private-id") || strings.Contains(text, "child-id") {
		t.Fatalf("invalid root presentation: %s %v", text, err)
	}
}

// The model is given each project's last-changed day, so it can say which was
// worked on most recently; a project without one is simply given none.
func TestAssistantFolderPresentation_ProjectsCarryTheirLastChangedDay(t *testing.T) {
	text, err := folderObservationPrompt(&preparedFolderTurn{observation: &foldercontext.Observation{Projects: []foldercontext.Project{
		{ID: "a", Name: "Night Drive", Files: 4, LastChanged: "2026-10-09"},
		{ID: "b", Name: "Empty Idea"},
	}}})
	if err != nil || !strings.Contains(text, `{"name":"Night Drive","files":4,"last_changed":"2026-10-09"}`) ||
		!strings.Contains(text, `{"name":"Empty Idea","files":0}`) {
		t.Fatalf("project dates in the prompt: %s %v", text, err)
	}
	if !strings.Contains(folderConversationInstructions, "last_changed is the date of the newest file seen") {
		t.Fatal("the model is not told what last_changed means")
	}
}
