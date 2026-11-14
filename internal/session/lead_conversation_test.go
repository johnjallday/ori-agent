package session

import (
	"sync"
	"testing"
	"time"
)

func TestLeadConversation_OnceAcrossTabsAndDeletedHistory(t *testing.T) {
	store, closeStore := setupHybridStore(t, 10)
	defer closeStore()
	h := store.(*hybridStore)
	if err := store.CreateWorkspace(t.Context(), &Workspace{ID: "ws", Name: "Desk", OwnerUserID: "local", Status: WorkspaceStatusActive}); err != nil {
		t.Fatal(err)
	}
	in := LeadConversationInput{WorkspaceID: "ws", OwnerUserID: "local", AgentName: "Mira", AgentInstanceID: "mira-local", Introduction: "Hello."}
	var wg sync.WaitGroup
	results := make(chan LeadConversationResult, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := h.EnsureLeadConversation(t.Context(), in)
			if err != nil {
				t.Error(err)
				return
			}
			results <- out
		}()
	}
	wg.Wait()
	close(results)
	id, changed := "", 0
	for out := range results {
		if id != "" && id != out.SessionID {
			t.Fatal("multiple conversations")
		}
		id = out.SessionID
		if out.Changed {
			changed++
		}
	}
	if id == "" || changed != 1 {
		t.Fatalf("delivery id=%s changes=%d", id, changed)
	}
	first, err := store.GetSession(t.Context(), id) // prime cache before second delivery
	if err != nil || len(first.Messages) != 1 {
		t.Fatalf("intro %v %v", first, err)
	}
	in.Assessment = "Saved review: start with the existing outline."
	for i := range 2 {
		out, err := h.EnsureLeadConversation(t.Context(), in)
		if err != nil || out.Changed != (i == 0) || out.AwaitingAssessment {
			t.Fatalf("assessment delivery %+v %v", out, err)
		}
	}
	loaded, err := store.GetSession(t.Context(), id)
	if err != nil || len(loaded.Messages) != 2 || loaded.Messages[1].Content != in.Assessment {
		t.Fatalf("stale cached conversation: %+v %v", loaded, err)
	}
	if err := store.DeleteSession(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	out, err := h.EnsureLeadConversation(t.Context(), in)
	if err != nil || out.Changed || out.SessionID != "" {
		t.Fatalf("deleted conversation resurrected: %+v %v", out, err)
	}
}

func TestLeadConversation_FolderReportBeforeWelcome(t *testing.T) {
	for _, mode := range []string{"report only", "user replied", "other assistant message", "not enrolled", "second conversation", "deleted report"} {
		t.Run(mode, func(t *testing.T) {
			store, cleanup := setupHybridStore(t, 10)
			defer cleanup()
			h := store.(*hybridStore)
			if err := store.CreateWorkspace(t.Context(), &Workspace{ID: "ws", Name: "Downloads", OwnerUserID: "local", Status: WorkspaceStatusActive}); err != nil {
				t.Fatal(err)
			}
			in := LeadConversationInput{WorkspaceID: "ws", OwnerUserID: "local", AgentName: "Downloads Manager", AgentInstanceID: "hired", Introduction: "Hello."}
			if mode != "not enrolled" {
				if err := h.EnrollWelcome(t.Context(), WelcomeScope{WorkspaceID: "ws", OwnerUserID: "local", AgentName: in.AgentName, AgentInstanceID: "hired"}); err != nil {
					t.Fatal(err)
				}
			}
			report, _, err := h.DeliverFolderReport(t.Context(), FolderReportInput{WorkspaceID: "ws", AgentName: in.AgentName, ReportKey: "look-1", Content: "Folder look report."})
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "user replied", "other assistant message":
				role := RoleUser
				if mode == "other assistant message" {
					role = RoleAssistant
				}
				if err := store.AddMessage(t.Context(), report.SessionID, &Message{Role: role, Content: "Another conversation", CreatedAt: time.Now()}); err != nil {
					t.Fatal(err)
				}
			case "second conversation":
				if err := store.CreateSession(t.Context(), &Session{AgentName: in.AgentName, FolderID: "ws"}); err != nil {
					t.Fatal(err)
				}
			case "deleted report":
				if err := store.DeleteSession(t.Context(), report.SessionID); err != nil {
					t.Fatal(err)
				}
				// An existing conversation is never adopted merely because an
				// old folder-report receipt exists for the same workspace.
				if err := store.CreateSession(t.Context(), &Session{AgentName: in.AgentName, FolderID: "ws"}); err != nil {
					t.Fatal(err)
				}
			}
			first, err := h.EnsureLeadConversation(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "report only" {
				if first.Changed || first.SessionID != "" {
					t.Fatal("adopted an unproven or user-owned conversation", first)
				}
				return
			}
			if first.SessionID != report.SessionID || !first.Changed || !first.AwaitingAssessment {
				t.Fatal("host report suppressed the pending welcome", first)
			}
			in.Assessment = "Saved filing batch ready for review."
			for run := range 2 {
				out, err := h.EnsureLeadConversation(t.Context(), in)
				if err != nil || out.SessionID != report.SessionID || out.Changed != (run == 0) || out.AwaitingAssessment {
					t.Fatal("filing welcome missing or repeated", out, err)
				}
			}
			conversation, err := store.GetSession(t.Context(), report.SessionID)
			if err != nil || len(conversation.Messages) != 3 || conversation.Messages[2].Content != in.Assessment {
				t.Fatal("unexpected conversation", conversation, err)
			}
		})
	}
}

func TestLeadConversation_FencesOwnershipVersionAndUserConversations(t *testing.T) {
	for _, mode := range []string{"existing", "user replied", "owner", "version", "moved", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			store, cleanup := setupHybridStore(t, 10)
			defer cleanup()
			h := store.(*hybridStore)
			if err := store.CreateWorkspace(t.Context(), &Workspace{ID: "ws", Name: "Desk", OwnerUserID: "local", Status: WorkspaceStatusActive}); err != nil {
				t.Fatal(err)
			}
			in := LeadConversationInput{WorkspaceID: "ws", OwnerUserID: "local", AgentName: "Mira", AgentInstanceID: "mira-local", Introduction: "Hello."}
			if mode == "existing" {
				if err := store.CreateSession(t.Context(), &Session{AgentName: "Mira", FolderID: "ws"}); err != nil {
					t.Fatal(err)
				}
			}
			first, err := h.EnsureLeadConversation(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "existing":
				if first.Changed || first.SessionID != "" {
					t.Fatal("hijacked existing conversation")
				}
			case "user replied":
				if err := store.AddMessage(t.Context(), first.SessionID, &Message{Role: RoleUser, Content: "I have a different priority.", CreatedAt: time.Now()}); err != nil {
					t.Fatal(err)
				}
			case "owner":
				in.OwnerUserID = "other"
			case "version":
				in.WorkspaceVersion++
			case "replacement":
				in.AgentInstanceID = "replacement"
			case "moved":
				s, err := store.GetSession(t.Context(), first.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				s.FolderID = ""
				if err := store.UpdateSession(t.Context(), s); err != nil {
					t.Fatal(err)
				}
			}
			in.Assessment = "Unsolicited advice"
			out, err := h.EnsureLeadConversation(t.Context(), in)
			if mode == "owner" || mode == "version" {
				if err == nil {
					t.Fatal("stale authority accepted")
				}
				return
			}
			if err != nil || out.Changed {
				t.Fatalf("unexpected delivery %+v %v", out, err)
			}
		})
	}
}
