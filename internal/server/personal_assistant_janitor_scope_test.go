package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type janitorKnowledgeTestWorkspaces struct{ ws *workspace.Workspace }

func (f janitorKnowledgeTestWorkspaces) Get(id string) (*workspace.Workspace, error) {
	if f.ws == nil || f.ws.ID != id {
		return nil, errors.New("missing workspace")
	}
	return f.ws, nil
}
func (f janitorKnowledgeTestWorkspaces) ListActive() ([]*workspace.Workspace, error) {
	if f.ws == nil {
		return nil, nil
	}
	return []*workspace.Workspace{f.ws}, nil
}

type janitorKnowledgeTestBrief struct{ cfg *dailybrief.Config }

func (f janitorKnowledgeTestBrief) GetConfig(context.Context, string) (*dailybrief.Config, error) {
	return f.cfg, nil
}

type janitorKnowledgeTestJournal struct {
	status  filejanitor.Status
	actions []filejanitor.FileAction
	reads   int
}

func (f *janitorKnowledgeTestJournal) Status(string) (filejanitor.Status, error) {
	return f.status, nil
}
func (f *janitorKnowledgeTestJournal) ListActions(string) ([]filejanitor.FileAction, error) {
	f.reads++
	return f.actions, nil
}

func TestJanitorKnowledgeScopeSourceAndUndoExcludePromptsWithoutNotifications(t *testing.T) {
	ctx := context.Background()
	builder, handler := newDailyBriefTestServer(t)
	hire := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/personal-assistant/hire", bytes.NewBufferString(`{"request_id":"janitor-hire","if_version":0,"display_name":"Atlas","mandate":"Help me plan.","focus_areas":["plan_my_day"]}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(hire, req)
	if hire.Code != http.StatusCreated {
		t.Fatalf("hire: %d %s", hire.Code, hire.Body.String())
	}
	before, err := builder.personalAssistantStore.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	hq := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/personal-assistant/hq",
		bytes.NewBufferString(`{"request_id":"janitor-hq","if_version":`+strconv.FormatInt(before.StateVersion, 10)+`,"name":"My HQ","timezone":"UTC"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(hq, req)
	if hq.Code != http.StatusCreated {
		t.Fatalf("hq: %d %s", hq.Code, hq.Body.String())
	}
	state, err := builder.personalAssistantStore.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	profileReader := personalassistant.NewAgentStoreProfileReader(builder.st)
	bindings := personalassistant.NewKnowledgeResolver(builder.personalAssistantStore, builder.personalHQService, profileReader)
	janitorWS := &workspace.Workspace{ID: "janitor-ws", OwnerUserID: "local", Name: "Private inbox", Status: workspace.StatusActive}
	janitorWS.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: filejanitor.LegacyTemplateID, Builtin: true})
	cfg := &dailybrief.Config{WorkspaceID: state.HQWorkspaceID, Scope: dailybrief.ScopeSelected,
		SelectedWorkspaceIDs: []string{janitorWS.ID}}
	at := time.Now().UTC().Truncate(time.Second)
	journal := &janitorKnowledgeTestJournal{status: janitorKnowledgeTestStatus(at), actions: []filejanitor.FileAction{
		janitorKnowledgeTestAction("one", at.Add(-3*time.Hour)),
		janitorKnowledgeTestAction("two", at.Add(-2*time.Hour)),
		janitorKnowledgeTestAction("three", at.Add(-time.Hour)),
	}}
	reader := &janitorKnowledgeReader{bindings: bindings, briefs: janitorKnowledgeTestBrief{cfg},
		workspaces: janitorKnowledgeTestWorkspaces{janitorWS}, janitor: journal, now: func() time.Time { return at }}
	patterns, err := reader.ReadFresh(ctx, "local")
	if err != nil || len(patterns) != 1 {
		t.Fatalf("eligible scoped actions=%+v err=%v", patterns, err)
	}
	knowledge := personalassistant.NewKnowledgeStore(bindings, builder.workspaceFileStore)
	memory := workspace.NewMemoryStore(builder.workspaceFileStore)
	authority := scopedKnowledgeAuthority{apps: personalassistant.NewSavedAppAuthority(builder.onboardingMgr), janitor: reader}
	learning := personalassistant.NewKnowledgeLifecycleService(knowledge, memory, authority)
	producer := janitorKnowledgeProducer{reader: reader, learning: learning}
	items, err := producer.Check(ctx, "local")
	if err != nil || len(items) != 1 || len(items[0].Revisions[0].Evidence) != 3 {
		t.Fatalf("bounded Janitor suggestion=%+v err=%v", items, err)
	}
	readerContext := personalassistant.NewKnowledgeContextReader(knowledge, memory, authority)
	if section, err := readerContext.HomeSection(ctx, "local", state.HQWorkspaceID); err != nil || section != "" {
		t.Fatalf("pending Janitor suggestion leaked: %q %v", section, err)
	}
	approved, err := learning.ApproveCandidate(ctx, "local", items[0].ID, items[0].Version, "janitor-approve")
	if err != nil || approved.State != personalassistant.KnowledgeApproved {
		t.Fatalf("Janitor approval: %+v %v", approved, err)
	}
	section, err := readerContext.HomeSection(ctx, "local", state.HQWorkspaceID)
	if err != nil || !strings.Contains(section, "filing documents") {
		t.Fatalf("approved Janitor fact absent: %q %v", section, err)
	}
	for _, scenario := range []struct {
		name    string
		change  func()
		restore func()
	}{
		{"foreign owner", func() { janitorWS.OwnerUserID = "foreign" }, func() { janitorWS.OwnerUserID = "local" }},
		{"brief scope revoked", func() { cfg.SelectedWorkspaceIDs = []string{"other"} }, func() { cfg.SelectedWorkspaceIDs = []string{janitorWS.ID} }},
		{"unverified plugin provenance", func() {
			janitorWS.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: filejanitor.LegacyTemplateID, Builtin: false})
		}, func() {
			janitorWS.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: filejanitor.LegacyTemplateID, Builtin: true})
		}},
		{"root read permission revoked", func() { journal.status.Readiness.Checks[0].Status = filejanitor.ComponentFailed }, func() { journal.status.Readiness.Checks[0].Status = filejanitor.ComponentOK }},
		{"root generation changed", func() { journal.status.Settings.RootID = "different-generation" }, func() { journal.status.Settings.RootID = "root-generation-1" }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			scenario.change()
			defer scenario.restore()
			if text, err := readerContext.HomeSection(ctx, "local", state.HQWorkspaceID); err != nil || strings.Contains(text, "filing documents") {
				t.Fatalf("revoked source leaked approved fact: %q %v", text, err)
			}
			if scenario.name == "root read permission revoked" {
				sources := personalKnowledgeSources{bindings: bindings, janitor: reader}.ReadSources(ctx, "local")
				if sources.FileJanitor.Status != "revoked" {
					t.Fatalf("missing read access was reported as a healthy empty or generic source: %+v", sources)
				}
			}
		})
	}
	journal.actions[0].Undo = filejanitor.UndoFailed
	journal.actions[0].UndoneAt = at
	section, err = readerContext.HomeSection(ctx, "local", state.HQWorkspaceID)
	if err != nil || !strings.Contains(section, "filing documents") {
		t.Fatalf("failed undo hid valid support: %q %v", section, err)
	}
	journal.actions[0].Undo = filejanitor.UndoDone
	section, err = readerContext.HomeSection(ctx, "local", state.HQWorkspaceID)
	if err != nil || section != "" {
		t.Fatalf("successful undo without notification leaked fact: %q %v", section, err)
	}
	// Simulate a lost completion event: explicit Check must recover from the
	// durable journal without rescanning files or inventing a new candidate.
	recovery := &janitorKnowledgeRecovery{reader: reader, store: knowledge, learning: learning}
	producer.recovery = recovery
	recovery.producer = producer
	if next, err := producer.Check(ctx, "local"); err != nil || len(next) != 0 {
		t.Fatalf("missed undo not reconciled: %+v %v", next, err)
	}
	persisted, err := knowledge.Read(ctx, "local")
	if err != nil || len(persisted.Items) != 1 || persisted.Items[0].State != personalassistant.KnowledgeNeedsReview || persisted.Items[0].Target != nil {
		t.Fatalf("approved canonical fact not suspended: %+v %v", persisted.Items, err)
	}
	if section, err := readerContext.HomeSection(ctx, "local", state.HQWorkspaceID); err != nil || section != "" {
		t.Fatalf("suspended fact visible after recovery: %q %v", section, err)
	}
	if err := recovery.Reconcile(ctx, "local"); err != nil {
		t.Fatalf("replay should preserve suspension: %v", err)
	}
	// A fourth verified move provides a NEW three-action checkpoint. The
	// original successful undo remains durable; reconfirmation cannot reuse it.
	journal.actions = append(journal.actions, janitorKnowledgeTestAction("four", at.Add(-30*time.Minute)))
	evidence, err := authority.FreshEvidence(ctx, personalassistant.KnowledgeBinding{
		UserID: "local", HQWorkspaceID: state.HQWorkspaceID,
	}, persisted.Items[0])
	if err != nil || len(evidence) != 3 {
		t.Fatalf("new checkpoint unavailable: %+v %v", evidence, err)
	}
	checkpoint := personalassistant.KnowledgeEvidenceCheckpoint(evidence)
	if _, err := learning.Reconfirm(ctx, "local", approved.ID, persisted.Items[0].Version, "reconfirm-janitor",
		persisted.Items[0].CurrentRevisionID, "stale-checkpoint", approved.Revisions[0].Text); err == nil {
		t.Fatal("reconfirmation accepted a stale/unknown evidence checkpoint")
	}
	reconfirmed, err := learning.Reconfirm(ctx, "local", approved.ID, persisted.Items[0].Version, "reconfirm-janitor",
		persisted.Items[0].CurrentRevisionID, checkpoint, approved.Revisions[0].Text)
	if err != nil || reconfirmed.State != personalassistant.KnowledgeApproved {
		t.Fatalf("reconfirm fresh evidence: %+v %v", reconfirmed, err)
	}
	if _, err := learning.Reconfirm(ctx, "local", approved.ID, persisted.Items[0].Version, "reconfirm-janitor",
		persisted.Items[0].CurrentRevisionID, checkpoint, approved.Revisions[0].Text); err != nil {
		t.Fatalf("lost-response retry: %v", err)
	}
	if err := recovery.Reconcile(ctx, "local"); err != nil {
		t.Fatalf("old undone evidence suspended new checkpoint: %v", err)
	}
	section, err = readerContext.HomeSection(ctx, "local", state.HQWorkspaceID)
	if err != nil || strings.Count(section, "filing documents") != 1 {
		t.Fatalf("reconfirmed current revision missing or repeated: %q %v", section, err)
	}
	journal.actions[1].Undo = filejanitor.UndoDone
	journal.actions[1].UndoneAt = at
	if section, err := readerContext.HomeSection(ctx, "local", state.HQWorkspaceID); err != nil || section != "" {
		t.Fatalf("new contradiction visible before notification: %q %v", section, err)
	}
	if err := recovery.Reconcile(ctx, "local"); err != nil {
		t.Fatalf("second contradiction: %v", err)
	}
	pendingForget, err := knowledge.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	forgotten, err := learning.ForgetItem(ctx, "local", approved.ID, pendingForget.Items[0].Version, "forget-suspended-janitor")
	if err != nil || forgotten.State != personalassistant.KnowledgeForgotten {
		t.Fatalf("cannot forget suspended fact: %+v %v", forgotten, err)
	}
	if _, err := learning.ForgetItem(ctx, "local", approved.ID, pendingForget.Items[0].Version, "forget-suspended-janitor"); err != nil {
		t.Fatalf("lost Forget response could not replay: %v", err)
	}
	journal.actions = append(journal.actions,
		janitorKnowledgeTestAction("five", at.Add(-20*time.Minute)),
		janitorKnowledgeTestAction("six", at.Add(-10*time.Minute)))
	restartedKnowledge := personalassistant.NewKnowledgeStore(bindings, builder.workspaceFileStore)
	restartedLearning := personalassistant.NewKnowledgeLifecycleService(restartedKnowledge, memory, authority)
	restartedProducer := janitorKnowledgeProducer{reader: reader, learning: restartedLearning}
	if results, err := restartedProducer.Check(ctx, "local"); err != nil || len(results) != 0 {
		t.Fatalf("forgotten meaning re-proposed after restart: %+v %v", results, err)
	}
	restarted, err := restartedKnowledge.Read(ctx, "local")
	if err != nil || len(restarted.Items) != 1 || len(restarted.Items[0].Revisions) != 0 {
		t.Fatalf("forgotten sidecar retained history: %+v %v", restarted.Items, err)
	}
	serialized, err := json.Marshal(restarted)
	if err != nil || strings.Contains(string(serialized), "filing documents") {
		t.Fatalf("old Janitor plaintext retained after Forget: %s %v", serialized, err)
	}
	janitorWS.OwnerUserID = "foreign"
	journal.reads = 0
	if _, err := reader.ReadFresh(ctx, "local"); err == nil || journal.reads != 0 {
		t.Fatalf("foreign workspace journal read: %v reads=%d", err, journal.reads)
	}
	janitorWS.OwnerUserID = "local"
	cfg.SelectedWorkspaceIDs = []string{"not-janitor-ws"}
	journal.reads = 0
	if _, err := reader.ReadFresh(ctx, "local"); err == nil || journal.reads != 0 {
		t.Fatalf("scope-excluded journal read: %v reads=%d", err, journal.reads)
	}
	cfg.SelectedWorkspaceIDs = []string{janitorWS.ID}
	janitorWS.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: filejanitor.LegacyTemplateID, Builtin: false})
	journal.reads = 0
	if _, err := reader.ReadFresh(ctx, "local"); err != nil || journal.reads != 0 {
		t.Fatalf("lookalike plugin/unverified source was read: %v reads=%d", err, journal.reads)
	}
}
