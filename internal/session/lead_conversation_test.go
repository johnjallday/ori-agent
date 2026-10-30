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
