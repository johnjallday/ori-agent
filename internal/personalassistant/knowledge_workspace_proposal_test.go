package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func newWorkspaceProposalService(t *testing.T) (*knowledgeFixture, *KnowledgeLearningService, *time.Time) {
	t.Helper()
	f := newKnowledgeFixture(t)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	svc := NewKnowledgeLifecycleService(NewKnowledgeStore(f.resolver(), f.folder), f.memory, nil)
	svc.now = func() time.Time { return now }
	return f, svc, &now
}

func workspaceFact(workspaceID, text string) WorkspaceFactRequest {
	return WorkspaceFactRequest{
		WorkspaceID: workspaceID, WorkspaceName: "Album 3", Agent: "Scout",
		TaskID: "task-42", Category: "how_you_work", Text: text,
	}
}

func TestProposeWorkspaceFact_CreatesAttributedCandidateWithoutWritingMemory(t *testing.T) {
	f, svc, now := newWorkspaceProposalService(t)
	ctx := context.Background()
	result, item, err := svc.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-album", "John delivers stems at 48 kHz."))
	if err != nil || result != WorkspaceFactProposed {
		t.Fatalf("propose = %q %v", result, err)
	}
	if item.State != KnowledgeCandidate || item.SourceKind != WorkspaceAgentSourceKind || item.Category != "how_you_work" {
		t.Fatalf("candidate = %+v", item)
	}
	if item.SemanticKey != knowledgeTextKey("John delivers stems at 48 kHz.") {
		t.Fatalf("semantic key is not the text key: %q", item.SemanticKey)
	}
	evidence := item.Revisions[0].Evidence
	want := KnowledgeEvidence{
		SourceKind: WorkspaceAgentSourceKind, SourceID: "ws-album", ObservedAt: *now,
		Summary: "Album 3 · Scout · task task-42",
	}
	if len(evidence) != 1 || evidence[0] != want {
		t.Fatalf("evidence = %+v, want %+v", evidence, want)
	}
	origin, ok := WorkspaceProposalOriginOf(evidence)
	if !ok || origin != (WorkspaceProposalOrigin{WorkspaceID: "ws-album", WorkspaceName: "Album 3", Agent: "Scout", TaskID: "task-42", ObservedAt: *now}) {
		t.Fatalf("origin = %+v %v", origin, ok)
	}
	// Proposing is review-only: the HQ MEMORY.md is untouched.
	if snapshot, err := f.memory.SnapshotExact("hq-local"); err != nil || len(snapshot.Entries) != 0 {
		t.Fatalf("proposal wrote canonical memory: %+v %v", snapshot.Entries, err)
	}

	chat := workspaceFact("ws-album", "John prefers dark mode.")
	chat.TaskID = ""
	_, item, err = svc.ProposeWorkspaceFact(ctx, "local", chat)
	if err != nil || item.Revisions[0].Evidence[0].Summary != "Album 3 · Scout · chat" {
		t.Fatalf("chat proposal evidence = %+v %v", item.Revisions, err)
	}
	if origin, _ := WorkspaceProposalOriginOf(item.Revisions[0].Evidence); origin.TaskID != "" || origin.Agent != "Scout" {
		t.Fatalf("chat origin = %+v", origin)
	}
}

func TestWorkspaceProposalOrigin_RoundTripsAwkwardLabels(t *testing.T) {
	cases := []struct {
		name, workspace, agent, task string
		want                         WorkspaceProposalOrigin
	}{
		{"separator in workspace", "Mix · Master", "Scout", "", WorkspaceProposalOrigin{WorkspaceName: "Mix - Master", Agent: "Scout"}},
		{"separator in agent", "Album", "A · B", "7", WorkspaceProposalOrigin{WorkspaceName: "Album", Agent: "A - B", TaskID: "7"}},
		{"unsafe workspace name", "Album‮3", "Scout", "7", WorkspaceProposalOrigin{Agent: "Scout", TaskID: "7"}},
		{"no agent", "Album", "", "", WorkspaceProposalOrigin{WorkspaceName: "Album", Agent: workspaceOriginNoAgent}},
		{"over-long workspace name", strings.Repeat("w", workspaceOriginMaxLabel+1), "Scout", "", WorkspaceProposalOrigin{Agent: "Scout"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			summary := workspaceOriginSummary(tc.workspace, tc.agent, tc.task)
			got, ok := WorkspaceProposalOriginOf([]KnowledgeEvidence{{SourceKind: WorkspaceAgentSourceKind, SourceID: "ws", Summary: summary}})
			tc.want.WorkspaceID = "ws"
			if !ok || got != tc.want {
				t.Fatalf("summary %q parsed to %+v, want %+v", summary, got, tc.want)
			}
		})
	}
	if _, ok := WorkspaceProposalOriginOf([]KnowledgeEvidence{{SourceKind: "saved_app", SourceID: "x", Summary: "a · b · chat"}}); ok {
		t.Fatal("another source's evidence parsed as a workspace origin")
	}
}

func TestProposeWorkspaceFact_EvidenceHoldsNoPathOrSecret(t *testing.T) {
	f, svc, _ := newWorkspaceProposalService(t)
	ctx := context.Background()
	req := workspaceFact("ws-album", "John mixes in Ableton.")
	req.Agent = "token sk-abc1234567890def"
	req.WorkspaceName = "Album\u202e3"
	if _, _, err := svc.ProposeWorkspaceFact(ctx, "local", req); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(f.folder.path, ".ori", knowledgeFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"sk-abc1234567890def", "\u202e", f.folder.path} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("sidecar holds %q:\n%s", leaked, raw)
		}
	}
	secret := workspaceFact("ws-album", "the api key is sk-abc1234567890def")
	if _, _, err := svc.ProposeWorkspaceFact(ctx, "local", secret); !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), "Vault") {
		t.Fatalf("secret-looking text = %v, want a validation error naming the Vault", err)
	}
}

func TestProposeWorkspaceFact_AcceptsTheFiveDossierCategoriesOnly(t *testing.T) {
	_, svc, _ := newWorkspaceProposalService(t)
	ctx := context.Background()
	for i, category := range []string{"how_you_work", "people", "projects", "routines", "sources"} {
		req := workspaceFact("ws-album", fmt.Sprintf("Fact number %d about John.", i))
		req.Category = category
		if result, item, err := svc.ProposeWorkspaceFact(ctx, "local", req); err != nil || result != WorkspaceFactProposed || item.Category != category {
			t.Fatalf("category %s = %q %+v %v", category, result, item, err)
		}
	}
	for _, category := range []string{"", "you", "secrets", "HOW_YOU_WORK"} {
		req := workspaceFact("ws-album", "A fact with a bad category.")
		req.Category = category
		if _, _, err := svc.ProposeWorkspaceFact(ctx, "local", req); !errors.Is(err, ErrValidation) {
			t.Fatalf("category %q = %v, want ErrValidation", category, err)
		}
	}
}

func TestProposeWorkspaceFact_DeduplicatesAcrossWorkspacesAndAgainstGlobalMemory(t *testing.T) {
	f, svc, _ := newWorkspaceProposalService(t)
	ctx := context.Background()
	_, first, err := svc.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-album", "John delivers stems at 48 kHz."))
	if err != nil {
		t.Fatal(err)
	}
	// Another workspace, another case and spacing: still the same fact.
	result, again, err := svc.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-ep", "john  delivers STEMS at 48 kHz."))
	if err != nil || result != WorkspaceFactAlreadyPending || again.ID != first.ID {
		t.Fatalf("cross-workspace duplicate = %q %+v %v", result, again, err)
	}

	binding, err := svc.store.resolve(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveExplicit(ctx, "local", binding.StateVersion, "explicit-tempo", "how_you_work", "John writes at 92 BPM."); err != nil {
		t.Fatal(err)
	}
	if result, _, err := svc.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-album", "John writes at 92 BPM.")); err != nil || result != WorkspaceFactAlreadyKnown {
		t.Fatalf("approved global fact = %q %v, want already known", result, err)
	}
	// A line the assistant wrote inside HQ is global memory too.
	if err := f.memory.Append("hq-local", workspace.MemoryEntry{Type: workspace.MemoryTypeFact, Date: "2026-10-17", Provenance: "agent:Ada", Text: "John lives in Seoul."}); err != nil {
		t.Fatal(err)
	}
	if result, _, err := svc.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-album", "John lives in Seoul.")); err != nil || result != WorkspaceFactAlreadyKnown {
		t.Fatalf("HQ memory line = %q %v, want already known", result, err)
	}
	doc, err := svc.store.Read(ctx, "local")
	if err != nil || len(doc.Items) != 2 || len(doc.Admissions) != 0 {
		t.Fatalf("duplicates created items or consumed the shared quota: %+v %v", doc, err)
	}
}

func TestProposeWorkspaceFact_RejectedOrForgottenTextIsSuppressedFromAnySource(t *testing.T) {
	_, svc, _ := newWorkspaceProposalService(t)
	ctx := context.Background()
	_, candidate, err := svc.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-album", "John hates reverb."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SuppressCandidate(ctx, "local", candidate.ID, candidate.Version, KnowledgeRejected); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-ep", "John hates reverb.")); !errors.Is(err, ErrKnowledgeSuppressed) {
		t.Fatalf("rejected text re-proposed from another workspace: %v", err)
	}

	binding, err := svc.store.resolve(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := svc.SaveExplicit(ctx, "local", binding.StateVersion, "explicit-forget", "people", "John's manager is Mina.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ForgetItem(ctx, "local", explicit.ID, explicit.Version, "forget-mina"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-album", "John's manager is Mina.")); !errors.Is(err, ErrKnowledgeSuppressed) {
		t.Fatalf("forgotten text re-proposed: %v", err)
	}

	// A rejected saved-app suggestion's wording is suppressed for workspaces too.
	saved, _, err := svc.Propose(ctx, "local", testProposal("ableton", "Maybe you use Ableton for mixing."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SuppressCandidate(ctx, "local", saved.ID, saved.Version, KnowledgeRejected); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-album", "Maybe you use Ableton for mixing.")); !errors.Is(err, ErrKnowledgeSuppressed) {
		t.Fatalf("text rejected under another source re-proposed: %v", err)
	}
}

func TestProposeWorkspaceFact_PausedAndMissingAssistantRefuse(t *testing.T) {
	f, svc, _ := newWorkspaceProposalService(t)
	ctx := context.Background()
	if _, _, err := svc.ProposeWorkspaceFact(ctx, "someone-else", workspaceFact("ws-album", "A fact.")); !errors.Is(err, ErrGlobalMemoryUnavailable) {
		t.Fatalf("no hired assistant = %v, want ErrGlobalMemoryUnavailable", err)
	}
	state, err := f.relationships.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	state.Status = StatusPaused
	if _, err := f.relationships.UpdateState(ctx, state, state.StateVersion); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-album", "A fact.")); !errors.Is(err, ErrKnowledgePaused) {
		t.Fatalf("paused assistant = %v, want ErrKnowledgePaused", err)
	}
	var nilService *KnowledgeLearningService
	if _, _, err := nilService.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-album", "A fact.")); !errors.Is(err, ErrGlobalMemoryUnavailable) {
		t.Fatalf("unwired service = %v", err)
	}
}

func TestProposeWorkspaceFact_QuotaIsSeparateFromOtherSources(t *testing.T) {
	_, svc, now := newWorkspaceProposalService(t)
	ctx := context.Background()
	// Exhaust the shared daily pool first: it must not block a workspace.
	for _, id := range []string{"app-a", "app-b", "app-c"} {
		if _, created, err := svc.Propose(ctx, "local", testProposal(id, "Work-style hypothesis for "+id)); err != nil || !created {
			t.Fatalf("admit %s: %v", id, err)
		}
	}
	var proposed []KnowledgeItem
	for i := range workspaceProposalPendingLimit {
		_, item, err := svc.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-album", fmt.Sprintf("Workspace fact %02d.", i)))
		if err != nil {
			t.Fatalf("workspace proposal %d: %v", i, err)
		}
		proposed = append(proposed, item)
	}
	if _, _, err := svc.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-album", "Workspace fact 11.")); !errors.Is(err, ErrKnowledgeQuota) {
		t.Fatalf("11th pending workspace proposal = %v, want ErrKnowledgeQuota", err)
	}
	// The next day the shared pool is open again even though the workspace
	// pool is full of pending items.
	*now = now.Add(24 * time.Hour)
	if _, created, err := svc.Propose(ctx, "local", testProposal("app-d", "Work-style hypothesis for app-d")); err != nil || !created {
		t.Fatalf("shared pool blocked by workspace proposals: %v", err)
	}
	// Clearing pending items reopens the pending cap but not the daily cap.
	*now = now.Add(-24 * time.Hour).Add(time.Hour)
	for _, item := range proposed[:2] {
		if _, err := svc.SuppressCandidate(ctx, "local", item.ID, item.Version, KnowledgeRejected); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := svc.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-album", "Workspace fact 11.")); !errors.Is(err, ErrKnowledgeQuota) {
		t.Fatalf("11th workspace proposal in 24 hours = %v, want ErrKnowledgeQuota", err)
	}
	*now = now.Add(24 * time.Hour)
	if result, _, err := svc.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-album", "Workspace fact 11.")); err != nil || result != WorkspaceFactProposed {
		t.Fatalf("after the 24-hour window = %q %v", result, err)
	}
	doc, err := svc.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Admissions) != 1 {
		t.Fatalf("workspace proposals entered the shared admissions: %v", doc.Admissions)
	}
}

func TestReviewItemsAttributeEverySourceAndWorkspaceProposals(t *testing.T) {
	_, svc, now := newWorkspaceProposalService(t)
	ctx := context.Background()
	if _, _, err := svc.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-album", "John delivers stems at 48 kHz.")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Propose(ctx, "local", testProposal("ableton", "Maybe you use Ableton.")); err != nil {
		t.Fatal(err)
	}
	items, err := svc.ReviewItems(ctx, "local")
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %+v %v", items, err)
	}
	for _, item := range items {
		switch item.Source.Kind {
		case WorkspaceAgentSourceKind:
			want := KnowledgeReviewSource{Kind: WorkspaceAgentSourceKind, WorkspaceID: "ws-album", WorkspaceName: "Album 3", Agent: "Scout", TaskID: "task-42"}
			observed := item.Source.ObservedAt
			item.Source.ObservedAt = nil
			if item.Source != want || observed == nil || !observed.Equal(*now) {
				t.Fatalf("workspace source = %+v observed=%v", item.Source, observed)
			}
		case "saved_app":
			if item.Source != (KnowledgeReviewSource{Kind: "saved_app"}) {
				t.Fatalf("saved-app source = %+v", item.Source)
			}
		default:
			t.Fatalf("unexpected source %+v", item.Source)
		}
	}
}

func TestReviewItemsLabelInterviewFactsApartFromFactsYouTold(t *testing.T) {
	ctx := context.Background()
	f, interview, version := interviewSaveFixture(t)
	if _, err := interview.SaveReviewed(ctx, "local", KnowledgeInterviewSaveRequest{
		StateVersion: version, RequestID: "interview-chip",
		Rows: []KnowledgeInterviewReviewedRow{{RowID: "priority", Category: "projects", Destination: "personal_hq", Text: "Finish my portfolio"}},
	}); err != nil {
		t.Fatal(err)
	}
	lifecycle := NewKnowledgeLifecycleService(NewKnowledgeStore(f.resolver(), f.folder), f.memory, nil)
	current, err := lifecycle.CurrentStateVersion(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.SaveExplicit(ctx, "local", current, "told-directly", "people", "My manager is Mina."); err != nil {
		t.Fatal(err)
	}
	items, err := lifecycle.ReviewItems(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, item := range items {
		kinds[item.Text] = item.Source.Kind
	}
	if kinds["Finish my portfolio"] != KnowledgeInterviewSourceKind || kinds["My manager is Mina."] != "explicit" {
		t.Fatalf("source kinds = %v", kinds)
	}
}

// The sidecar is decoded with DisallowUnknownFields, so a field this feature
// added would make an older Ori read a newer file as corrupt. Workspace
// proposals must persist only fields the pre-feature schema already knows.
func TestWorkspaceProposalSidecarKeepsThePreFeatureSchema(t *testing.T) {
	f, svc, _ := newWorkspaceProposalService(t)
	ctx := context.Background()
	if _, _, err := svc.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-album", "John records vocals at night.")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(f.folder.path, ".ori", knowledgeFileName))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Fields map[string]json.RawMessage
		Items  []struct {
			Revisions []struct {
				Evidence []map[string]json.RawMessage `json:"evidence"`
			} `json:"revisions"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &doc.Fields); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{"schema_version": true, "version": true, "owner": true, "items": true, "tombstones": true, "receipts": true, "admissions": true, "interview": true}
	for field := range doc.Fields {
		if !known[field] {
			t.Errorf("new top-level sidecar field %q", field)
		}
	}
	evidenceKnown := map[string]bool{"source_kind": true, "source_id": true, "observed_at": true, "summary": true}
	for _, item := range doc.Items {
		for _, revision := range item.Revisions {
			for _, evidence := range revision.Evidence {
				for field := range evidence {
					if !evidenceKnown[field] {
						t.Errorf("new evidence field %q", field)
					}
				}
			}
		}
	}
}
