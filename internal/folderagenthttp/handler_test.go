package folderagenthttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

func handler() *Handler {
	return New(folderagent.NewService(folderagent.Deps{}), func(context.Context) (string, error) { return "u1", nil })
}

func TestChoose_RefusesAPathAndAnEmptyChoice(t *testing.T) {
	for _, body := range []string{`{"path":"/Users/me/Music"}`, `{}`, `{"picker":true,"extra":1}`} {
		rec := httptest.NewRecorder()
		handler().Choose(rec, httptest.NewRequest(http.MethodPost, "/choose", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s → %d %s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestStart_RefusesAPathField(t *testing.T) {
	rec := httptest.NewRecorder()
	handler().Start(rec, httptest.NewRequest(http.MethodPost, "/start", strings.NewReader(`{"choice_token":"x","path":"/tmp"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestStart_HireRefusesAPathFieldToo(t *testing.T) {
	rec := httptest.NewRecorder()
	handler().Start(rec, httptest.NewRequest(http.MethodPost, "/start",
		strings.NewReader(`{"choice_token":"x","hire":{"name":"A","path":"/tmp"}}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestGet_AWorkspaceWithoutAFolderRecordHasNoReport(t *testing.T) {
	dir := t.TempDir()
	service := folderagent.NewService(folderagent.Deps{Store: folderagent.NewStore(func(string) (string, error) { return dir, nil })})
	h := New(service, func(context.Context) (string, error) { return "u1", nil })
	req := httptest.NewRequest(http.MethodGet, "/folder-agent/ws-1", nil)
	req.SetPathValue("workspaceID", "ws-1")
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"folder_agent":null`) {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func emptyStoreHandler(t *testing.T) *Handler {
	dir := t.TempDir()
	service := folderagent.NewService(folderagent.Deps{Store: folderagent.NewStore(func(string) (string, error) { return dir, nil })})
	return New(service, func(context.Context) (string, error) { return "u1", nil })
}

// The goal offer takes "use" (with an optional edited sentence) or "dismiss";
// anything else, or an unknown field, is refused before the service is asked.
// The service answers once (a second answer is ErrWrongState, a 409 above).
func TestGoal_TakesUseOrDismissOnly(t *testing.T) {
	cases := map[string]int{
		`{"answer":"maybe"}`:                     http.StatusBadRequest,
		`{}`:                                     http.StatusBadRequest,
		`{"answer":"use","path":"/Users/me"}`:    http.StatusBadRequest,
		`{"answer":"use"}`:                       http.StatusNotFound,
		`{"answer":"use","goal":"Finish Album"}`: http.StatusNotFound,
		`{"answer":"dismiss"}`:                   http.StatusNotFound,
	}
	for body, want := range cases {
		req := httptest.NewRequest(http.MethodPost, "/folder-agent/ws-1/goal", strings.NewReader(body))
		req.SetPathValue("workspaceID", "ws-1")
		rec := httptest.NewRecorder()
		emptyStoreHandler(t).Goal(rec, req)
		if rec.Code != want {
			t.Errorf("%s → %d %s, want %d", body, rec.Code, rec.Body.String(), want)
		}
	}
}

// The offer to learn the songs takes "learn" or "dismiss" only; anything else,
// or an unknown field, is refused before the service is asked, so nothing is
// granted or read on a malformed request.
func TestSongs_TakesLearnOrDismissOnly(t *testing.T) {
	cases := map[string]int{
		`{"answer":"maybe"}`:                 http.StatusBadRequest,
		`{"answer":"use"}`:                   http.StatusBadRequest,
		`{}`:                                 http.StatusBadRequest,
		`{"answer":"learn","path":"/Users"}`: http.StatusBadRequest,
		`{"answer":"learn","scope":"names"}`: http.StatusBadRequest,
		`{"answer":"learn"}`:                 http.StatusNotFound,
		`{"answer":"dismiss"}`:               http.StatusNotFound,
		// Try again on a stopped reading reaches the service too.
		`{"answer":"retry"}`:                 http.StatusNotFound,
		`{"answer":"retry","scope":"names"}`: http.StatusBadRequest,
	}
	for body, want := range cases {
		req := httptest.NewRequest(http.MethodPost, "/folder-agent/ws-1/songs", strings.NewReader(body))
		req.SetPathValue("workspaceID", "ws-1")
		rec := httptest.NewRecorder()
		emptyStoreHandler(t).Songs(rec, req)
		if rec.Code != want {
			t.Errorf("%s → %d %s, want %d", body, rec.Code, rec.Body.String(), want)
		}
	}
}

// An older card still sends the goal with Set it up: the body is accepted and
// the goal ignored (folder-setup-ends-in-home FR15), so the request reaches
// the service rather than failing as a bad request.
func TestSetUp_AcceptsAndIgnoresAnOlderCardsGoal(t *testing.T) {
	body := `{"plan_digest":"d","selected":[],"use_goal":true,"goal":"Finish the album"}`
	req := httptest.NewRequest(http.MethodPost, "/folder-agent/ws-1/setup", strings.NewReader(body))
	req.SetPathValue("workspaceID", "ws-1")
	rec := httptest.NewRecorder()
	emptyStoreHandler(t).SetUp(rec, req)
	if rec.Code == http.StatusBadRequest {
		t.Fatalf("an older card's body was refused: %d %s", rec.Code, rec.Body.String())
	}
	if got := setUpOf(setUpRequest{PlanDigest: "d", UseGoal: true, Goal: "Finish"}); got.PlanDigest != "d" {
		t.Fatalf("set up request = %+v", got)
	}
}

func TestWriteError_PlainSentences(t *testing.T) {
	cases := []struct {
		err    error
		status int
		text   string
	}{
		{folderagent.ErrChoiceLost, http.StatusGone, "That folder choice expired. Choose the folder again."},
		{folderagent.ErrStageNotYet, http.StatusConflict, "Setting up this stage is not available yet"},
		{folderagent.ErrNothingToSet, http.StatusConflict, "There is nothing to set up yet"},
		{folderagent.ErrWrongState, http.StatusConflict, "The report has moved on. Reload to see where it stands"},
		{folderagent.ErrNoRecord, http.StatusNotFound, "That folder workspace is no longer here"},
		{folderagent.ErrNotOwner, http.StatusNotFound, "That folder workspace is no longer here"},
		{fmt.Errorf("%w: brief", personalassistant.ErrValidation), http.StatusBadRequest, "Keep it to one line, without paths, under 300 characters"},
		{&personalassistant.FolderRootError{Message: "Choose a folder outside the Workspace Directory."}, http.StatusBadRequest, "Choose a folder outside the Workspace Directory."},
		{&folderagent.Refusal{Code: "anchor_already_owned", Message: `This folder already belongs to the workspace "Songs".`, Status: 409,
			Duplicate: &folderagent.WorkspaceRef{ID: "ws-1", Name: "Songs"}}, http.StatusConflict, `This folder already belongs to the workspace "Songs".`},
		{&folderagent.Refusal{Code: "agent_name_taken", Status: 409,
			Message: "You already have an agent named Atlas. Choose it under One of your agents, or give this one another name."},
			http.StatusConflict, "You already have an agent named Atlas. Choose it under One of your agents, or give this one another name."},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		writeError(rec, tc.err)
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		got, _ := body["error"].(string)
		if rec.Code != tc.status || got != tc.text {
			t.Errorf("%v → %d %q, want %d %q", tc.err, rec.Code, got, tc.status, tc.text)
		}
	}
}
