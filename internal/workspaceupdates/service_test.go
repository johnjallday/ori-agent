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
	// held makes the composer answer "waiting" as it does for a workspace whose
	// folder agent is still asking its own question.
	held atomic.Bool
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
		if f.held.Load() {
			return session.LeadConversationInput{}, StateWaitingSetup, nil
		}
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

func TestWelcomeReconcileReportsProgressOnlyWhenSomethingChanged(t *testing.T) {
	f := newRecoveryFixture(t)
	f.create(false)
	progressed := func() bool {
		t.Helper()
		_, did, err := f.service.reconcile(t.Context(), 0)
		if err != nil {
			t.Fatal(err)
		}
		return did
	}
	if !progressed() {
		t.Fatal("writing the introduction was not progress")
	}
	// Introduction delivered, no assessment yet: the pass revisits the workspace
	// and finds nothing to change. This is the state that used to repeat forever.
	if progressed() {
		t.Fatal("an unchanged awaiting enrollment counted as progress")
	}
	f.assessed.Store(true)
	if !progressed() {
		t.Fatal("delivering the assessment was not progress")
	}
	// Nothing pending: an idle service must not report progress either.
	if progressed() {
		t.Fatal("an empty keyset counted as progress")
	}
}

// While the composer says "waiting" (the folder agent is asking its own
// question) a pass writes no message, settles nothing and reports no progress,
// so the worker backs off instead of spinning, and the enrolment is still there
// for the pass that follows the release.
func TestHeldWelcomeStaysPendingAndIsDeliveredOnceReleased(t *testing.T) {
	f := newRecoveryFixture(t)
	f.held.Store(true)
	f.create(false)

	pending := func() int {
		t.Helper()
		items, err := f.sessions.(Store).PendingWelcomes(t.Context(), 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		return len(items)
	}
	for i := range 3 {
		_, did, err := f.service.reconcile(t.Context(), 0)
		if err != nil || did {
			t.Fatalf("held pass %d: progress=%v err=%v, want none", i, did, err)
		}
	}
	if got := f.read(); len(got.Messages) != 0 || got.SessionID != "" {
		t.Fatalf("a held welcome was written: %+v", got)
	}
	if pending() != 1 {
		t.Fatalf("the held enrolment is not pending: %d", pending())
	}

	f.held.Store(false)
	if _, did, err := f.service.reconcile(t.Context(), 0); err != nil || !did {
		t.Fatalf("released pass: progress=%v err=%v, want progress", did, err)
	}
	if got := f.read(); len(got.Messages) != 1 {
		t.Fatalf("the released welcome was not delivered: %+v", got)
	}
}

// With the worker's interval far in the future, only a wake can bring the next
// pass: releasing the hold and calling Notify delivers the welcome at once, so
// setting up or keeping a folder does not leave the welcome minutes late.
func TestHeldWelcomeIsDeliveredOnNotifyNotOnTheTimer(t *testing.T) {
	f := newRecoveryFixture(t)
	f.service.interval = time.Hour
	f.service.maxInterval = time.Hour
	f.held.Store(true)
	f.create(false)
	f.service.Start()

	deadline := time.Now().Add(3 * time.Second)
	for f.calls.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.calls.Load() < 1 {
		t.Fatal("the worker never ran its first pass")
	}
	if got := f.read(); len(got.Messages) != 0 {
		t.Fatalf("delivered while held: %+v", got)
	}

	f.held.Store(false)
	f.service.Notify()
	for len(f.read().Messages) == 0 && time.Now().Before(deadline.Add(3*time.Second)) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := f.read(); len(got.Messages) != 1 {
		t.Fatalf("Notify did not deliver the released welcome: %+v", got)
	}
}

func TestWelcomeWorkerBacksOffWhileIdleAndRecoversOnProgress(t *testing.T) {
	f := newRecoveryFixture(t)
	s := f.service
	wait := s.interval
	var seen []time.Duration
	for range 8 {
		wait = s.nextWait(wait, false)
		seen = append(seen, wait)
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] < seen[i-1] || seen[i] > s.maxInterval {
			t.Fatalf("backoff not monotonic and capped: %v", seen)
		}
	}
	if seen[len(seen)-1] != s.maxInterval {
		t.Fatalf("idle backoff never reached its cap %s: %v", s.maxInterval, seen)
	}
	if got := s.nextWait(wait, true); got != s.interval {
		t.Fatalf("progress left the wait at %s, want the base %s", got, s.interval)
	}
	// A configured cap below the base cadence must never speed the worker up.
	s.maxInterval = time.Second
	if got := s.nextWait(s.interval, false); got != s.interval {
		t.Fatalf("cap below base interval sped the worker up to %s", got)
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
