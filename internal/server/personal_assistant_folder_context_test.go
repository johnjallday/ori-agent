package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/session"
)

// Only selection I/O is substituted in these handler-boundary tests. Canonical
// sessions, HTTP decoders, ownership/revision checks and the provider are real
// production adapters (the provider captures requests without contacting a vendor).
type folderObservationStub struct {
	observation foldercontext.Observation
	calls       int
	target      foldercontext.Target
	onObserve   func()
	status      personalassistant.FolderContinuationReason
	statusCalls int
	saved       bool
}

func newFolderObservationStub() *folderObservationStub {
	return &folderObservationStub{observation: foldercontext.Observation{Version: 1, ID: "selection-a", Folder: "Fixture", ScannedAt: time.Now().UTC(), Entries: 2, Files: 1,
		Kinds: []foldercontext.Kind{{Name: ".md", Count: 1}}, Projects: []foldercontext.Project{{ID: "root", Name: "Fixture", Files: 1}},
		Coverage: foldercontext.Coverage{MaxDepth: 3, MaxEntries: 5000, BudgetSeconds: 3}}}
}
func (s *folderObservationStub) Choices(context.Context, string) (personalassistant.FolderDigestView, error) {
	return personalassistant.FolderDigestView{Chips: []personalassistant.FolderChip{{ID: "documents", Label: "Documents"}}}, nil
}
func (s *folderObservationStub) Observe(_ context.Context, target foldercontext.Target, mode, chip string) (*foldercontext.Observation, error) {
	s.calls++
	s.target = target
	s.saved = false
	if s.onObserve != nil {
		s.onObserve()
	}
	if mode != "chip" || chip != "documents" {
		return nil, personalassistant.ErrFolderSelection
	}
	copy := s.observation
	return &copy, nil
}
func (s *folderObservationStub) Resolve(_ context.Context, target foldercontext.Target, id string) (*foldercontext.Observation, error) {
	if target != s.target || id != s.observation.ID || s.status != "" {
		return nil, personalassistant.ErrFolderSelection
	}
	copy := s.observation
	return &copy, nil
}
func (s *folderObservationStub) Status(context.Context, foldercontext.Target, foldercontext.Observation) personalassistant.FolderContinuationReason {
	s.statusCalls++
	return s.status
}
func (s *folderObservationStub) BindSaved(target foldercontext.Target, id, conversationID string) {
	if target == s.target && id == s.observation.ID {
		s.target.ConversationID, s.target.DraftID = conversationID, ""
		s.saved = true
	}
}
func (s *folderObservationStub) WasSaved(target foldercontext.Target, id string) bool {
	return s.saved && target == s.target && id == s.observation.ID
}

func folderHTTP(handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/home-assistant/folder-context/select", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	handler(w, r)
	return w
}

func TestAssistantFolderSelection_StrictLocalBoundary(t *testing.T) {
	f := newConversationServerFixture(t)
	observations := newFolderObservationStub()
	f.handler.FolderObservations = observations
	for _, body := range []string{
		`{"draft_id":"` + uuid.NewString() + `","mode":"chip","chip":"documents","path":"/private"}`,
		`{"draft_id":"` + uuid.NewString() + `","mode":"chip","chip":"documents","observation":{"files":999}}`,
		`{"draft_id":"bad","mode":"chip","chip":"documents"}`,
		`{"draft_id":"` + uuid.NewString() + `","mode":"chip","chip":"documents"} {}`,
		`{"conversation_id":"deleted","mode":"chip","chip":"documents"}`,
		`{"draft_id":"` + uuid.NewString() + `","revision":"forged","mode":"chip","chip":"documents"}`,
		`{"chip":"` + strings.Repeat("x", 2100) + `"}`,
	} {
		w := folderHTTP(f.handler.SelectFolderContextHandler, body)
		if w.Code < 400 {
			t.Fatalf("accepted %s: %s", body, w.Body.String())
		}
	}
	if observations.calls != 0 {
		t.Fatal("invalid request reached scanner")
	}
	w := folderHTTP(f.handler.SelectFolderContextHandler, `{"draft_id":"`+uuid.NewString()+`","mode":"chip","chip":"documents"}`)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	if observations.calls != 1 || observations.target.WorkspaceID != f.hq || observations.target.UserID != "local" {
		t.Fatalf("wrong binding: %+v", observations.target)
	}
	if len(f.provider.requests) != 0 {
		t.Fatal("selection called model")
	}
	list, err := f.sessions.ListSessions(context.Background(), nil, nil)
	if err != nil || len(list.Sessions) != 0 {
		t.Fatalf("selection created session: %+v %v", list, err)
	}
}

func TestAssistantFolderSelection_RejectsForeignStaleAndDeletedDuringScan(t *testing.T) {
	f := newConversationServerFixture(t)
	observations := newFolderObservationStub()
	f.handler.FolderObservations = observations
	ctx := context.Background()
	foreign := &session.Session{AgentName: "Other", FolderID: f.hq}
	if err := f.sessions.CreateSession(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	w := folderHTTP(f.handler.SelectFolderContextHandler, `{"conversation_id":"`+foreign.ID+`","mode":"chip","chip":"documents"}`)
	if w.Code < 400 || observations.calls != 0 {
		t.Fatal("foreign selection accepted")
	}
	owned := &session.Session{AgentName: "Atlas", FolderID: f.hq}
	if err := f.sessions.CreateSession(ctx, owned); err != nil {
		t.Fatal(err)
	}
	rows, err := f.sessions.(session.FolderContextStore).AppendFolderTurn(ctx, owned.ID, f.hq, "Atlas", "", foldercontext.Event{Version: 1, Observation: &observations.observation}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	w = folderHTTP(f.handler.SelectFolderContextHandler, `{"conversation_id":"`+owned.ID+`","mode":"chip","chip":"documents"}`)
	if w.Code < 400 || observations.calls != 0 {
		t.Fatal("stale selection accepted")
	}
	observations.onObserve = func() {
		if err := f.sessions.DeleteSession(ctx, owned.ID); err != nil {
			t.Fatal(err)
		}
	}
	w = folderHTTP(f.handler.SelectFolderContextHandler, `{"conversation_id":"`+owned.ID+`","revision":"`+rows[0].ID+`","mode":"chip","chip":"documents"}`)
	if w.Code < 400 || strings.Contains(w.Body.String(), "Fixture") {
		t.Fatal("published after deletion:", w.Body.String())
	}
}

func TestAssistantFolderSelection_ValidReplacementRetiresSavedBindingOnly(t *testing.T) {
	f := newConversationServerFixture(t)
	observations := newFolderObservationStub()
	f.handler.FolderObservations = observations
	ctx := context.Background()
	sess := &session.Session{AgentName: "Atlas", FolderID: f.hq}
	if err := f.sessions.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	store := f.sessions.(session.FolderContextStore)
	rows, err := store.AppendFolderTurn(ctx, sess.ID, f.hq, "Atlas", "", foldercontext.Event{Version: 1, Observation: &observations.observation, OfferID: "review-a"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	observations.observation.ID = "replacement"
	w := folderHTTP(f.handler.SelectFolderContextHandler, `{"conversation_id":"`+sess.ID+`","revision":"`+rows[0].ID+`","mode":"chip","chip":"documents"}`)
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	var result struct {
		Revision    string                    `json:"revision"`
		Observation foldercontext.Observation `json:"observation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Revision == rows[0].ID || result.Observation.ID != "replacement" {
		t.Fatalf("replacement: %+v", result)
	}
	messages, err := store.GetFolderMessages(ctx, sess.ID)
	if err != nil || len(messages) != 2 || messages[1].FolderContext.Observation != nil || messages[1].FolderContext.OfferID != "" {
		t.Fatalf("old binding not retired: %+v %v", messages, err)
	}
	if len(f.provider.requests) != 0 {
		t.Fatal("replacement sent metadata")
	}
}

func TestAssistantFolderDetach_CanonicalRevisionAndHistory(t *testing.T) {
	f := newConversationServerFixture(t)
	observations := newFolderObservationStub()
	f.handler.FolderObservations = observations
	ctx := context.Background()
	sess := &session.Session{AgentName: "Atlas", FolderID: f.hq}
	if err := f.sessions.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	store := f.sessions.(session.FolderContextStore)
	rows, err := store.AppendFolderTurn(ctx, sess.ID, f.hq, "Atlas", "", foldercontext.Event{Version: 1, Observation: &observations.observation}, "Question", "Answer")
	if err != nil {
		t.Fatal(err)
	}
	body := `{"conversation_id":"` + sess.ID + `","revision":"` + rows[0].ID + `"}`
	w := folderHTTP(f.handler.DetachFolderContextHandler, body)
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	var state agenthttp.PersonalAssistantFolderState
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil || state.Revision == rows[0].ID || state.Observation != nil {
		t.Fatalf("detach: %+v %v", state, err)
	}
	w = folderHTTP(f.handler.DetachFolderContextHandler, body)
	if w.Code != http.StatusConflict {
		t.Fatal("stale detach succeeded")
	}
	messages, err := store.GetFolderMessages(ctx, sess.ID)
	if err != nil || len(messages) != 4 || messages[1].Content != "Question" {
		t.Fatal("detach deleted history")
	}
	if _, err := store.AppendFolderTurn(ctx, sess.ID, f.hq, "Atlas", rows[0].ID, foldercontext.Event{Version: 1}, "", ""); !errors.Is(err, session.ErrFolderContextConflict) {
		t.Fatal("stale write accepted")
	}
}
