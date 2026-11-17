package chathttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspaceupdates"
)

const (
	goalQuestion   = "What outcome would you like us to work toward?"
	noAssessmentLn = "I don’t have a saved assessment yet. Ask me about your goal, or choose a review in Goal settings when you’re ready."
)

// fakeFolderWelcome answers the same for every workspace and counts the asks.
type fakeFolderWelcome struct {
	hold  FolderWelcome
	reads int
}

func (f *fakeFolderWelcome) FolderWelcome(string) FolderWelcome {
	f.reads++
	return f.hold
}

// folderWelcomeFixture is an ordinary workspace with a lead and no goal and no
// saved assessment, the case whose welcome asks the goal question.
func folderWelcomeFixture(t *testing.T) (h *Handler, store workspace.Store, ws *workspace.Workspace, sessions session.HybridStore) {
	t.Helper()
	provider := &scriptedChatProvider{name: "ollama", supportsTools: true}
	h, _ = newCLIChatTestHandler(t, "ollama", "test-model", provider)
	sessions, closeSessions := setupWorkspaceToolSessionStore(t)
	t.Cleanup(closeSessions)
	store = session.NewWorkspaceStoreAdapter(sessions)
	_, _, ws = assessmentFixture(t)
	ws.AgentInstances[0].Name = "cli-agent"
	ws.Mission, ws.MissionRuns = "", nil
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	ws, err := store.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.SetWorkspaceStore(store)
	h.SetSessionStore(sessions)
	ag, _ := h.store.GetAgent("cli-agent")
	h.SetRuntimeResolver(&stubChatRuntimeResolver{resolved: &workspace.ResolvedAgentRuntime{Agent: ag, AgentInstance: &ws.AgentInstances[0], FromWorkspaceCopy: true}})
	return h, store, ws, sessions
}

func composeIntro(t *testing.T, h *Handler, ws *workspace.Workspace) (string, string) {
	t.Helper()
	input, state, err := h.ComposeLeadWelcome(t.Context(), "local", ws.ID)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	return input.Introduction, state
}

// A workspace that was not made by feeding a folder is unchanged, with no reader
// wired, with one that says none, and while it says "kept".
func TestComposeLeadWelcome_NotFedAndKeptAreUnchanged(t *testing.T) {
	h, _, ws, _ := folderWelcomeFixture(t)
	for name, reader := range map[string]folderWelcomeReader{
		"no reader":  nil,
		"not fed":    &fakeFolderWelcome{hold: FolderWelcomeNone},
		"kept as is": &fakeFolderWelcome{hold: FolderWelcomeAfterKept},
	} {
		t.Run(name, func(t *testing.T) {
			h.folderWelcome = reader
			intro, state := composeIntro(t, h, ws)
			if state != workspaceupdates.StateReady {
				t.Fatalf("state = %q, want ready", state)
			}
			for _, want := range []string{"Hi, I’m cli-agent", noAssessmentLn, goalQuestion} {
				if !strings.Contains(intro, want) {
					t.Errorf("missing %q in %q", want, intro)
				}
			}
		})
	}
}

// While the folder agent is asking, the welcome is not composed: it would ask a
// second, unrelated question. The worker keeps the enrolment and looks again.
func TestComposeLeadWelcome_HeldWhileTheFolderAgentIsAsking(t *testing.T) {
	h, _, ws, _ := folderWelcomeFixture(t)
	reader := &fakeFolderWelcome{hold: FolderWelcomeHold}
	h.SetFolderWelcomeReader(reader)
	input, state, err := h.ComposeLeadWelcome(t.Context(), "local", ws.ID)
	if err != nil || state != workspaceupdates.StateWaitingSetup || input.Introduction != "" || input.AgentInstanceID != "" {
		t.Fatalf("held compose = %+v %q %v", input, state, err)
	}
	if reader.reads != 1 {
		t.Fatalf("the reader was asked %d times", reader.reads)
	}
}

// The hold answers before the workspace-version fence: the folder agent writes
// the workspace (tickets) all through a look, and that must not turn "wait" into
// a conflict the worker treats as a failure.
func TestComposeLeadWelcome_HeldEvenWhileTheWorkspaceIsChanging(t *testing.T) {
	h, store, ws, _ := folderWelcomeFixture(t)
	// Every read after the first reports a newer version, as if the folder agent
	// wrote the workspace between the composer's two reads.
	changing := &versionBumpingStore{Store: store}
	h.SetWorkspaceStore(changing)

	// Control: with nothing held the composer meets that and reports a conflict,
	// so this fixture does provoke the fence the hold must come before.
	h.SetFolderWelcomeReader(&fakeFolderWelcome{hold: FolderWelcomeNone})
	if _, _, err := h.ComposeLeadWelcome(t.Context(), "local", ws.ID); !errors.Is(err, session.ErrWorkspaceVersionConflict) {
		t.Fatalf("control: compose with a changing workspace = %v, want a version conflict", err)
	}

	changing.gets = 0
	h.SetFolderWelcomeReader(&fakeFolderWelcome{hold: FolderWelcomeHold})
	_, state, err := h.ComposeLeadWelcome(t.Context(), "local", ws.ID)
	if err != nil || state != workspaceupdates.StateWaitingSetup {
		t.Fatalf("held compose while changing = %q %v, want waiting_setup and no error", state, err)
	}
}

// versionBumpingStore returns the workspace as stored on the first read and a
// copy with a newer version on every later one.
type versionBumpingStore struct {
	workspace.Store
	gets int
}

func (s *versionBumpingStore) Get(id string) (*workspace.Workspace, error) {
	ws, err := s.Store.Get(id)
	s.gets++
	if err != nil || ws == nil || s.gets == 1 {
		return ws, err
	}
	// A detached copy by JSON, as workspaceupdates.ReadWorkspace makes one (a
	// workspace holds a lock and is not copied by value).
	raw, err := json.Marshal(ws)
	if err != nil {
		return nil, err
	}
	var newer workspace.Workspace
	if err := json.Unmarshal(raw, &newer); err != nil {
		return nil, err
	}
	newer.Version = ws.Version + 1
	return &newer, nil
}

// After Set up the Commander offers a goal itself, so the welcome leaves out
// its goal question and keeps everything else.
func TestComposeLeadWelcome_AfterSetUpLeavesOutTheGoalQuestionOnly(t *testing.T) {
	h, _, ws, _ := folderWelcomeFixture(t)
	h.SetFolderWelcomeReader(&fakeFolderWelcome{hold: FolderWelcomeAfterSetUp})
	intro, state := composeIntro(t, h, ws)
	if state != workspaceupdates.StateReady {
		t.Fatalf("state = %q, want ready", state)
	}
	if strings.Contains(intro, goalQuestion) {
		t.Fatalf("the goal question is still asked after set up: %q", intro)
	}
	for _, want := range []string{"Hi, I’m cli-agent", "the lead for", noAssessmentLn} {
		if !strings.Contains(intro, want) {
			t.Errorf("missing %q in %q", want, intro)
		}
	}
}

// The browser's explicit open reaches the same composer: while held it answers
// waiting_setup and writes nothing.
func TestLeadConversationOpen_HeldAnswersWaitingSetupAndWritesNothing(t *testing.T) {
	h, _, ws, sessions := folderWelcomeFixture(t)
	h.SetFolderWelcomeReader(&fakeFolderWelcome{hold: FolderWelcomeHold})
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.SetPathValue("workspaceID", ws.ID)
	w := httptest.NewRecorder()
	h.LeadConversationOpen(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "waiting_setup") || strings.Contains(w.Body.String(), `"changed":true`) {
		t.Fatalf("held open = %d %s", w.Code, w.Body.String())
	}
	var count int
	if err := sessions.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("a held welcome wrote %d conversations (%v)", count, err)
	}
}
