package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestSavedAppObservationBeforeHQIsReconciledAfterActivation(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/hire", bytes.NewBufferString(`{"request_id":"early-app-hire","if_version":0,"display_name":"Atlas","mandate":"Help me plan.","focus_areas":["plan_my_day"]}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("hire: %d %s", response.Code, response.Body.String())
	}
	if err := builder.onboardingMgr.SetUserProfile(&types.InferredProfile{
		DetectedApps: []string{"Obsidian"}, InferredAt: time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	before, err := builder.personalAssistantStore.GetState(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"request_id": "early-app-hq", "if_version": before.StateVersion, "name": "My HQ", "timezone": "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	hqReq := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/hq", bytes.NewReader(body))
	hqReq.Header.Set("Content-Type", "application/json")
	hqResponse := httptest.NewRecorder()
	handler.ServeHTTP(hqResponse, hqReq)
	if hqResponse.Code != http.StatusCreated {
		t.Fatalf("hq: %d %s", hqResponse.Code, hqResponse.Body.String())
	}
	knowledge := personalassistant.NewKnowledgeStore(
		personalassistant.NewKnowledgeResolver(builder.personalAssistantStore, builder.personalHQService,
			personalassistant.NewAgentStoreProfileReader(builder.st)), builder.workspaceFileStore,
	)
	janitorEmpty := httptest.NewRecorder()
	handler.ServeHTTP(janitorEmpty, httptest.NewRequest(http.MethodPost,
		"/api/personal-assistant/knowledge/check-janitor", nil))
	if janitorEmpty.Code != http.StatusOK || !strings.Contains(janitorEmpty.Body.String(), `"candidates":[]`) {
		t.Fatalf("healthy empty Janitor source is not read-only: %d %s", janitorEmpty.Code, janitorEmpty.Body.String())
	}
	doc, err := knowledge.Read(context.Background(), "local")
	if err != nil || len(doc.Items) != 1 || doc.Items[0].State != personalassistant.KnowledgeCandidate {
		t.Fatalf("HQ activation did not reconcile saved source: %+v %v", doc, err)
	}
	again := httptest.NewRecorder()
	handler.ServeHTTP(again, httptest.NewRequest(http.MethodPost, "/api/personal-assistant/knowledge/check-saved-apps", nil))
	if again.Code != http.StatusOK {
		t.Fatalf("explicit source reconciliation failed: %d %s", again.Code, again.Body.String())
	}
	doc, err = knowledge.Read(context.Background(), "local")
	if err != nil || len(doc.Items) != 1 || len(doc.Admissions) != 1 {
		t.Fatalf("reconciliation duplicated candidate or quota: %+v %v", doc, err)
	}
	getReview := httptest.NewRecorder()
	handler.ServeHTTP(getReview, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/knowledge", nil))
	if getReview.Code != http.StatusOK || !strings.Contains(getReview.Body.String(), `"state_version":`) ||
		!strings.Contains(getReview.Body.String(), `"saved_apps":{"status":"available","observed_at":`) ||
		!strings.Contains(getReview.Body.String(), `"file_janitor":{"status":"not_configured"}`) {
		t.Fatalf("review did not return truthful source availability: %d %s", getReview.Code, getReview.Body.String())
	}
	beforeRead := doc.Version
	for attempt := 0; attempt < 2; attempt++ {
		review := httptest.NewRecorder()
		handler.ServeHTTP(review, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/knowledge", nil))
		if review.Code != http.StatusOK {
			t.Fatalf("review read %d failed: %d %s", attempt, review.Code, review.Body.String())
		}
	}
	doc, err = knowledge.Read(context.Background(), "local")
	if err != nil || doc.Version != beforeRead || len(doc.Items) != 1 || len(doc.Admissions) != 1 {
		t.Fatalf("GET review produced an automatic app proposal or sidecar mutation: %+v %v", doc, err)
	}
	currentState, err := builder.personalAssistantStore.GetState(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	body, err = json.Marshal(map[string]any{"state_version": currentState.StateVersion,
		"request_id": "user-explicit-1", "category": "projects", "text": "Finish the portfolio"})
	if err != nil {
		t.Fatal(err)
	}
	for _, attempt := range []int{1, 2} {
		saved := httptest.NewRecorder()
		handler.ServeHTTP(saved, httptest.NewRequest(http.MethodPost, "/api/personal-assistant/knowledge/explicit", bytes.NewReader(body)))
		if saved.Code != http.StatusOK || !strings.Contains(saved.Body.String(), "Finish the portfolio") {
			t.Fatalf("explicit fact attempt %d: %d %s", attempt, saved.Code, saved.Body.String())
		}
	}
	doc, err = knowledge.Read(context.Background(), "local")
	if err != nil || len(doc.Items) != 2 || len(doc.Admissions) != 1 || doc.Items[1].State != personalassistant.KnowledgeApproved {
		t.Fatalf("explicit save duplicated proposal or memory: %+v %v", doc, err)
	}
	readToday := func() personalassistant.TodayProjection {
		t.Helper()
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/today", nil))
		var response struct {
			Today personalassistant.TodayProjection `json:"today"`
		}
		if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &response) != nil {
			t.Fatalf("Today after explicit save: %d %s", recorder.Code, recorder.Body.String())
		}
		return response.Today
	}
	today := readToday()
	if today.Remembered.Health.Status != personalassistant.TodaySectionAvailable || len(today.Remembered.Items) != 2 ||
		today.Remembered.Items[0].Title != "Finish the portfolio" ||
		today.Remembered.Items[1].Kind != "existing_next_action" || today.Remembered.Items[1].Route == "" {
		t.Fatalf("first Today recap did not use exact approved fact: %+v", today.Remembered)
	}
	forgetBody, err := json.Marshal(map[string]any{"version": doc.Items[1].Version, "request_id": "user-explicit-forget"})
	if err != nil {
		t.Fatal(err)
	}
	forgotten := httptest.NewRecorder()
	handler.ServeHTTP(forgotten, httptest.NewRequest(http.MethodPost,
		"/api/personal-assistant/knowledge/"+doc.Items[1].ID+"/forget", bytes.NewReader(forgetBody)))
	if forgotten.Code != http.StatusOK || len(readToday().Remembered.Items) != 0 {
		t.Fatalf("Today retained forgotten fact: %d %s", forgotten.Code, forgotten.Body.String())
	}
	// The optional interview is a memory/profile save, never an implicit task,
	// follow-up or Daily Brief generation—even when Today links existing work.
	workSnapshot := func() struct {
		Tickets   []workspace.Ticket
		FollowUps int
		BriefID   string
	} {
		t.Helper()
		tickets, err := workspace.NewTicketService(builder.workspaceStore).List(workspace.TicketQuery{WorkspaceID: currentState.HQWorkspaceID})
		if err != nil {
			t.Fatal(err)
		}
		followups, err := builder.followUpService.List(context.Background(), followup.Filter{UserID: "local"})
		if err != nil {
			t.Fatal(err)
		}
		brief, err := builder.dailyBriefService.GetCurrent(context.Background(), currentState.HQWorkspaceID)
		if err != nil && !errors.Is(err, dailybrief.ErrRevisionNotFound) {
			t.Fatal(err)
		}
		id := ""
		if brief != nil {
			id = brief.ID
		}
		return struct {
			Tickets   []workspace.Ticket
			FollowUps int
			BriefID   string
		}{tickets, len(followups), id}
	}
	beforeInterviewWork := workSnapshot()
	interviewGET := httptest.NewRecorder()
	handler.ServeHTTP(interviewGET, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/knowledge/interview", nil))
	var interview struct {
		StateVersion int64 `json:"state_version"`
		Profile      struct {
			UpdatedAt   time.Time         `json:"updated_at"`
			Preferences map[string]string `json:"preferences"`
		} `json:"profile"`
		Questions []personalassistant.InterviewQuestion `json:"questions"`
	}
	if interviewGET.Code != http.StatusOK || json.Unmarshal(interviewGET.Body.Bytes(), &interview) != nil ||
		len(interview.Questions) != 3 || interview.StateVersion != currentState.StateVersion || interview.Profile.UpdatedAt.IsZero() {
		t.Fatalf("interview not available after HQ: %d %s", interviewGET.Code, interviewGET.Body.String())
	}
	if readToday().InterviewStatus != "offered" {
		t.Fatal("Today did not offer a voluntary interview after HQ activation")
	}
	interviewBody, err := json.Marshal(personalassistant.KnowledgeInterviewSaveRequest{
		StateVersion: interview.StateVersion, RequestID: "interview-no-model", Rows: []personalassistant.KnowledgeInterviewReviewedRow{
			{RowID: "priority", Category: "projects", Destination: "personal_hq", Text: "Review the final draft"},
			{RowID: "communication", Category: "how_you_work", Destination: "profile", Text: "concise",
				Preference: "response_style", ExpectedProfileValue: interview.Profile.Preferences["response_style"],
				ExpectedProfileUpdatedAt: interview.Profile.UpdatedAt},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		answer := httptest.NewRecorder()
		handler.ServeHTTP(answer, httptest.NewRequest(http.MethodPost,
			"/api/personal-assistant/knowledge/interview/save", bytes.NewReader(interviewBody)))
		if answer.Code != http.StatusOK || !strings.Contains(answer.Body.String(), `"completed"`) {
			t.Fatalf("interview save/replay %d: %d %s", attempt, answer.Code, answer.Body.String())
		}
	}
	if readToday().InterviewStatus != "completed" {
		t.Fatal("Today repeated an already completed interview")
	}
	if remembered := readToday().Remembered.Items; len(remembered) < 2 ||
		remembered[0].Title != "Review the final draft" || remembered[1].Title != "concise" || remembered[1].Kind != "reviewed_preference" {
		t.Fatalf("Today did not use confirmed interview priority/preference: %+v", remembered)
	}
	if after := workSnapshot(); !reflect.DeepEqual(beforeInterviewWork, after) {
		t.Fatalf("interview or Today created work/brief without consent: before=%+v after=%+v", beforeInterviewWork, after)
	}
	user, err := builder.userStore.Get(context.Background(), "local")
	if err != nil || user.Preferences["response_style"] != "concise" {
		t.Fatalf("interview did not save canonical profile field: %+v %v", user, err)
	}
	if cas, ok := builder.userStore.(personalassistant.ProfileCASStore); ok {
		if _, err := cas.UpdateFieldCAS(context.Background(), "local", "preferences.response_style", user.UpdatedAt, "concise", "detailed"); err != nil {
			t.Fatal(err)
		}
		for _, item := range readToday().Remembered.Items {
			if item.Kind == "reviewed_preference" || item.Title == "concise" {
				t.Fatalf("externally changed profile preference misrepresented as confirmed: %+v", item)
			}
		}
	}
	forged := httptest.NewRecorder()
	handler.ServeHTTP(forged, httptest.NewRequest(http.MethodPost, "/api/personal-assistant/knowledge/explicit",
		bytes.NewBufferString(`{"state_version":1,"request_id":"forge","category":"projects","text":"User owns this","source_kind":"file_janitor"}`)))
	if forged.Code != http.StatusBadRequest {
		t.Fatalf("browser-provided source was accepted: %d %s", forged.Code, forged.Body.String())
	}
}

func TestHiredHomeUsesCurrentApprovedCanonicalKnowledgeOnly(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/hire", bytes.NewBufferString(`{"request_id":"knowledge-hire","if_version":0,"display_name":"Atlas","mandate":"Help me plan.","focus_areas":["plan_my_day"]}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("hire: %d %s", response.Code, response.Body.String())
	}
	beforeState, err := builder.personalAssistantStore.GetState(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	hqBody, err := json.Marshal(map[string]any{"request_id": "knowledge-hq", "if_version": beforeState.StateVersion, "name": "My HQ", "timezone": "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	hqRequest := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/hq", bytes.NewReader(hqBody))
	hqRequest.Header.Set("Content-Type", "application/json")
	hqResponse := httptest.NewRecorder()
	handler.ServeHTTP(hqResponse, hqRequest)
	if hqResponse.Code != http.StatusCreated {
		t.Fatalf("hq setup: %d %s", hqResponse.Code, hqResponse.Body.String())
	}
	state, err := builder.personalAssistantStore.GetState(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	observedAt := time.Now().UTC().Truncate(time.Second)
	if err := builder.onboardingMgr.SetUserProfile(&types.InferredProfile{
		DetectedApps: []string{"Obsidian"}, InferredAt: observedAt,
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	knowledge := personalassistant.NewKnowledgeStore(
		personalassistant.NewKnowledgeResolver(builder.personalAssistantStore, builder.personalHQService,
			personalassistant.NewAgentStoreProfileReader(builder.st)), builder.workspaceFileStore,
	)
	memory := workspace.NewMemoryStore(builder.workspaceFileStore)
	authority := personalassistant.NewSavedAppAuthority(builder.onboardingMgr)
	lifecycle := personalassistant.NewKnowledgeLifecycleService(knowledge, memory, authority)
	producer := personalassistant.NewSavedAppProducer(lifecycle, builder.onboardingMgr, builder.userStore)
	automatic, err := knowledge.Read(ctx, "local")
	if err != nil || len(automatic.Items) != 1 || automatic.Items[0].State != personalassistant.KnowledgeCandidate {
		t.Fatalf("saved-profile completion did not admit a review-only candidate: %+v %v", automatic, err)
	}
	items, err := producer.Check(ctx, "local")
	if err != nil || len(items) != 1 {
		t.Fatalf("saved evidence proposals=%+v err=%v", items, err)
	}
	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/knowledge", nil))
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), "Obsidian") {
		t.Fatalf("read-only review did not show proposal: %d %s", read.Code, read.Body.String())
	}
	adapter := personalAssistantContextAdapter{
		relationship: builder.personalAssistantService, profiles: builder.userStore,
		workspaces: builder.workspaceStore, knowledge: builder.server.Storage.PersonalAssistantKnowledge,
	}
	before, err := adapter.ResolvePersonalAssistantContext(ctx, "local")
	if err != nil || strings.Contains(before.HQMemory, "Obsidian") {
		t.Fatalf("pending suggestion entered Home: %+v %v", before, err)
	}
	forged := httptest.NewRecorder()
	handler.ServeHTTP(forged, httptest.NewRequest(http.MethodPost,
		"/api/personal-assistant/knowledge/"+items[0].ID+"/approve",
		bytes.NewBufferString(`{"version":1,"request_id":"forged","source_kind":"explicit","user_id":"local"}`)))
	if forged.Code != http.StatusBadRequest {
		t.Fatalf("browser-defined evidence accepted: %d %s", forged.Code, forged.Body.String())
	}
	approveBody, err := json.Marshal(map[string]any{"version": items[0].Version, "request_id": "home-approval-1"})
	if err != nil {
		t.Fatal(err)
	}
	approveHTTP := httptest.NewRecorder()
	handler.ServeHTTP(approveHTTP, httptest.NewRequest(http.MethodPost,
		"/api/personal-assistant/knowledge/"+items[0].ID+"/approve", bytes.NewReader(approveBody)))
	if approveHTTP.Code != http.StatusOK {
		t.Fatalf("review approval: %d %s", approveHTTP.Code, approveHTTP.Body.String())
	}
	approvedDoc, err := knowledge.Read(ctx, "local")
	if err != nil || approvedDoc.Items[0].State != personalassistant.KnowledgeApproved {
		t.Fatalf("approval did not persist: %+v %v", approvedDoc, err)
	}
	approved := approvedDoc.Items[0]
	after, err := adapter.ResolvePersonalAssistantContext(ctx, "local")
	if err != nil || !strings.Contains(after.HQMemory, "Obsidian") ||
		after.Sources["reviewed_personal_hq_memory"].Status != "available" {
		t.Fatalf("approved canonical fact absent: %+v %v", after, err)
	}
	if err := builder.onboardingMgr.SetUserProfile(nil); err != nil {
		t.Fatal(err)
	}
	revoked, err := adapter.ResolvePersonalAssistantContext(ctx, "local")
	if err != nil || strings.Contains(revoked.HQMemory, "Obsidian") {
		t.Fatalf("missing source evidence leaked approved text: %+v %v", revoked, err)
	}
	reviewRevoked := httptest.NewRecorder()
	handler.ServeHTTP(reviewRevoked, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/knowledge", nil))
	if reviewRevoked.Code != http.StatusOK || strings.Contains(reviewRevoked.Body.String(), "Obsidian may") ||
		!strings.Contains(reviewRevoked.Body.String(), "source_unavailable") {
		t.Fatalf("review read misrepresented revoked evidence: %d %s", reviewRevoked.Code, reviewRevoked.Body.String())
	}
	if err := builder.onboardingMgr.SetUserProfile(&types.InferredProfile{
		DetectedApps: []string{"Obsidian"}, InferredAt: observedAt,
	}); err != nil {
		t.Fatal(err)
	}
	forgetBody, err := json.Marshal(map[string]any{"version": approved.Version, "request_id": "home-forget-1"})
	if err != nil {
		t.Fatal(err)
	}
	forgetHTTP := httptest.NewRecorder()
	handler.ServeHTTP(forgetHTTP, httptest.NewRequest(http.MethodPost,
		"/api/personal-assistant/knowledge/"+approved.ID+"/forget", bytes.NewReader(forgetBody)))
	if forgetHTTP.Code != http.StatusOK {
		t.Fatalf("review forget: %d %s", forgetHTTP.Code, forgetHTTP.Body.String())
	}
	forgotten, err := adapter.ResolvePersonalAssistantContext(ctx, "local")
	if err != nil || strings.Contains(forgotten.HQMemory, "Obsidian") {
		t.Fatalf("forgotten fact reached Home: %+v %v", forgotten, err)
	}
	guard := builder.hqVisibilityDeps().MemoryWriteGuard
	if err := guard(ctx, state.HQWorkspaceID, "Obsidian may be one of the tools you use to keep notes."); err != workspace.ErrMemoryManaged {
		t.Fatalf("production generic agent tool could resurrect forgotten candidate: %v", err)
	}
	write := builder.hqVisibilityDeps().MemoryWrite
	entry := workspace.MemoryEntry{Type: workspace.MemoryTypeFact, Date: "2026-09-01", Provenance: "agent:Atlas",
		Text: "Obsidian may be one of the tools you use to keep notes."}
	if handled, err := write(ctx, state.HQWorkspaceID, entry); !handled || !errors.Is(err, workspace.ErrMemoryManaged) {
		t.Fatalf("atomic HQ tool write bypassed forgotten suppression: handled=%v err=%v", handled, err)
	}
	if err := builder.workspaceFileStore.Save(&workspace.Workspace{ID: "another-workspace", Name: "Unrelated workspace"}); err != nil {
		t.Fatal(err)
	}
	if handled, err := write(ctx, "another-workspace", entry); handled || err != nil {
		t.Fatalf("ordinary workspace tool semantics changed: handled=%v err=%v", handled, err)
	}
	// Pausing does not promote an inactive fact back into the Home context.
	paused, err := builder.personalAssistantStore.GetState(ctx, "local")
	if err != nil || paused.HQWorkspaceID != state.HQWorkspaceID {
		t.Fatalf("relationship missing: %+v %v", paused, err)
	}
	pausedState := paused.Clone()
	pausedState.Status = personalassistant.StatusPaused
	if _, err := builder.personalAssistantStore.UpdateState(ctx, pausedState, paused.StateVersion); err != nil {
		t.Fatal(err)
	}
	inPause, err := adapter.ResolvePersonalAssistantContext(ctx, "local")
	if err != nil || strings.Contains(inPause.HQMemory, "Obsidian") ||
		inPause.Sources["reviewed_personal_hq_memory"].Reason != "assistant_paused" {
		t.Fatalf("paused context: %+v %v", inPause, err)
	}
}
