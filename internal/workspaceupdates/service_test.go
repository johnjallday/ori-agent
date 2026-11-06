package workspaceupdates

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/resetstate"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type recoveryFixture struct {
	t        *testing.T
	root     string
	sessions session.HybridStore
	files    *workspace.FileStore
	folders  *workspace.SyncStore
	service  *Service
	calls    atomic.Int32
	assessed atomic.Bool
}

func newRecoveryFixture(t *testing.T) *recoveryFixture {
	t.Helper()
	f := &recoveryFixture{t: t, root: t.TempDir()}
	t.Setenv("WORKSPACE_DIR", filepath.Join(f.root, "workspaces"))
	f.open()
	t.Cleanup(f.close)
	return f
}

func (f *recoveryFixture) open() {
	f.t.Helper()
	var err error
	f.sessions, err = session.NewHybridStore(f.t.Context(), &session.HybridStoreConfig{DatabasePath: filepath.Join(f.root, "sessions.db"), CacheSize: 5})
	if err != nil {
		f.t.Fatal(err)
	}
	f.files, err = workspace.NewFileStore(filepath.Join(f.root, "workspace-index"))
	if err != nil {
		f.t.Fatal(err)
	}
	primary := session.NewWorkspaceStoreAdapter(f.sessions)
	primary.SetWorkspaceAgentStore(f.files)
	f.folders = workspace.NewSyncStore(primary, f.files)
	composer := composerFunc(func(_ context.Context, owner, id string) (session.LeadConversationInput, string, error) {
		f.calls.Add(1)
		ws, err := ReadWorkspace(f.folders, id)
		if err != nil {
			return session.LeadConversationInput{}, "", err
		}
		scope, eligible := Scope(ws, owner)
		if !eligible {
			return session.LeadConversationInput{}, StateUnavailable, nil
		}
		assessment := ""
		if f.assessed.Load() {
			assessment = "An already saved assessment."
		}
		return session.LeadConversationInput{WorkspaceID: id, OwnerUserID: owner, AgentInstanceID: scope.AgentInstanceID, AgentName: scope.AgentName, WorkspaceVersion: ws.Version, Introduction: "Welcome", Assessment: assessment}, StateReady, nil
	})
	f.service = New(f.sessions.(Store), f.folders, composer, &resetstate.WorkGate{})
}

func (f *recoveryFixture) close() {
	f.t.Helper()
	if f.service != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := f.service.Stop(ctx); err != nil {
			f.t.Error(err)
		}
	}
	if f.files != nil {
		if err := f.files.Close(); err != nil {
			f.t.Error(err)
		}
		f.files = nil
	}
	if f.sessions != nil {
		if err := f.sessions.Close(); err != nil {
			f.t.Error(err)
		}
		f.sessions = nil
	}
}

func (f *recoveryFixture) create(wizard bool) {
	f.t.Helper()
	ws := &workspace.Workspace{ID: "ws", Name: "Desk", Status: workspace.StatusActive, OwnerUserID: "local", AgentInstances: []workspace.AgentInstance{{ID: "mira", Name: "Mira", EntryPoint: true}}}
	if wizard {
		ws.SetTemplateProvenance(&workspace.TemplateProvenance{TemplateID: "synthetic", SetupWizard: &workspace.SetupWizard{Version: 1, Steps: []workspace.SetupWizardStep{{ID: "ack", Kind: workspace.SetupStepKindSummary, Required: true}}}})
	}
	if err := f.folders.Save(ws); err != nil {
		f.t.Fatal(err)
	}
	if err := f.service.Enroll(f.t.Context(), "local", ws.ID); err != nil {
		f.t.Fatal(err)
	}
}

func (f *recoveryFixture) read() session.WelcomeDelivery {
	f.t.Helper()
	ws, err := ReadWorkspace(f.folders, "ws")
	if err != nil {
		f.t.Fatal(err)
	}
	scope, ok := Scope(ws, "local")
	if !ok {
		f.t.Fatal("no live scope")
	}
	got, err := f.sessions.(Store).ReadWelcome(f.t.Context(), scope)
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

func TestWelcomeRecoveryUsesPersistedReadinessAfterMissedHookAndRestart(t *testing.T) {
	f := newRecoveryFixture(t)
	f.create(true)
	if _, err := f.service.Reconcile(t.Context(), 0); err != nil || f.calls.Load() != 0 {
		t.Fatalf("recovery ignored folder-owned setup gate: calls=%d err=%v", f.calls.Load(), err)
	}
	// The only readiness write belongs to the fixture's explicit setup. There
	// is no wizard adapter available to the recovery service to rerun it.
	if err := f.folders.Update("ws", func(ws *workspace.Workspace) error {
		now := time.Now().UTC()
		ws.SetSetupWizardProgress(&workspace.SetupWizardProgress{WizardVersion: 1, State: workspace.SetupWizardStateReady, CompletedAt: &now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Simulate exit after readiness persistence but before the callback hint.
	f.close()
	f.open()
	if _, err := f.service.Reconcile(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	first := f.read()
	if first.Unread != 1 || len(first.Messages) != 1 {
		t.Fatalf("missed delivery: %+v", first)
	}
	// Exit after message/identity commit and before any UI/event publication.
	f.close()
	f.open()
	if _, err := f.service.Reconcile(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	again := f.read()
	if again.SessionID != first.SessionID || again.Unread != 1 || len(again.Messages) != 1 || again.Messages[0].MessageID != first.Messages[0].MessageID {
		t.Fatalf("restart duplicated or lost saved truth: %+v", again)
	}
}

func TestWelcomeRecoveryNoWizardAllowsFolderOnlyScaffoldingVersion(t *testing.T) {
	f := newRecoveryFixture(t)
	f.create(false)
	// The ordinary creator writes scaffolding directly to the folder mirror.
	// With no goal/template mutation, SQLite can legitimately stay at version 0.
	if err := f.files.Update("ws", func(ws *workspace.Workspace) error {
		ws.SetSharedData("synthetic_scaffolding", true)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	primary, err := f.folders.Get("ws")
	if err != nil {
		t.Fatal(err)
	}
	folder, err := f.files.Get("ws")
	if err != nil || folder.Version == primary.Version {
		t.Fatal("fixture did not reproduce independent folder version", err)
	}
	if err := f.service.Enroll(t.Context(), "local", "ws"); err != nil {
		t.Fatal("ordinary completed creation refused enrollment", err)
	}
	if _, err := f.service.Reconcile(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	if got := f.read(); got.Unread != 1 {
		t.Fatal("no-wizard welcome lost", got)
	}
}

func TestWelcomeRecoveryNoWizardCommitFailureAndDelayedAssessment(t *testing.T) {
	f := newRecoveryFixture(t)
	f.create(false)
	if _, err := f.sessions.DB().ExecContext(t.Context(), `CREATE TRIGGER fail_update BEFORE INSERT ON workspace_welcome_messages BEGIN SELECT RAISE(ABORT,'injected crash boundary'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Reconcile(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.sessions.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed update left a phantom conversation", count, err)
	}
	if _, err := f.sessions.DB().ExecContext(t.Context(), `DROP TRIGGER fail_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Reconcile(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	first := f.read()
	if first.Unread != 1 || len(first.Messages) != 1 {
		t.Fatal("no-wizard welcome not recovered", first)
	}
	f.assessed.Store(true)
	if _, err := f.service.Reconcile(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	latest := f.read()
	if latest.SessionID != first.SessionID || latest.Unread != 2 || len(latest.Messages) != 2 {
		t.Fatal("saved later assessment not delivered", latest)
	}
	pending, err := f.sessions.(Store).PendingWelcomes(t.Context(), 0, 100)
	if err != nil || len(pending) != 0 {
		t.Fatal("completed enrollment kept retrying", pending, err)
	}
	before := f.calls.Load()
	if _, err := f.service.Reconcile(t.Context(), 0); err != nil || f.calls.Load() != before {
		t.Fatal("recovery revisited completed delivery", err)
	}
}

func TestWelcomeRecoveryDoesNotEnrollLegacyOrTransferToReplacement(t *testing.T) {
	f := newRecoveryFixture(t)
	f.create(false)
	// A replacement can arrive before any welcome has been written.
	if err := f.folders.Update("ws", func(ws *workspace.Workspace) error {
		ws.AgentInstances[0].ID = "replacement"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Reconcile(t.Context(), 0); err != nil || f.calls.Load() != 0 {
		t.Fatal("recovery adopted replacement", err)
	}
	// Nor can the explicit fallback bypass that retirement, even if the old
	// instance is later put back. The original enrollment stays settled.
	if err := f.folders.Update("ws", func(ws *workspace.Workspace) error {
		ws.AgentInstances[0].ID = "mira"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	out, err := Deliver(t.Context(), f.service.composer, f.service.store, "local", "ws")
	if err != nil || out.Changed || out.SessionID != "" {
		t.Fatal("retired enrollment resurrected", out, err)
	}
	legacy := &workspace.Workspace{ID: "legacy", Name: "Old", Status: workspace.StatusActive, OwnerUserID: "local", AgentInstances: []workspace.AgentInstance{{ID: "lead-old", Name: "Mira", EntryPoint: true}}}
	if err := f.folders.Save(legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Reconcile(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.sessions.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil || count != 0 {
		t.Fatal("legacy workspace was mass-seeded", count, err)
	}
}

func TestWelcomeWorkerAdmissionAndRetryableStop(t *testing.T) {
	f := newRecoveryFixture(t)
	f.create(false)
	entered, resume := make(chan struct{}), make(chan struct{})
	original := f.service.composer
	f.service.composer = composerFunc(func(ctx context.Context, owner, id string) (session.LeadConversationInput, string, error) {
		close(entered)
		<-resume // deliberately uncooperative dependency to test drain refusal
		return original.ComposeLeadWelcome(ctx, owner, id)
	})
	f.service.Start()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not start")
	}
	gate := f.service.gate
	if snapshot := gate.Snapshot(); snapshot.Active != 1 || snapshot.Owners != 1 {
		t.Fatalf("untracked recovery: %+v", snapshot)
	}
	if err := gate.TryFence(t.Context()); !errors.Is(err, resetstate.ErrWorkActive) {
		t.Fatal("reset interrupted active recovery", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if err := f.service.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) || gate.Snapshot().Owners != 1 {
		t.Fatal("ambiguous stop reported drained", err)
	}
	close(resume)
	if err := f.service.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if snapshot := gate.Snapshot(); snapshot.Active != 0 || snapshot.Owners != 0 {
		t.Fatal("owner was not joined", snapshot)
	}
	if err := gate.TryFence(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.service.Start()
	if _, err := f.service.Reconcile(t.Context(), 0); !errors.Is(err, resetstate.ErrWorkFenced) || gate.Snapshot().Owners != 0 {
		t.Fatal("recovery bypassed reset", err)
	}
}
