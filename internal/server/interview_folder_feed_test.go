package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/progression"
)

// The interview's folder feed through the real routes and wiring: a chip scan
// proposes wording for question 1, the snapshot proposes it again later without
// a scan, and none of it saves a fact, creates a workspace or completes the
// "Show your assistant a folder" mission.
func TestInterviewFolderFeed_SuggestsWithoutSavingOrSettingUp(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)
	ctx := context.Background()
	call := func(method, target, body string) *httptest.ResponseRecorder {
		t.Helper()
		var reader *bytes.Reader
		if body != "" {
			reader = bytes.NewReader([]byte(body))
		}
		var req *http.Request
		if reader != nil {
			req = httptest.NewRequest(method, target, reader)
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(method, target, nil)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}

	if w := call(http.MethodPost, "/api/personal-assistant/hire", `{"request_id":"feed-hire","if_version":0,"display_name":"Atlas","mandate":"Help me plan.","focus_areas":["plan_my_day"]}`); w.Code != http.StatusCreated {
		t.Fatalf("hire: %d %s", w.Code, w.Body.String())
	}
	state, err := builder.personalAssistantStore.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	hq, err := json.Marshal(map[string]any{"request_id": "feed-hq", "if_version": state.StateVersion, "name": "My HQ", "timezone": "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if w := call(http.MethodPost, "/api/personal-assistant/hq", string(hq)); w.Code != http.StatusCreated {
		t.Fatalf("hq: %d %s", w.Code, w.Body.String())
	}

	// One project under the sandbox home's Documents.
	home := os.Getenv("HOME")
	for _, rel := range []string{"Documents/Thesis/main.tex", "Documents/Thesis/chapters/one.tex", "Documents/Thesis/chapters/two.tex"} {
		full := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	const interview = "/api/personal-assistant/knowledge/interview"
	type snapshot struct {
		Suggestion        *personalassistant.InterviewSuggestion `json:"suggestion"`
		RememberedProject string                                 `json:"remembered_project"`
		Questions         []personalassistant.InterviewQuestion  `json:"questions"`
	}
	read := func() snapshot {
		t.Helper()
		w := call(http.MethodGet, interview, "")
		var body snapshot
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Questions) != 3 {
			t.Fatalf("interview snapshot: %d %s", w.Code, w.Body.String())
		}
		return body
	}
	if before := read(); before.Suggestion != nil || before.RememberedProject != "" {
		t.Fatalf("nothing was shown yet: %+v", before)
	}

	// The browser may name a chip and nothing else.
	if w := call(http.MethodPost, interview+"/suggest", `{"path":"`+filepath.Join(home, "Documents")+`"}`); w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), home) {
		t.Fatalf("a path must be refused without being echoed: %d %s", w.Code, w.Body.String())
	}
	if w := call(http.MethodGet, interview+"/suggest", ""); w.Code == http.StatusOK {
		t.Fatalf("GET suggest => %d", w.Code)
	}

	countWorkspaces := func() int {
		t.Helper()
		ids, err := builder.workspaceStore.List()
		if err != nil {
			t.Fatal(err)
		}
		return len(ids)
	}
	workspacesBefore := countWorkspaces()
	w := call(http.MethodPost, interview+"/suggest", `{"chip":"documents"}`)
	var suggested struct {
		Suggestion *personalassistant.InterviewSuggestion `json:"suggestion"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &suggested) != nil ||
		suggested.Suggestion == nil || suggested.Suggestion.Text != "Thesis, a LaTeX manuscript" {
		t.Fatalf("suggest: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), home) {
		t.Fatalf("the suggestion names a path: %s", w.Body.String())
	}

	// The same wording is available later with no scan: the reader is wired.
	if later := read(); later.Suggestion == nil || later.Suggestion.Text != "Thesis, a LaTeX manuscript" || later.RememberedProject != "" {
		t.Fatalf("snapshot after the scan: %+v", later)
	}

	// Nothing was saved, set up or completed by showing the folder.
	knowledge := personalassistant.NewKnowledgeStore(
		personalassistant.NewKnowledgeResolver(builder.personalAssistantStore, builder.personalHQService,
			personalassistant.NewAgentStoreProfileReader(builder.st)), builder.workspaceFileStore,
	)
	doc, err := knowledge.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range doc.Items {
		if item.Category == "projects" {
			t.Fatalf("a project fact was saved before Save: %+v", item)
		}
	}
	if after := countWorkspaces(); after != workspacesBefore {
		t.Fatalf("the interview scan created a workspace: %d -> %d", workspacesBefore, after)
	}
	if builder.progressionEngine != nil && builder.progressionEngine.HasCompleted(progression.ShowFolderQuestID) {
		t.Fatal("a scan alone completed the show-a-folder mission")
	}

	// Home sees the offer the interview's scan recorded, still unanswered.
	digest := call(http.MethodGet, "/api/personal-assistant/folder-digest", "")
	var home1 struct {
		FolderDigest personalassistant.FolderDigestView `json:"folder_digest"`
	}
	if digest.Code != http.StatusOK || json.Unmarshal(digest.Body.Bytes(), &home1) != nil || home1.FolderDigest.Offer == nil ||
		home1.FolderDigest.Offer.Status != personalassistant.FolderOfferPending || home1.FolderDigest.Offer.Subject.Name != "Thesis" {
		t.Fatalf("Home's offer after the interview scan: %d %s", digest.Code, digest.Body.String())
	}
}
