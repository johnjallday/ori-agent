package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type realKnowledgeJanitorMover struct{}

func (realKnowledgeJanitorMover) Move(_ context.Context, _, source, destination string) error {
	return os.Rename(source, destination)
}

func TestRealJanitorApprovedMovesUndoAndHQLearningFromJournal(t *testing.T) {
	ctx := context.Background()
	builder, handler := newDailyBriefTestServer(t)
	post := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(response, request)
		return response
	}
	hire := post("/api/personal-assistant/hire", `{"request_id":"real-janitor-hire","if_version":0,"display_name":"Atlas","mandate":"Help me plan.","focus_areas":["plan_my_day"]}`)
	if hire.Code != http.StatusCreated {
		t.Fatalf("hire: %d %s", hire.Code, hire.Body.String())
	}
	before, err := builder.personalAssistantStore.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	hq := post("/api/personal-assistant/hq", `{"request_id":"real-janitor-hq","if_version":`+strconv.FormatInt(before.StateVersion, 10)+`,"name":"My HQ","timezone":"UTC"}`)
	if hq.Code != http.StatusCreated {
		t.Fatalf("hq: %d %s", hq.Code, hq.Body.String())
	}
	state, err := builder.personalAssistantStore.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	janitor := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Disposable Janitor"})
	janitor.ID = "janitor-real-fixture"
	janitor.FolderSlug = "disposable-janitor"
	janitor.OwnerUserID = "local"
	janitor.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: filejanitor.LegacyTemplateID, Builtin: true})
	if err := builder.workspaceStore.Save(janitor); err != nil {
		t.Fatal(err)
	}
	if _, err := builder.dailyBriefService.UpdateConfig(ctx, dailybrief.Config{WorkspaceID: state.HQWorkspaceID,
		UserID: "local", Scope: dailybrief.ScopeSelected, SelectedWorkspaceIDs: []string{janitor.ID}, Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "inbox")
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatal(err)
	}
	service := builder.fileJanitorService
	if service == nil {
		t.Fatal("Janitor service not wired")
	}
	service.SetMover(realKnowledgeJanitorMover{})
	if _, err := service.ConfirmSetup(filejanitor.SetupRequest{WorkspaceID: janitor.ID, Path: root}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.pdf", "two.pdf", "three.pdf", "four.pdf"} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-10 * time.Minute)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	batch, created, err := service.ScanNow(janitor.ID, filejanitor.ScanSourceManual)
	if err != nil || !created {
		t.Fatalf("real scan created=%t err=%v", created, err)
	}
	_, candidates, err := service.BatchDetail(janitor.ID, batch.ID)
	if err != nil || len(candidates) != 4 {
		t.Fatalf("real scan candidates=%d err=%v", len(candidates), err)
	}
	items := make([]filejanitor.PreviewRequestItem, 0, 3)
	for _, candidate := range candidates[:3] {
		items = append(items, filejanitor.PreviewRequestItem{CandidateID: candidate.ID,
			Operation: filejanitor.OperationMove, Category: string(filejanitor.CategoryDocuments)})
	}
	preview, err := service.PreviewMoves(filejanitor.PreviewRequest{WorkspaceID: janitor.ID, UserID: "local", Items: items})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := service.ConfirmMoves(ctx, filejanitor.ConfirmRequest{WorkspaceID: janitor.ID,
		UserID: "local", BatchID: preview.BatchID, Token: preview.Token, Items: items})
	if err != nil || applied.Applied != 3 {
		t.Fatalf("real approved moves: %+v %v", applied, err)
	}
	for _, outcome := range applied.Outcomes {
		if !outcome.Undoable {
			t.Fatalf("move not durable/undoable: %+v", outcome)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/personal-assistant/knowledge/check-janitor", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("real journal check: %d %s", response.Code, response.Body.String())
	}
	read := func() []personalassistant.KnowledgeReviewItem {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/knowledge", nil))
		var body struct {
			Items []personalassistant.KnowledgeReviewItem `json:"items"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
			t.Fatalf("review: %d %s", response.Code, response.Body.String())
		}
		return body.Items
	}
	var candidate personalassistant.KnowledgeReviewItem
	for _, item := range read() {
		if item.SourceKind == "file_janitor" {
			candidate = item
		}
	}
	if candidate.ID == "" || candidate.State != personalassistant.KnowledgeCandidate || len(candidate.Evidence) != 3 {
		t.Fatalf("real moves did not create one bounded candidate: %+v", candidate)
	}
	sources := httptest.NewRecorder()
	handler.ServeHTTP(sources, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/knowledge", nil))
	if sources.Code != http.StatusOK || !strings.Contains(sources.Body.String(), `"file_janitor":{"status":"available"}`) ||
		!strings.Contains(sources.Body.String(), `"saved_apps":{"status":"not_configured"}`) ||
		strings.Contains(sources.Body.String(), "one.pdf") || strings.Contains(sources.Body.String(), root) {
		t.Fatalf("source cards missed real ready Janitor: %d %s", sources.Code, sources.Body.String())
	}
	approved := post("/api/personal-assistant/knowledge/"+candidate.ID+"/approve", `{"version":`+strconv.FormatInt(candidate.Version, 10)+`,"request_id":"real-approve"}`)
	if approved.Code != http.StatusOK {
		t.Fatalf("real journal approval: %d %s", approved.Code, approved.Body.String())
	}
	// A failed real undo has UndoneAt set in the action journal, but still
	// leaves its verified applied move in place. Only UndoDone is a reversal.
	occupiedName := filepath.Join(root, applied.Outcomes[1].Name)
	if err := os.WriteFile(occupiedName, []byte("someone else's file"), 0o600); err != nil {
		t.Fatal(err)
	}
	failed, err := service.Undo(ctx, janitor.ID, applied.Outcomes[1].ActionID, "local")
	if err != nil || failed.Result != "failed" {
		t.Fatalf("real blocked undo: %+v %v", failed, err)
	}
	for _, item := range read() {
		if item.ID == candidate.ID && (item.State != personalassistant.KnowledgeApproved || item.ReviewUnavailable != "") {
			t.Fatalf("failed UndoFailed revoked still-valid applied support: %+v", item)
		}
	}
	if err := os.Remove(occupiedName); err != nil {
		t.Fatal(err)
	}
	undo, err := service.Undo(ctx, janitor.ID, applied.Outcomes[0].ActionID, "local")
	if err != nil || undo.Result != "undone" {
		t.Fatalf("real undo: %+v %v", undo, err)
	}
	sources = httptest.NewRecorder()
	handler.ServeHTTP(sources, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/knowledge", nil))
	if sources.Code != http.StatusOK || !strings.Contains(sources.Body.String(), `"file_janitor":{"status":"healthy_empty"}`) {
		t.Fatalf("source cards claimed support after undo: %d %s", sources.Code, sources.Body.String())
	}
	for _, item := range read() {
		if item.ID == candidate.ID {
			if item.State != personalassistant.KnowledgeNeedsReview || item.ReviewUnavailable == "" {
				t.Fatalf("successful undo did not remove active fact: %+v", item)
			}
		}
	}
	prompt := personalassistant.NewKnowledgeContextReader(personalassistant.NewKnowledgeStore(
		personalassistant.NewKnowledgeResolver(builder.personalAssistantStore, builder.personalHQService,
			personalassistant.NewAgentStoreProfileReader(builder.st)), builder.workspaceFileStore),
		workspace.NewMemoryStore(builder.workspaceFileStore), scopedKnowledgeAuthority{
			apps: personalassistant.NewSavedAppAuthority(builder.onboardingMgr), janitor: &janitorKnowledgeReader{
				bindings: personalassistant.NewKnowledgeResolver(builder.personalAssistantStore, builder.personalHQService,
					personalassistant.NewAgentStoreProfileReader(builder.st)),
				briefs: builder.dailyBriefService, workspaces: builder.workspaceFileStore, janitor: service, now: time.Now},
		})
	section, err := prompt.HomeSection(ctx, "local", state.HQWorkspaceID)
	if err != nil || strings.Contains(section, "filing documents") {
		t.Fatalf("real undo leaked stale reviewed context: %q %v", section, err)
	}
	memoryStore := workspace.NewMemoryStore(builder.workspaceFileStore)
	rawPrompt, err := memoryStore.ReadPromptRaw(state.HQWorkspaceID)
	if err != nil || strings.Contains(rawPrompt, "filing documents") {
		t.Fatalf("real undo leaked into raw workspace.memory: %q %v", rawPrompt, err)
	}
	brief := httptest.NewRecorder()
	handler.ServeHTTP(brief, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/today", nil))
	if brief.Code != http.StatusOK || strings.Contains(brief.Body.String(), "filing documents") {
		t.Fatalf("real undo leaked into Today: %d %s", brief.Code, brief.Body.String())
	}
	// A genuinely new fourth approved move restores a three-action checkpoint.
	// A restart reads the same persisted journal and sidecar; it must not allow
	// the old undo to suspend an explicitly reconfirmed revision again.
	fourth := []filejanitor.PreviewRequestItem{{CandidateID: candidates[3].ID,
		Operation: filejanitor.OperationMove, Category: string(filejanitor.CategoryDocuments)}}
	followup, err := service.PreviewMoves(filejanitor.PreviewRequest{WorkspaceID: janitor.ID, UserID: "local", Items: fourth})
	if err != nil {
		t.Fatal(err)
	}
	newMove, err := service.ConfirmMoves(ctx, filejanitor.ConfirmRequest{WorkspaceID: janitor.ID,
		UserID: "local", BatchID: followup.BatchID, Token: followup.Token, Items: fourth})
	if err != nil || newMove.Applied != 1 {
		t.Fatalf("fresh supporting move: %+v %v", newMove, err)
	}
	var needsReview personalassistant.KnowledgeReviewItem
	for _, item := range read() {
		if item.ID == candidate.ID {
			needsReview = item
		}
	}
	if needsReview.State != personalassistant.KnowledgeNeedsReview || needsReview.EvidenceCheckpoint == "" || len(needsReview.FreshEvidence) != 3 {
		t.Fatalf("new persisted support not offered for exact reconfirmation: %+v", needsReview)
	}
	body, err := json.Marshal(map[string]any{"version": needsReview.Version, "request_id": "real-reconfirm",
		"text": needsReview.Text, "revision_id": needsReview.RevisionID,
		"evidence_checkpoint": needsReview.EvidenceCheckpoint})
	if err != nil {
		t.Fatal(err)
	}
	reconfirmed := post("/api/personal-assistant/knowledge/"+candidate.ID+"/reconfirm", string(body))
	if reconfirmed.Code != http.StatusOK {
		t.Fatalf("real fresh-evidence reconfirm: %d %s", reconfirmed.Code, reconfirmed.Body.String())
	}
	section, err = prompt.HomeSection(ctx, "local", state.HQWorkspaceID)
	if err != nil || !strings.Contains(section, "filing documents") {
		t.Fatalf("old undo wrongly suspended fresh revision: %q %v", section, err)
	}
	brief = httptest.NewRecorder()
	handler.ServeHTTP(brief, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/today", nil))
	if brief.Code != http.StatusOK || !strings.Contains(brief.Body.String(), "filing documents") {
		t.Fatalf("freshly reconfirmed fact did not return to Today: %d %s", brief.Code, brief.Body.String())
	}
	var again personalassistant.KnowledgeReviewItem
	for _, item := range read() {
		if item.ID == candidate.ID {
			again = item
		}
	}
	if again.State != personalassistant.KnowledgeApproved {
		t.Fatalf("review did not retain new approval: %+v", again)
	}
	restarted := personalassistant.NewKnowledgeContextReader(personalassistant.NewKnowledgeStore(
		personalassistant.NewKnowledgeResolver(builder.personalAssistantStore, builder.personalHQService,
			personalassistant.NewAgentStoreProfileReader(builder.st)), builder.workspaceFileStore),
		workspace.NewMemoryStore(builder.workspaceFileStore), scopedKnowledgeAuthority{
			apps: personalassistant.NewSavedAppAuthority(builder.onboardingMgr), janitor: &janitorKnowledgeReader{
				bindings: personalassistant.NewKnowledgeResolver(builder.personalAssistantStore, builder.personalHQService,
					personalassistant.NewAgentStoreProfileReader(builder.st)),
				briefs: builder.dailyBriefService, workspaces: builder.workspaceFileStore, janitor: service, now: time.Now},
		})
	if fresh, err := restarted.HomeSection(ctx, "local", state.HQWorkspaceID); err != nil || !strings.Contains(fresh, "filing documents") {
		t.Fatalf("new knowledge store did not rehydrate reconfirmed revision: %q %v", fresh, err)
	}
	forgotten := post("/api/personal-assistant/knowledge/"+candidate.ID+"/forget", `{"version":`+strconv.FormatInt(again.Version, 10)+`,"request_id":"real-janitor-forget"}`)
	if forgotten.Code != http.StatusOK {
		t.Fatalf("real Janitor forget: %d %s", forgotten.Code, forgotten.Body.String())
	}
	if check := post("/api/personal-assistant/knowledge/check-janitor", ""); check.Code != http.StatusOK {
		t.Fatalf("repeat Janitor check after Forget: %d %s", check.Code, check.Body.String())
	}
	brief = httptest.NewRecorder()
	handler.ServeHTTP(brief, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/today", nil))
	if brief.Code != http.StatusOK || strings.Contains(brief.Body.String(), "filing documents") {
		t.Fatalf("forgotten Janitor fact reappeared in Today: %d %s", brief.Code, brief.Body.String())
	}
	for _, item := range read() {
		if item.ID == candidate.ID || item.SourceKind == "file_janitor" {
			t.Fatalf("automatic Janitor suggestion reappeared after Forget: %+v", item)
		}
	}
}
