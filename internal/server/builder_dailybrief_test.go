package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// newDailyBriefTestServer builds a full server (same wiring path as
// production, via ServerBuilder.Build) rooted at a temp directory, mirroring
// newRoutesTestHandler in routes_test.go. Returns the builder itself (not
// just its handler) so tests can reach unexported fields like
// dailyBriefService/workspaceStore to drive a TriggerScheduled generation,
// which has no HTTP route (only first-open/manual are exposed to clients).
func newDailyBriefTestServer(t *testing.T) (*ServerBuilder, http.Handler) {
	t.Helper()
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir to temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalWD) })
	// DefaultWorkspaceRoot() resolves to $HOME/Ori Workspaces regardless of
	// CWD (it does not respect ORI_DATA_DIR either) — this test actually
	// creates a workspace, so without this it would write into the real
	// user's home directory. t.Setenv restores the original HOME after the
	// test.
	t.Setenv("HOME", tmpDir)

	builder, err := NewServerBuilder()
	if err != nil {
		t.Fatalf("NewServerBuilder failed: %v", err)
	}
	srv, err := builder.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if builder.dailyBriefService == nil {
		t.Fatal("expected dailyBriefService to be wired")
	}
	return builder, srv.Handler()
}

func TestDailyBrief_FollowUpSourceIsWiredAndGroundedWithoutModel(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)
	ctx := context.Background()
	setupReq := httptest.NewRequest(http.MethodPost, "/api/personal-hq/setup", bytes.NewBufferString(`{"name":"Personal HQ"}`))
	setupReq.Header.Set("Content-Type", "application/json")
	setupRec := httptest.NewRecorder()
	handler.ServeHTTP(setupRec, setupReq)
	if setupRec.Code != http.StatusOK {
		t.Fatalf("setup status=%d body=%s", setupRec.Code, setupRec.Body.String())
	}
	var setup struct {
		Status struct {
			WorkspaceID string `json:"workspace_id"`
		} `json:"status"`
	}
	if err := json.Unmarshal(setupRec.Body.Bytes(), &setup); err != nil || setup.Status.WorkspaceID == "" {
		t.Fatalf("setup response=%s err=%v", setupRec.Body.String(), err)
	}
	if _, err := builder.dailyBriefService.UpdateConfig(ctx, dailybrief.Config{
		WorkspaceID: setup.Status.WorkspaceID, UserID: "local", Timezone: "UTC",
		Scope: dailybrief.ScopeAll, IncludeFutureWorkspaces: true,
	}); err != nil {
		t.Fatal(err)
	}
	due := time.Now().Add(-time.Hour)
	captured, err := builder.followUpService.Capture(ctx, followup.CaptureInput{
		UserID: "local", WorkspaceID: setup.Status.WorkspaceID,
		Category: followup.CategoryIOwe, Direction: followup.DirectionOutbound,
		Title: "Send the signed form", DueAt: &due, Provenance: followup.ProvenanceExplicit,
		Source: followup.SourceRef{Type: "personal_assistant_first_assignment", ID: "item-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := builder.dailyBriefService.RequestGenerationNow(ctx, setup.Status.WorkspaceID, "local", dailybrief.TriggerFirstOpen)
	if err != nil {
		t.Fatal(err)
	}
	var content dailybrief.BriefContent
	if err := json.Unmarshal([]byte(revision.ContentJSON), &content); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range content.NeedsAttention {
		if item.Ref.EntityType == "follow_up" && item.Ref.EntityID == captured.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("grounded follow-up absent from brief: %#v", content)
	}
}

func TestEmailOpsFollowUpFlowsThroughProductionWriterTodayAndDailyBriefWithoutMovingOwnership(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)
	ctx := context.Background()

	hireReq := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/hire", bytes.NewBufferString(`{"request_id":"followup-hire","if_version":0,"display_name":"Atlas","mandate":"Keep commitments visible.","focus_areas":["plan_my_day"]}`))
	hireReq.Header.Set("Content-Type", "application/json")
	hireRec := httptest.NewRecorder()
	handler.ServeHTTP(hireRec, hireReq)
	if hireRec.Code != http.StatusCreated {
		t.Fatalf("hire status=%d body=%s", hireRec.Code, hireRec.Body.String())
	}
	state := activateTestRelationshipWithHQ(t, builder, handler, "local")

	emailOps := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Email Ops"})
	emailOps.ID = "email-ops-integration"
	emailOps.FolderSlug = "email-ops"
	emailOps.OwnerUserID = "local"
	emailOps.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: workspace.EmailOpsTemplateID, Builtin: true})
	if err := builder.workspaceStore.Save(emailOps); err != nil {
		t.Fatalf("save Email Ops: %v", err)
	}
	if _, err := builder.dailyBriefService.UpdateConfig(ctx, dailybrief.Config{
		WorkspaceID: state.HQWorkspaceID, UserID: "local", Timezone: "UTC",
		Scope: dailybrief.ScopeSelected, SelectedWorkspaceIDs: []string{emailOps.ID},
	}); err != nil {
		t.Fatalf("configure Email Ops scope: %v", err)
	}

	dueToday := time.Now().UTC().Truncate(time.Second)
	preexisting, err := builder.followUpService.Capture(ctx, followup.CaptureInput{
		UserID: "local", WorkspaceID: emailOps.ID, Category: followup.CategoryWaitingOn,
		Direction: followup.DirectionInbound, Title: "Pre-existing Email Ops row", DueAt: &dueToday,
		Source:     followup.SourceRef{Type: "email_thread", ID: "thread-existing", AccountID: "account-1"},
		Provenance: followup.ProvenanceExplicit,
	})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := builder.personalAssignment.Preview(ctx, "local", state.StateVersion, personalassistant.AssignmentInput{
		Rows: []personalassistant.AssignmentInputRow{{
			Type: personalassistant.AssignmentRowFixedCommitment, Title: "HQ first-assignment follow-up",
			Due: time.Now().UTC().Format("2006-01-02"),
		}},
	})
	if err != nil {
		t.Fatalf("preview first assignment: %v", err)
	}
	applied, err := builder.personalAssignment.Apply(ctx, "local", personalassistant.AssignmentApplyRequest{
		PreviewID: preview.Preview.PreviewID, PreviewVersion: preview.Preview.AssignmentVersion,
		PayloadHash: preview.Preview.PayloadHash, IfVersion: preview.StateVersion, ApplyRequestID: "integration-apply-442",
	})
	if err != nil {
		t.Fatalf("apply first assignment: %v", err)
	}
	if len(applied.CreatedCanonicalRefs) != 1 || applied.CreatedCanonicalRefs[0].Kind != "follow_up" {
		t.Fatalf("first assignment refs = %+v", applied.CreatedCanonicalRefs)
	}
	hqFollowUp, err := builder.followUpService.Get(ctx, "local", applied.CreatedCanonicalRefs[0].ID)
	if err != nil {
		t.Fatalf("read first-assignment follow-up: %v", err)
	}
	if hqFollowUp.WorkspaceID != state.HQWorkspaceID {
		t.Fatalf("first-assignment owner = %q, want HQ %q", hqFollowUp.WorkspaceID, state.HQWorkspaceID)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/api/personal-hq/followups", bytes.NewBufferString(`{"category":"needs_decision","direction":"inbound","title":"Waiting for Alex's signed agreement","counterparty":"Alex"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create follow-up status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var createdPayload struct {
		FollowUp *followup.FollowUp `json:"followup"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &createdPayload); err != nil || createdPayload.FollowUp == nil {
		t.Fatalf("decode created follow-up: followup=%+v err=%v", createdPayload.FollowUp, err)
	}
	created := createdPayload.FollowUp
	if created.WorkspaceID != emailOps.ID || created.Source.Type != "manual" || created.Provenance != followup.ProvenanceManual {
		t.Fatalf("real writer moved ownership/provenance: %+v", created)
	}

	panelReq := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+emailOps.ID+"/followups", nil)
	panelRec := httptest.NewRecorder()
	handler.ServeHTTP(panelRec, panelReq)
	var panelPayload struct {
		FollowUps []*followup.FollowUp `json:"followups"`
	}
	if panelRec.Code != http.StatusOK {
		t.Fatalf("panel status=%d body=%s", panelRec.Code, panelRec.Body.String())
	}
	if err := json.Unmarshal(panelRec.Body.Bytes(), &panelPayload); err != nil || len(panelPayload.FollowUps) != 2 {
		t.Fatalf("panel follow-ups=%+v err=%v body=%s", panelPayload.FollowUps, err, panelRec.Body.String())
	}
	portalReq := httptest.NewRequest(http.MethodGet, "/api/personal-hq/email-ops", nil)
	portalRec := httptest.NewRecorder()
	handler.ServeHTTP(portalRec, portalReq)
	var portalPayload struct {
		Status struct {
			WorkspaceID       string `json:"workspace_id"`
			OpenFollowupCount int    `json:"open_followup_count"`
		} `json:"status"`
	}
	if err := json.Unmarshal(portalRec.Body.Bytes(), &portalPayload); err != nil ||
		portalPayload.Status.WorkspaceID != emailOps.ID || portalPayload.Status.OpenFollowupCount != 2 {
		t.Fatalf("portal=%+v err=%v body=%s", portalPayload, err, portalRec.Body.String())
	}

	assertToday := func(wantCreated bool) {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/personal-assistant/today", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("today status=%d body=%s", rec.Code, rec.Body.String())
		}
		var payload struct {
			Today personalassistant.TodayProjection `json:"today"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode Today: %v", err)
		}
		seen, decisions := map[string]dailybrief.SourceRef{}, map[string]dailybrief.SourceRef{}
		for _, item := range payload.Today.FollowUps.Items {
			seen[item.ID] = item.Ref
		}
		for _, item := range payload.Today.Decisions.Items {
			decisions[item.ID] = item.Ref
		}
		if _, ok := seen[preexisting.ID]; !ok {
			t.Fatalf("pre-existing Email Ops row missing from Today: %+v", payload.Today.FollowUps)
		}
		if _, ok := seen[hqFollowUp.ID]; !ok {
			t.Fatalf("HQ follow-up missing from Today: %+v", payload.Today.FollowUps)
		}
		createdRef, createdSeen := seen[created.ID]
		_, decisionSeen := decisions[created.ID]
		if createdSeen != wantCreated || decisionSeen != wantCreated {
			t.Fatalf("created visibility=%v decision=%v want=%v today=%+v", createdSeen, decisionSeen, wantCreated, payload.Today)
		}
		if wantCreated && (createdRef.WorkspaceID != emailOps.ID || createdRef.WorkspaceSlug != emailOps.FolderSlug) {
			t.Fatalf("Today ref not grounded to Email Ops: %+v", createdRef)
		}
	}
	assertToday(true)

	generate := func() dailybrief.BriefContent {
		t.Helper()
		revision, err := builder.dailyBriefService.RequestGenerationNow(ctx, state.HQWorkspaceID, "local", dailybrief.TriggerManual)
		if err != nil {
			t.Fatalf("generate brief: %v", err)
		}
		var content dailybrief.BriefContent
		if err := json.Unmarshal([]byte(revision.ContentJSON), &content); err != nil {
			t.Fatalf("decode brief: %v", err)
		}
		return content
	}
	brief := generate()
	briefRefs := func(content dailybrief.BriefContent) map[string]dailybrief.SourceRef {
		refs := map[string]dailybrief.SourceRef{}
		for _, item := range content.NeedsAttention {
			refs[item.Ref.EntityID] = item.Ref
		}
		for _, item := range content.SinceLastBrief {
			refs[item.Ref.EntityID] = item.Ref
		}
		for _, item := range content.TodaysPlan {
			refs[item.Ref.EntityID] = item.Ref
		}
		return refs
	}
	refs := briefRefs(brief)
	if refs[created.ID].WorkspaceID != emailOps.ID || refs[created.ID].WorkspaceSlug != emailOps.FolderSlug ||
		refs[preexisting.ID].WorkspaceID != emailOps.ID || refs[hqFollowUp.ID].WorkspaceID != state.HQWorkspaceID {
		t.Fatalf("brief refs lost real owners: %+v", refs)
	}

	all, err := builder.followUpService.List(ctx, followup.Filter{UserID: "local"})
	if err != nil || len(all) != 3 {
		t.Fatalf("aggregation cloned or lost canonical rows: count=%d err=%v rows=%+v", len(all), err, all)
	}
	persistedExisting, err := builder.followUpService.Get(ctx, "local", preexisting.ID)
	if err != nil || persistedExisting.WorkspaceID != emailOps.ID || persistedExisting.Source != preexisting.Source {
		t.Fatalf("pre-existing row was rewritten: before=%+v after=%+v err=%v", preexisting, persistedExisting, err)
	}

	completeReq := httptest.NewRequest(http.MethodPost, "/api/personal-hq/followups/complete", bytes.NewBufferString(`{"id":"`+created.ID+`"}`))
	completeReq.Header.Set("Content-Type", "application/json")
	completeRec := httptest.NewRecorder()
	handler.ServeHTTP(completeRec, completeReq)
	if completeRec.Code != http.StatusOK {
		t.Fatalf("complete status=%d body=%s", completeRec.Code, completeRec.Body.String())
	}
	assertToday(false)
	if ref, exists := briefRefs(generate())[created.ID]; exists {
		t.Fatalf("completed owner-local row remained in regenerated brief: %+v", ref)
	}
	persistedCreated, err := builder.followUpService.Get(ctx, "local", created.ID)
	if err != nil || persistedCreated.WorkspaceID != emailOps.ID || persistedCreated.Status != followup.StatusCompleted {
		t.Fatalf("completion moved original row: %+v err=%v", persistedCreated, err)
	}
}

// TestDailyBrief_ScheduledSuccessCreatesExactlyOneActionCenterNotification
// covers task 7.11 end-to-end: initializeDailyBrief's onRevisionReady hook
// (builder_dailybrief.go) must actually create a visible Action Center
// opportunity for a successful *scheduled* generation when the HQ opted in,
// wiring RecordNotificationIfEnabled (unit-tested in internal/dailybrief) to
// a real workspace.OpportunityStore.Upsert call — previously unverified
// past the unit boundary.
func TestDailyBrief_ScheduledSuccessCreatesExactlyOneActionCenterNotification(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)
	ctx := context.Background()

	// Build My HQ (creates + designates the workspace in one call).
	setupReq := httptest.NewRequest(http.MethodPost, "/api/personal-hq/setup", bytes.NewBufferString(`{"name":"Command Post"}`))
	setupReq.Header.Set("Content-Type", "application/json")
	setupRec := httptest.NewRecorder()
	handler.ServeHTTP(setupRec, setupReq)
	if setupRec.Code != http.StatusOK {
		t.Fatalf("setup status = %d body=%s", setupRec.Code, setupRec.Body.String())
	}
	var setupResp struct {
		Status struct {
			WorkspaceID string `json:"workspace_id"`
		} `json:"status"`
	}
	if err := json.Unmarshal(setupRec.Body.Bytes(), &setupResp); err != nil {
		t.Fatalf("decode setup response: %v", err)
	}
	workspaceID := setupResp.Status.WorkspaceID
	if workspaceID == "" {
		t.Fatal("expected a designated HQ workspace id")
	}

	// Opt in to notifications.
	putReq := httptest.NewRequest(http.MethodPut, "/api/personal-hq/brief/config", bytes.NewBufferString(`{"notify_on_ready":true}`))
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("config PUT status = %d body=%s", putRec.Code, putRec.Body.String())
	}

	// Drive a TriggerScheduled generation directly — the scheduler's own
	// trigger path has no HTTP route by design (only first-open/manual are
	// client-initiated).
	cfg, err := builder.dailyBriefService.GetConfig(ctx, workspaceID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	localDate, err := dailybrief.TodayLocalDate(*cfg)
	if err != nil {
		t.Fatalf("TodayLocalDate: %v", err)
	}
	rev, err := builder.dailyBriefService.RequestGeneration(ctx, *cfg, "local", dailybrief.TriggerScheduled, localDate)
	if err != nil {
		t.Fatalf("RequestGeneration: %v", err)
	}
	if rev.Status != dailybrief.GenerationSucceeded && rev.Status != dailybrief.GenerationPartial {
		t.Fatalf("expected the scheduled generation to succeed or partially succeed, got %q", rev.Status)
	}

	opportunityStore := workspace.NewOpportunityStore(builder.workspaceStore)
	opportunities, err := opportunityStore.List(workspaceID)
	if err != nil {
		t.Fatalf("List opportunities: %v", err)
	}
	if len(opportunities) != 1 {
		t.Fatalf("expected exactly one Action Center opportunity, got %d: %#v", len(opportunities), opportunities)
	}
	if opportunities[0].WorkspaceID != workspaceID {
		t.Fatalf("expected the opportunity to be scoped to the HQ workspace, got %q", opportunities[0].WorkspaceID)
	}

	// A manual refresh on the same day must not create a second
	// notification (PRD FR63: manual/first-open never notify).
	if _, err := builder.dailyBriefService.RequestGeneration(ctx, *cfg, "local", dailybrief.TriggerManual, localDate); err != nil {
		t.Fatalf("manual RequestGeneration: %v", err)
	}
	opportunities, err = opportunityStore.List(workspaceID)
	if err != nil {
		t.Fatalf("List opportunities after manual refresh: %v", err)
	}
	if len(opportunities) != 1 {
		t.Fatalf("expected manual refresh to create no additional opportunity, got %d: %#v", len(opportunities), opportunities)
	}
}

// activateTestRelationshipWithHQ builds a real Personal HQ through the legacy
// Build My HQ endpoint and binds the already-hired relationship to it, leaving
// the relationship active.
//
// It stands in for the guided Map quest so tests whose subject is something
// else — a context adapter, a brief projection — do not have to drive the whole
// walkthrough. It is a fixture, not a claim about how HQ is really created:
// the canonical path is POST /api/personal-assistant/hq.
func activateTestRelationshipWithHQ(t *testing.T, builder *ServerBuilder, handler http.Handler, userID string) *personalassistant.State {
	t.Helper()
	ctx := context.Background()

	setupReq := httptest.NewRequest(http.MethodPost, "/api/personal-hq/setup", bytes.NewBufferString(`{"name":"Personal HQ"}`))
	setupReq.Header.Set("Content-Type", "application/json")
	setupRec := httptest.NewRecorder()
	handler.ServeHTTP(setupRec, setupReq)
	if setupRec.Code != http.StatusOK {
		t.Fatalf("build hq status = %d body=%s", setupRec.Code, setupRec.Body.String())
	}
	var setupResp struct {
		Status struct {
			WorkspaceID string `json:"workspace_id"`
		} `json:"status"`
	}
	if err := json.Unmarshal(setupRec.Body.Bytes(), &setupResp); err != nil {
		t.Fatalf("decode build hq response: %v", err)
	}

	workspaceID := setupResp.Status.WorkspaceID
	created, err := builder.sessionStore.GetWorkspace(ctx, workspaceID)
	if err != nil {
		t.Fatalf("load created hq: %v", err)
	}
	var entry session.AgentInstance
	for _, instance := range created.AgentInstances {
		if instance.EntryPoint {
			entry = instance
			break
		}
	}
	if entry.ID == "" {
		t.Fatal("created hq has no entry agent instance")
	}

	state, err := builder.personalAssistantStore.GetState(ctx, userID)
	if err != nil {
		t.Fatalf("load relationship: %v", err)
	}
	next := state.Clone()
	next.Status = personalassistant.StatusActive
	next.HQWorkspaceID = workspaceID
	next.HQEntryAgentInstanceID = entry.ID
	next.GlobalAgentProfileName = entry.Name
	updated, err := builder.personalAssistantStore.UpdateState(ctx, next, state.StateVersion)
	if err != nil {
		t.Fatalf("bind relationship to hq: %v", err)
	}
	return updated
}

// TestReplaceNeverCopiesBriefHistoryToNewHQ covers task 8.4: replacing the
// designated HQ must never carry the former HQ's brief config/history onto
// the new one — dailybrief.Store keys everything by WorkspaceID, but this
// proves it end-to-end through the real HTTP surface (which resolves
// "current HQ" dynamically via personalhq.Status on every call) rather than
// only at the storage layer.
func TestPersonalAssistantContextReadsMemoryEditsAndDeletesOnNextTurn(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)
	hireReq := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/hire", bytes.NewBufferString(`{"request_id":"memory-hire","if_version":0,"display_name":"Atlas","mandate":"Keep commitments visible.","focus_areas":["plan_my_day"]}`))
	hireReq.Header.Set("Content-Type", "application/json")
	hireRec := httptest.NewRecorder()
	handler.ServeHTTP(hireRec, hireReq)
	if hireRec.Code != http.StatusCreated {
		t.Fatalf("hire status=%d body=%s", hireRec.Code, hireRec.Body.String())
	}
	// Hiring no longer creates Personal HQ: that is the guided Map quest's
	// separate, user-confirmed consequence. This test is about the context
	// adapter reading HQ memory, so it takes the relationship to active state
	// directly rather than driving the whole quest.
	state := activateTestRelationshipWithHQ(t, builder, handler, "local")
	memory := workspace.NewMemoryStore(builder.workspaceFileStore)
	if err := memory.Append(state.HQWorkspaceID, workspace.MemoryEntry{Type: workspace.MemoryTypeFact, Date: "2026-09-01", Provenance: "user", Text: "Launch review is Thursday"}); err != nil {
		t.Fatal(err)
	}
	adapter := personalAssistantContextAdapter{
		relationship: builder.personalAssistantService, profiles: builder.userStore, workspaces: builder.workspaceStore,
	}
	first, err := adapter.ResolvePersonalAssistantContext(context.Background(), "local")
	if err != nil || !strings.Contains(first.HQMemory, "Launch review is Thursday") {
		t.Fatalf("first memory context=%+v err=%v", first, err)
	}
	if err := memory.EditAt(state.HQWorkspaceID, 0, workspace.MemoryEntry{Type: workspace.MemoryTypeFact, Date: "2026-09-01", Provenance: "user", Text: "Launch review moved to Wednesday"}); err != nil {
		t.Fatal(err)
	}
	second, err := adapter.ResolvePersonalAssistantContext(context.Background(), "local")
	if err != nil || strings.Contains(second.HQMemory, "Thursday") || !strings.Contains(second.HQMemory, "Wednesday") {
		t.Fatalf("edited memory context=%+v err=%v", second, err)
	}
	if _, err := memory.Forget(state.HQWorkspaceID, "Launch review moved to Wednesday"); err != nil {
		t.Fatal(err)
	}
	third, err := adapter.ResolvePersonalAssistantContext(context.Background(), "local")
	if err != nil || strings.Contains(third.HQMemory, "Launch review") {
		t.Fatalf("deleted memory was resurrected: context=%+v err=%v", third, err)
	}
}

func TestPersonalHQWorkspaceLister_RequiresActivePersonalAssistant(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)
	ctx := context.Background()
	setupReq := httptest.NewRequest(http.MethodPost, "/api/personal-hq/setup", bytes.NewBufferString(`{"name":"Personal HQ"}`))
	setupReq.Header.Set("Content-Type", "application/json")
	setupRec := httptest.NewRecorder()
	handler.ServeHTTP(setupRec, setupReq)
	if setupRec.Code != http.StatusOK {
		t.Fatalf("setup status=%d body=%s", setupRec.Code, setupRec.Body.String())
	}
	status, err := builder.personalHQService.Status(ctx, "local")
	if err != nil || status == nil || !status.Valid {
		t.Fatalf("HQ status=%+v err=%v", status, err)
	}
	lister := &personalHQWorkspaceLister{
		service: builder.personalHQService, relationship: builder.personalAssistantStore,
	}
	var entry *session.AgentInstance
	for i := range status.Workspace.AgentInstances {
		if status.Workspace.AgentInstances[i].EntryPoint {
			entry = &status.Workspace.AgentInstances[i]
			break
		}
	}
	if entry == nil {
		t.Fatal("Personal HQ entry missing")
	}
	state := personalassistant.NewState("local")
	if _, err := builder.personalAssistantStore.CreateState(ctx, state); err != nil {
		t.Fatal(err)
	}
	notHired, err := lister.ListScheduledWorkspaces(ctx)
	if err != nil || len(notHired) != 0 {
		t.Fatalf("HQ without an active personal assistant was scheduled: %+v err=%v", notHired, err)
	}
	persisted, _ := builder.personalAssistantStore.GetState(ctx, "local")
	persisted.Status = personalassistant.StatusPaused
	persisted.DisplayName = entry.Name
	persisted.GlobalAgentProfileName = entry.Name
	persisted.HQWorkspaceID = status.WorkspaceID
	persisted.HQEntryAgentInstanceID = entry.ID
	if _, err := builder.personalAssistantStore.UpdateState(ctx, persisted, persisted.StateVersion); err != nil {
		t.Fatal(err)
	}
	paused, err := lister.ListScheduledWorkspaces(ctx)
	if err != nil || len(paused) != 0 {
		t.Fatalf("paused PAF was scheduled: %+v err=%v", paused, err)
	}
	persisted, _ = builder.personalAssistantStore.GetState(ctx, "local")
	persisted.Status = personalassistant.StatusActive
	if _, err := builder.personalAssistantStore.UpdateState(ctx, persisted, persisted.StateVersion); err != nil {
		t.Fatal(err)
	}
	active, err := lister.ListScheduledWorkspaces(ctx)
	if err != nil || len(active) != 1 || active[0].WorkspaceID != status.WorkspaceID {
		t.Fatalf("resumed PAF was not scheduled: %+v err=%v", active, err)
	}
}

func TestReplaceNeverCopiesBriefHistoryToNewHQ(t *testing.T) {
	builder, handler := newDailyBriefTestServer(t)
	ctx := context.Background()

	setupReq := httptest.NewRequest(http.MethodPost, "/api/personal-hq/setup", bytes.NewBufferString(`{"name":"Command Post"}`))
	setupReq.Header.Set("Content-Type", "application/json")
	setupRec := httptest.NewRecorder()
	handler.ServeHTTP(setupRec, setupReq)
	if setupRec.Code != http.StatusOK {
		t.Fatalf("setup status = %d body=%s", setupRec.Code, setupRec.Body.String())
	}
	var setupResp struct {
		Status struct {
			WorkspaceID string `json:"workspace_id"`
		} `json:"status"`
	}
	if err := json.Unmarshal(setupRec.Body.Bytes(), &setupResp); err != nil {
		t.Fatalf("decode setup response: %v", err)
	}
	oldHQID := setupResp.Status.WorkspaceID

	openReq := httptest.NewRequest(http.MethodPost, "/api/personal-hq/brief/open", nil)
	openRec := httptest.NewRecorder()
	handler.ServeHTTP(openRec, openReq)
	if openRec.Code != http.StatusAccepted {
		t.Fatalf("open status = %d body=%s", openRec.Code, openRec.Body.String())
	}
	var oldRevision *dailybrief.Revision
	var err error
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		oldRevision, err = builder.dailyBriefService.GetCurrent(ctx, oldHQID)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if oldRevision == nil {
		t.Fatalf("expected the original HQ to have a current brief before replacing, last err: %v", err)
	}

	newHQ := &session.Workspace{
		ID: "ws-new-hq", Name: "New Command Post", Kind: session.WorkspaceKindWorkspace,
		OwnerUserID: "local", Status: session.WorkspaceStatusActive,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := builder.sessionStore.CreateWorkspace(ctx, newHQ); err != nil {
		t.Fatalf("CreateWorkspace(new hq): %v", err)
	}
	replaceReq := httptest.NewRequest(http.MethodPost, "/api/personal-hq/replace", bytes.NewBufferString(`{"workspace_id":"ws-new-hq"}`))
	replaceReq.Header.Set("Content-Type", "application/json")
	replaceRec := httptest.NewRecorder()
	handler.ServeHTTP(replaceRec, replaceReq)
	if replaceRec.Code != http.StatusOK {
		t.Fatalf("replace status = %d body=%s", replaceRec.Code, replaceRec.Body.String())
	}

	// The HTTP surface resolves "current HQ" dynamically, so it now points
	// at the new workspace — its brief config/history must be untouched
	// defaults, never a copy of the old HQ's.
	configReq := httptest.NewRequest(http.MethodGet, "/api/personal-hq/brief/config", nil)
	configRec := httptest.NewRecorder()
	handler.ServeHTTP(configRec, configReq)
	var configResp struct {
		Configured bool `json:"configured"`
	}
	if err := json.Unmarshal(configRec.Body.Bytes(), &configResp); err != nil {
		t.Fatalf("decode config response: %v", err)
	}
	if configResp.Configured {
		t.Fatal("expected the new HQ to start with no brief config of its own, not the old HQ's")
	}

	currentReq := httptest.NewRequest(http.MethodGet, "/api/personal-hq/brief/current", nil)
	currentRec := httptest.NewRecorder()
	handler.ServeHTTP(currentRec, currentReq)
	var currentResp struct {
		Revision *dailybrief.Revision `json:"revision"`
	}
	if err := json.Unmarshal(currentRec.Body.Bytes(), &currentResp); err != nil {
		t.Fatalf("decode current response: %v", err)
	}
	if currentResp.Revision != nil {
		t.Fatalf("expected the new HQ to have no current brief, got %#v", currentResp.Revision)
	}

	// The former HQ's own brief history must remain completely intact,
	// still addressable by its own workspace id.
	stillThere, err := builder.dailyBriefService.GetCurrent(ctx, oldHQID)
	if err != nil {
		t.Fatalf("expected the former HQ's brief to remain intact after replace: %v", err)
	}
	if stillThere.ID != oldRevision.ID {
		t.Fatalf("expected the former HQ's revision to be untouched, got %#v want %#v", stillThere, oldRevision)
	}
}
