package downloadsjanitor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// TestGoldenPath_WorksWithNoAgentInTheWorkspace is the FR-92 / Success-Metric-11
// guarantee: File Janitor is a service, not an agent feature.
//
// The workspace here has no agent instances at all — no Curator, no manager, no
// entry agent — and there is no chat session, mission, or task runner anywhere
// in the fixture. The whole golden path still has to work: watch registration,
// scan, review, approval, apply, journal, and undo.
//
// This is the test that would fail if any step were quietly routed through an
// agent prompt or a task the user's team has to execute.
func TestGoldenPath_WorksWithNoAgentInTheWorkspace(t *testing.T) {
	store, _ := newTestStore(t)
	workspaces := newFakeWorkspaceStore("ws-1")
	service := NewService(store, workspaces)
	service.SetMover(&realMover{})
	trash := newFakeTrash(t)
	service.SetTrash(trash)
	triggers := newFakeTriggers()
	automation := NewAutomation(service, triggers)
	service.SetAutomationStatus(automation)

	// Precondition: this workspace has no agents whatsoever.
	ws, err := workspaces.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(ws.AgentInstances) != 0 {
		t.Fatalf("precondition: the fixture workspace should have no agents, got %d", len(ws.AgentInstances))
	}
	if name := ws.EntryAgentName(); name != "" {
		t.Fatalf("precondition: the fixture workspace should have no entry agent, got %q", name)
	}

	root := filepath.Join(tempDirCanonical(t), "Inbox")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}

	// 1. Setup — a real folder grant, no agent involved.
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}

	// 2. Watch registration — an in-process domain trigger, not an agent task.
	if err := automation.EnsureWatcher("ws-1"); err != nil {
		t.Fatalf("EnsureWatcher: %v", err)
	}
	records, err := triggers.List("ws-1")
	if err != nil {
		t.Fatalf("List triggers: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected one watcher, got %d", len(records))
	}
	if records[0].Domain != DomainKey {
		t.Fatalf("the watcher must route to the service domain, not an agent: %q", records[0].Domain)
	}

	// 3. Scan — in-process, creating no task, mission, or chat run.
	agedFile(t, root, "invoice.pdf", 200)
	batch, created, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil {
		t.Fatalf("ScanNow: %v", err)
	}
	if !created || len(batch.CandidateIDs) != 1 {
		t.Fatalf("expected one proposed candidate, got created=%v ids=%v", created, batch.CandidateIDs)
	}
	afterScan, err := workspaces.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(afterScan.Tasks) != 0 {
		t.Fatalf("scanning created %d task(s); it must not need an agent to run", len(afterScan.Tasks))
	}
	if len(afterScan.AgentInstances) != 0 {
		t.Fatalf("scanning created %d agent(s)", len(afterScan.AgentInstances))
	}

	candidateID := batch.CandidateIDs[0]

	// 4. Review decision — the user's choice, recorded by the service.
	if _, err := service.ApplyDecisions("ws-1", []DecisionUpdate{
		{CandidateID: candidateID, Decision: DecisionMove},
	}); err != nil {
		t.Fatalf("ApplyDecisions: %v", err)
	}

	// 5. Preview + approval — issued and consumed by the compiled service.
	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1",
		UserID:      "local",
		Items:       []PreviewRequestItem{{CandidateID: candidateID, Operation: OperationMove}},
	})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}
	if preview.Token == "" {
		t.Fatal("preview issued no approval token")
	}

	// 6. Apply — the only thing that touches the filesystem.
	result, err := service.ConfirmMoves(context.Background(), ConfirmRequest{
		WorkspaceID: "ws-1",
		UserID:      "local",
		BatchID:     batch.ID,
		Token:       preview.Token,
		Items:       []PreviewRequestItem{{CandidateID: candidateID, Operation: OperationMove}},
	})
	if err != nil {
		t.Fatalf("ConfirmMoves: %v", err)
	}
	if result.Applied != 1 || result.Failed != 0 {
		t.Fatalf("apply outcome: applied=%d failed=%d stale=%d", result.Applied, result.Failed, result.Stale)
	}
	if _, err := os.Stat(filepath.Join(root, "invoice.pdf")); !os.IsNotExist(err) {
		t.Fatal("the source file is still in place after an approved move")
	}

	// 7. History — journaled by the service.
	actions, err := service.ListActions("ws-1")
	if err != nil {
		t.Fatalf("ListActions: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("expected one journal entry, got %d", len(actions))
	}

	// 8. Undo — service-owned, and it puts the file back.
	if _, err := service.Undo(context.Background(), "ws-1", actions[0].ID, "local"); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "invoice.pdf")); err != nil {
		t.Fatalf("undo did not restore the file: %v", err)
	}

	// Nothing about the whole path grew an agent or a task.
	final, err := workspaces.Get("ws-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(final.AgentInstances) != 0 || len(final.Tasks) != 0 {
		t.Fatalf("the golden path created agents (%d) or tasks (%d)", len(final.AgentInstances), len(final.Tasks))
	}
}

// TestGoldenPath_SurvivesAgentsBeingDeleted covers the other half of FR-92: a
// workspace that DID have a Curator and lost it keeps working. Deleting an
// agent must not disable scanning, approval, or undo — the agent was never in
// the path.
func TestGoldenPath_SurvivesAgentsBeingDeleted(t *testing.T) {
	store, _ := newTestStore(t)
	workspaces := newFakeWorkspaceStore("ws-1")
	service := NewService(store, workspaces)
	service.SetMover(&realMover{})
	service.SetTrash(newFakeTrash(t))

	// Give the workspace a Curator, as the blueprint would.
	if err := workspaces.Update("ws-1", func(w *workspace.Workspace) error {
		w.AgentInstances = []workspace.AgentInstance{
			{ID: "a1", Name: "Downloads Curator", NodeID: "n1", EntryPoint: true},
		}
		return nil
	}); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	root := filepath.Join(tempDirCanonical(t), "Inbox")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}

	// The user deletes every agent.
	if err := workspaces.Update("ws-1", func(w *workspace.Workspace) error {
		w.AgentInstances = nil
		return nil
	}); err != nil {
		t.Fatalf("delete agents: %v", err)
	}

	agedFile(t, root, "receipt.pdf", 150)
	batch, created, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil {
		t.Fatalf("ScanNow after agent deletion: %v", err)
	}
	if !created || len(batch.CandidateIDs) != 1 {
		t.Fatalf("scanning stopped working once the agent was gone: created=%v ids=%v", created, batch.CandidateIDs)
	}

	status, err := service.Status("ws-1")
	if err != nil {
		t.Fatalf("Status after agent deletion: %v", err)
	}
	if status.Readiness.State == ReadinessSetupRequired {
		t.Fatal("deleting an agent reset the workspace's setup")
	}
}
