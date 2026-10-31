package chathttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type stubCuratorWelcomeReader struct {
	value filejanitor.CuratorWelcome
	err   error
	calls [][3]string
}

func (s *stubCuratorWelcomeReader) ReadCuratorWelcome(_ context.Context, owner, ws, instance string) (filejanitor.CuratorWelcome, error) {
	s.calls = append(s.calls, [3]string{owner, ws, instance})
	return s.value, s.err
}

func TestFileCuratorWelcome_OffersOnlyStateBackedChoices(t *testing.T) {
	for _, tc := range []struct {
		name      string
		saved     filejanitor.CuratorWelcome
		available bool
		want      string
		uncertain bool
		choices   int
	}{
		{"batch with uncertainty", filejanitor.CuratorWelcome{Configured: true, Pending: 3, NeedsReview: 1}, true, "3 items waiting for review", true, 3},
		{"confident batch", filejanitor.CuratorWelcome{Configured: true, Pending: 1}, true, "1 item waiting for review", false, 2},
		{"no batch", filejanitor.CuratorWelcome{Configured: true}, true, "choose Scan now", false, 2},
		{"setup missing", filejanitor.CuratorWelcome{}, true, "existing setup controls", false, 2},
		{"unavailable", filejanitor.CuratorWelcome{Configured: true, Pending: 99, NeedsReview: 99}, false, "can’t verify", false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := fileCuratorWelcomeText(tc.saved, tc.available)
			if !strings.Contains(text, tc.want) || strings.Contains(text, "Discuss uncertain files") != tc.uncertain {
				t.Fatalf("wrong choices: %s", text)
			}
			if strings.Count(text, "\n1. ") != 1 || strings.Count(text, "\n2. ") != 1 || strings.Contains(text, "\n3. ") != (tc.choices == 3) || strings.Contains(text, "\n4. ") {
				t.Fatalf("choice count: %s", text)
			}
			for _, want := range []string{"I’d start", "Understand the filing rules", "reply with a number", "does not scan or approve anything", "explicit approval in the review screen"} {
				if !strings.Contains(text, want) {
					t.Errorf("missing %q: %s", want, text)
				}
			}
			for _, bad := range []string{"Goal settings", "saved assessment", "99", "I have scanned", "I’ll move"} {
				if strings.Contains(text, bad) {
					t.Errorf("unsupported claim %q: %s", bad, text)
				}
			}
			if tc.name == "no batch" && !strings.Contains(text, "doesn’t mean the folder is empty") {
				t.Fatal("inferred an empty folder from absent batch")
			}
		})
	}
}

func TestFileCuratorWelcome_UsesBatchDateNotReadTime(t *testing.T) {
	saved := filejanitor.CuratorWelcome{Configured: true, Pending: 2, BatchCompletedAt: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC), ReadAt: time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)}
	text := fileCuratorWelcomeText(saved, true)
	if !strings.Contains(text, "batch completed 2026-10-20 09:00 UTC") || !strings.Contains(text, "read 2026-10-30 10:00 UTC") {
		t.Fatal(text)
	}
}

func TestLeadConversationOpen_CuratorUsesFilingStateWithoutGoalAssessment(t *testing.T) {
	for _, mode := range []string{"new", "old unanswered welcome", "saved goal review", "unavailable reader"} {
		t.Run(mode, func(t *testing.T) {
			provider := &scriptedChatProvider{name: "ollama", supportsTools: true}
			h, mutations := newCLIChatTestHandler(t, "ollama", "test-model", provider)
			sessions, cleanup := setupWorkspaceToolSessionStore(t)
			defer cleanup()
			_, canonical, ws := assessmentFixture(t)
			ws.AgentInstances[0].Name = "cli-agent"
			ws.InstalledCapabilities = []workspace.InstalledCapability{{ID: workspace.CapabilityFileJanitor, Version: 1, InstalledAt: time.Now(), Source: workspace.InstallSourceInPlace, OwnedResources: []workspace.CapabilityResource{{Kind: workspace.ResourceCompanionAgent, ID: ws.AgentInstances[0].ID}}}}
			if mode != "saved goal review" {
				ws.MissionRuns = nil
			}
			adapter := session.NewWorkspaceStoreAdapter(sessions)
			if err := adapter.Save(ws); err != nil {
				t.Fatal(err)
			}
			stored, err := adapter.Get(ws.ID)
			if err != nil {
				t.Fatal(err)
			}
			ws.Version = stored.Version
			if err := canonical.Save(ws); err != nil {
				t.Fatal(err)
			}
			h.SetWorkspaceStore(canonical)
			h.SetSessionStore(sessions)
			ag, _ := h.store.GetAgent("cli-agent")
			h.SetRuntimeResolver(&stubChatRuntimeResolver{resolved: &workspace.ResolvedAgentRuntime{Agent: ag, AgentInstance: &ws.AgentInstances[0], FromWorkspaceCopy: true}})
			reader := &stubCuratorWelcomeReader{value: filejanitor.CuratorWelcome{Configured: true, Pending: 2, NeedsReview: 1}}
			if mode == "unavailable reader" {
				reader.err = errors.New("PRIVATE PROVIDER PATH")
			}
			h.SetFileCuratorWelcomeReader(reader)
			if mode == "old unanswered welcome" {
				_, err := sessions.(leadConversationStore).EnsureLeadConversation(t.Context(), session.LeadConversationInput{WorkspaceID: ws.ID, WorkspaceVersion: ws.Version, OwnerUserID: "local", AgentName: "cli-agent", AgentInstanceID: ws.AgentInstances[0].ID, Introduction: "Old generic welcome"})
				if err != nil {
					t.Fatal(err)
				}
			}
			before, _ := json.Marshal(ws)
			var id string
			for i := range 2 {
				r := httptest.NewRequest(http.MethodPost, "/", nil)
				r.SetPathValue("workspaceID", ws.ID)
				w := httptest.NewRecorder()
				h.LeadConversationOpen(w, r)
				var out session.LeadConversationResult
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Changed != (i == 0) || out.AwaitingAssessment {
					t.Fatalf("welcome %d %s", w.Code, w.Body.String())
				}
				if id != "" && out.SessionID != id {
					t.Fatal("created another conversation")
				}
				id = out.SessionID
			}
			conversation, err := sessions.GetSession(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			wantMessages := 1
			if mode == "old unanswered welcome" {
				wantMessages = 2
			}
			if len(conversation.Messages) != wantMessages {
				t.Fatalf("messages: %+v", conversation.Messages)
			}
			text := conversation.Messages[len(conversation.Messages)-1].Content
			if !strings.Contains(text, "What would you like to do?") || strings.Contains(text, "saved assessment") || strings.Contains(text, "Goal settings") || strings.Contains(text, "PRIVATE PROVIDER PATH") {
				t.Fatalf("wrong fallback: %s", text)
			}
			if mode != "unavailable reader" && !strings.Contains(text, "Discuss uncertain files") {
				t.Fatal(text)
			}
			for _, call := range reader.calls {
				if call != [3]string{"local", ws.ID, ws.AgentInstances[0].ID} {
					t.Fatalf("bad scope: %v", call)
				}
			}
			fresh, _ := canonical.Get(ws.ID)
			after, _ := json.Marshal(fresh)
			if string(before) != string(after) || len(provider.requests) != 0 || len(*mutations) != 0 {
				t.Fatal("welcome ran work or changed workspace")
			}
		})
	}
}

func TestWorkspaceAssessmentPrompt_KeepsCuratorChoicesSeparateFromGoalReviews(t *testing.T) {
	p, store, ws := assessmentFixture(t)
	ag := &resolvedChatAgent{WorkspaceTools: p}
	if strings.Contains(workspaceAssessmentPrompt(ag), "For File Curator:") {
		t.Fatal("ordinary lead got filing guidance")
	}
	ws.InstalledCapabilities = []workspace.InstalledCapability{{ID: workspace.CapabilityFileJanitor, Version: 1, InstalledAt: time.Now(), Source: workspace.InstallSourceInPlace, OwnedResources: []workspace.CapabilityResource{{Kind: workspace.ResourceCompanionAgent, ID: ws.AgentInstances[0].ID}}}}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	guidance := workspaceAssessmentPrompt(ag)
	for _, want := range []string{"filing reviews are separate from goal assessments", "numbered choice selects a discussion topic", "not scan or file-action approval", "rather than inventing filenames"} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("missing %q: %s", want, guidance)
		}
	}
}

func TestLeadWelcomeContext_DoesNotInferCuratorFromName(t *testing.T) {
	_, _, ws := assessmentFixture(t)
	ws.MissionRuns = nil
	ws.AgentInstances[0].Name = "File Curator"
	reader := &stubCuratorWelcomeReader{}
	h := &Handler{fileCuratorWelcome: reader}
	if got := h.leadWelcomeContext(t.Context(), ws, "local", ws.AgentInstances[0].ID); got != "" || len(reader.calls) != 0 {
		t.Fatal("a name granted filing context")
	}
}
