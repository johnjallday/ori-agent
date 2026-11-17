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

// A fed folder's welcome is held until the folder is set up or kept, so the
// Commander's question now creates the thread (titled for the folder) and the
// welcome joins it later. A workspace from before the hold has the welcome
// first, and the report lands in that thread. Either order is one thread.
func TestLeadConversation_TheQuestionAndTheWelcomeShareOneThreadInEitherOrder(t *testing.T) {
	setup := func(t *testing.T) (*hybridStore, LeadConversationInput, func()) {
		store, cleanup := setupHybridStore(t, 10)
		h := store.(*hybridStore)
		if err := store.CreateWorkspace(t.Context(), &Workspace{ID: "ws", Name: "Albums", OwnerUserID: "local", Status: WorkspaceStatusActive}); err != nil {
			t.Fatal(err)
		}
		in := LeadConversationInput{WorkspaceID: "ws", OwnerUserID: "local", AgentName: "Albums Manager", AgentInstanceID: "albums-lead", Introduction: "Hi, I’m Albums Manager."}
		if err := h.EnrollWelcome(t.Context(), WelcomeScope{WorkspaceID: "ws", OwnerUserID: "local", AgentName: in.AgentName, AgentInstanceID: in.AgentInstanceID}); err != nil {
			t.Fatal(err)
		}
		return h, in, cleanup
	}
	question := FolderReportInput{WorkspaceID: "ws", AgentName: "Albums Manager", ReportKey: "reported@1", Title: "Plan for Albums",
		Content: "I looked around Albums. One question first:\n\nThis looks like a music production folder."}
	contents := func(t *testing.T, h *hybridStore, id string) (string, []string) {
		t.Helper()
		loaded, err := h.GetSession(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range loaded.Messages {
			out = append(out, m.Content)
		}
		return loaded.Title, out
	}

	t.Run("held welcome: the question creates the thread and the welcome joins it", func(t *testing.T) {
		h, in, cleanup := setup(t)
		defer cleanup()
		report, changed, err := h.DeliverFolderReport(t.Context(), question)
		if err != nil || !changed {
			t.Fatalf("report: %+v %v %v", report, changed, err)
		}
		welcome, err := h.EnsureLeadConversation(t.Context(), in)
		if err != nil || !welcome.Changed || welcome.SessionID != report.SessionID {
			t.Fatalf("the welcome did not join the question's thread: %+v %v (report in %s)", welcome, err, report.SessionID)
		}
		title, got := contents(t, h, report.SessionID)
		if title != "Plan for Albums" || len(got) != 2 || got[0] != question.Content || got[1] != in.Introduction {
			t.Fatalf("thread %q = %q, want the question then the welcome", title, got)
		}
		// The newest report is still the question's own message.
		if latest, err := h.LatestFolderReport(t.Context(), "ws"); err != nil || latest.MessageID != report.MessageID {
			t.Fatalf("latest report %+v %v", latest, err)
		}
	})

	t.Run("welcome first (a workspace from before): the report lands in the welcome's thread", func(t *testing.T) {
		h, in, cleanup := setup(t)
		defer cleanup()
		welcome, err := h.EnsureLeadConversation(t.Context(), in)
		if err != nil || !welcome.Changed || welcome.SessionID == "" {
			t.Fatalf("welcome: %+v %v", welcome, err)
		}
		report, changed, err := h.DeliverFolderReport(t.Context(), question)
		if err != nil || !changed || report.SessionID != welcome.SessionID {
			t.Fatalf("the report did not join the welcome's thread: %+v %v %v", report, changed, err)
		}
		title, got := contents(t, h, welcome.SessionID)
		if title != "Getting started" || len(got) != 2 || got[0] != in.Introduction || got[1] != question.Content {
			t.Fatalf("thread %q = %q, want the welcome then the question", title, got)
		}
	})

	t.Run("the owner already typed: the held welcome is not posted into their conversation", func(t *testing.T) {
		h, in, cleanup := setup(t)
		defer cleanup()
		report, _, err := h.DeliverFolderReport(t.Context(), question)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.AddMessage(t.Context(), report.SessionID, &Message{Role: RoleUser, Content: "What is this?", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		welcome, err := h.EnsureLeadConversation(t.Context(), in)
		if err != nil || welcome.Changed || welcome.SessionID != "" || welcome.AwaitingAssessment {
			t.Fatalf("a held welcome was posted into a conversation the owner started: %+v %v", welcome, err)
		}
		if _, got := contents(t, h, report.SessionID); len(got) != 2 {
			t.Fatalf("thread = %q, want only the question and the owner's message", got)
		}
	})
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
