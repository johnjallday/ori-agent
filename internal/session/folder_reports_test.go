package session

import (
	"errors"
	"testing"
	"time"
)

func folderReportWorkspace(t *testing.T, store HybridStore, id string) {
	t.Helper()
	if err := store.CreateWorkspace(t.Context(), &Workspace{ID: id, Name: "Downloads", OwnerUserID: "local", Status: WorkspaceStatusActive}); err != nil {
		t.Fatal(err)
	}
}

func TestFolderReport_NewConversationOncePerLookAndUnreadUntilSeen(t *testing.T) {
	store, closeStore := setupHybridStore(t, 10)
	defer closeStore()
	h := store.(*hybridStore)
	folderReportWorkspace(t, store, "ws")
	in := FolderReportInput{WorkspaceID: "ws", AgentName: "Downloads Manager", ReportKey: "reported@1", Content: "I looked around Downloads.", Title: "Plan for Downloads"}

	first, changed, err := h.DeliverFolderReport(t.Context(), in)
	if err != nil || !changed || first.SessionID == "" || first.MessageID == "" {
		t.Fatalf("first delivery %+v changed=%v err=%v", first, changed, err)
	}
	// Announced again (a sweep after a restart): the same message, not a second.
	again, changed, err := h.DeliverFolderReport(t.Context(), in)
	if err != nil || changed || again.MessageID != first.MessageID {
		t.Fatalf("repeat delivery %+v changed=%v err=%v", again, changed, err)
	}
	loaded, err := store.GetSession(t.Context(), first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Title != "Plan for Downloads" || loaded.AgentName != "Downloads Manager" || len(loaded.Messages) != 1 ||
		loaded.Messages[0].Role != "assistant" || loaded.Messages[0].Content != in.Content {
		t.Fatalf("conversation %+v", loaded)
	}

	latest, err := h.LatestFolderReport(t.Context(), "ws")
	if err != nil || latest.MessageID != first.MessageID || latest.SeenAt != nil {
		t.Fatalf("latest %+v err=%v", latest, err)
	}
	if err := h.AcknowledgeFolderReport(t.Context(), "ws", "not-a-report"); !errors.Is(err, ErrFolderReportUnavailable) {
		t.Fatalf("acknowledging another message: %v", err)
	}
	if err := h.AcknowledgeFolderReport(t.Context(), "ws", first.MessageID); err != nil {
		t.Fatal(err)
	}
	if latest, err = h.LatestFolderReport(t.Context(), "ws"); err != nil || latest.SeenAt == nil {
		t.Fatalf("seen %+v err=%v", latest, err)
	}

	// The next look's report is a new message in the same conversation, and the
	// latest one again unread.
	in.ReportKey, in.Content = "reported@2", "I looked further."
	second, changed, err := h.DeliverFolderReport(t.Context(), in)
	if err != nil || !changed || second.SessionID != first.SessionID || second.MessageID == first.MessageID {
		t.Fatalf("second look %+v changed=%v err=%v", second, changed, err)
	}
	if latest, err = h.LatestFolderReport(t.Context(), "ws"); err != nil || latest.MessageID != second.MessageID || latest.SeenAt != nil {
		t.Fatalf("latest after second look %+v err=%v", latest, err)
	}
	if loaded, err = store.GetSession(t.Context(), first.SessionID); err != nil || len(loaded.Messages) != 2 {
		t.Fatalf("stale cached conversation: %+v %v", loaded, err)
	}
}

func TestFolderReport_GoesToTheAgentsMostRecentConversation(t *testing.T) {
	store, closeStore := setupHybridStore(t, 10)
	defer closeStore()
	h := store.(*hybridStore)
	folderReportWorkspace(t, store, "ws")
	older := &Session{ID: "older", Title: "Getting started", AgentName: "Downloads Manager", FolderID: "ws"}
	newer := &Session{ID: "newer", Title: "Talk", AgentName: "Downloads Manager", FolderID: "ws"}
	other := &Session{ID: "other", Title: "Someone else", AgentName: "Journal", FolderID: "ws"}
	for _, s := range []*Session{older, newer, other} {
		if err := store.CreateSession(t.Context(), s); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(5 * time.Millisecond)
	if err := store.AddMessage(t.Context(), "newer", &Message{Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := store.AddMessage(t.Context(), "other", &Message{Role: "user", Content: "unrelated"}); err != nil {
		t.Fatal(err)
	}
	out, changed, err := h.DeliverFolderReport(t.Context(), FolderReportInput{WorkspaceID: "ws", AgentName: "downloads manager", ReportKey: "reported@1", Content: "Plan."})
	if err != nil || !changed || out.SessionID != "newer" {
		t.Fatalf("delivered to %+v changed=%v err=%v, want the agent's most recent conversation", out, changed, err)
	}
}

func TestFolderReport_RefusesGoneWorkspacesAndForgetsDeletedConversations(t *testing.T) {
	store, closeStore := setupHybridStore(t, 10)
	defer closeStore()
	h := store.(*hybridStore)
	if _, _, err := h.DeliverFolderReport(t.Context(), FolderReportInput{WorkspaceID: "nowhere", AgentName: "A", ReportKey: "k", Content: "x"}); !errors.Is(err, ErrFolderReportUnavailable) {
		t.Fatalf("missing workspace: %v", err)
	}
	if _, _, err := h.DeliverFolderReport(t.Context(), FolderReportInput{WorkspaceID: "ws", AgentName: "A", ReportKey: "", Content: "x"}); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("empty key: %v", err)
	}
	folderReportWorkspace(t, store, "ws")
	out, _, err := h.DeliverFolderReport(t.Context(), FolderReportInput{WorkspaceID: "ws", AgentName: "A", ReportKey: "k", Content: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSession(t.Context(), out.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.LatestFolderReport(t.Context(), "ws"); !errors.Is(err, ErrFolderReportUnavailable) {
		t.Fatalf("a deleted conversation's report is still offered: %v", err)
	}
	// The same look is not posted again into a new conversation.
	if again, changed, err := h.DeliverFolderReport(t.Context(), FolderReportInput{WorkspaceID: "ws", AgentName: "A", ReportKey: "k", Content: "x"}); err != nil || changed || again.MessageID != out.MessageID {
		t.Fatalf("deleted report resurrected: %+v changed=%v err=%v", again, changed, err)
	}
}
