package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// The notes half of a confirmed Personal HQ setup (assistant-hq-onboarding
// 3.2–3.3): every case writes into a real temporary tree and reads the files,
// the record and the callbacks back.

type countingHeadlineModel struct{ calls atomic.Int32 }

func (m *countingHeadlineModel) Complete(context.Context, string, string) (string, error) {
	m.calls.Add(1)
	return "a model headline", nil
}

type setupHarness struct {
	*secondBrainHarness
	root      string
	model     *countingHeadlineModel
	connected atomic.Int32
}

func newSetupHarness(t *testing.T) *setupHarness {
	t.Helper()
	h := &setupHarness{secondBrainHarness: newSecondBrainHarness(t), model: &countingHeadlineModel{}}
	h.root = filepath.Join(filepath.Dir(h.documents()), "Ori Workspaces")
	if err := os.MkdirAll(h.root, 0o750); err != nil {
		t.Fatal(err)
	}
	root := h.root
	h.service.deps.DefaultParent = func() string { return root }
	h.service.deps.Looker.Model = h.model
	h.service.OnConnected(func(context.Context, string, secondbrain.Source) { h.connected.Add(1) })
	return h
}

func (h *setupHarness) reviewed() string { return filepath.Join(h.root, secondbrain.DefaultFolderName) }

func (h *setupHarness) prepare(t *testing.T, opID string) {
	t.Helper()
	dest, err := h.service.ResolveSetupDestination(h.reviewed(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.service.PrepareSetup(context.Background(), "local", opID, "hq-1", dest); err != nil {
		t.Fatal(err)
	}
}

func (h *setupHarness) run(t *testing.T, opID string) SecondBrainSetupResult {
	t.Helper()
	result, err := h.service.RunSetup(context.Background(), "local", opID)
	h.service.Wait()
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (h *setupHarness) document(t *testing.T) SecondBrainDocument {
	t.Helper()
	doc, err := h.service.read(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestSetupOperationID_IsStableAndAValidNotesRequestID(t *testing.T) {
	a := SecondBrainSetupOperationID("hq-request-1")
	if a != SecondBrainSetupOperationID(" hq-request-1 ") || a == SecondBrainSetupOperationID("hq-request-2") {
		t.Fatalf("operation id is not a stable function of the HQ request: %q", a)
	}
	long := SecondBrainSetupOperationID(string(make([]byte, 200)))
	if !secondbrain.ValidRequestID(a) || !secondbrain.ValidRequestID(long) {
		t.Fatalf("ids %q / %q are not valid notes request ids", a, long)
	}
}

func TestResolveSetupDestination_HoldsTheReviewedPlaceAndWritesNothing(t *testing.T) {
	h := newSetupHarness(t)
	dest, err := h.service.ResolveSetupDestination(h.reviewed(), "", "ignored name")
	if err != nil {
		t.Fatal(err)
	}
	if dest.Parent != h.root || dest.Name != secondbrain.DefaultFolderName {
		t.Fatalf("dest = %+v", dest)
	}
	// The Workspace Directory moved after the review: refuse, never substitute.
	moved := filepath.Join(filepath.Dir(h.root), "Elsewhere")
	h.service.deps.DefaultParent = func() string { return moved }
	if _, err := h.service.ResolveSetupDestination(h.reviewed(), "", ""); !errors.Is(err, ErrSecondBrainDestinationChanged) {
		t.Fatalf("err = %v, want ErrSecondBrainDestinationChanged", err)
	}
	if _, err := h.service.ResolveSetupDestination("", "", ""); !errors.Is(err, ErrSecondBrainDestinationChanged) {
		t.Fatalf("a missing witness was accepted: %v", err)
	}
	h.service.deps.DefaultParent = func() string { return "" }
	if _, err := h.service.ResolveSetupDestination(h.reviewed(), "", ""); !errors.Is(err, ErrSecondBrainSelection) {
		t.Fatalf("no default: err = %v", err)
	}
	// A chosen parent comes only through the trusted selection.
	h.tokens["docs"] = h.documents()
	dest, err = h.service.ResolveSetupDestination("", "docs", "My Notes")
	if err != nil || dest.Parent != h.documents() || dest.Name != "My Notes" {
		t.Fatalf("chosen dest = %+v, %v", dest, err)
	}
	if _, err := h.service.ResolveSetupDestination("", "expired", "My Notes"); !errors.Is(err, ErrSecondBrainSelection) {
		t.Fatalf("expired token: err = %v", err)
	}
	if _, err := h.service.ResolveSetupDestination("", "docs", "../escape"); err == nil {
		t.Fatal("a path-like name was accepted")
	}
	if _, err := os.Stat(h.reviewed()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("resolving wrote the folder: %v", err)
	}
	if doc := h.document(t); doc.Setup != nil || doc.Connected() != nil {
		t.Fatalf("resolving changed the record: %+v", doc.Setup)
	}
}

func TestSetup_PreparedOperationMakesAndConnectsTheReviewedFolderOnce(t *testing.T) {
	h := newSetupHarness(t)
	opID := SecondBrainSetupOperationID("hq-1")
	h.prepare(t, opID)
	doc := h.document(t)
	if doc.Setup == nil || doc.Setup.Status != secondbrain.SetupPending || doc.Setup.Target() != h.reviewed() {
		t.Fatalf("prepared = %+v", doc.Setup)
	}
	if _, err := os.Stat(h.reviewed()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preparing wrote the folder")
	}

	result := h.run(t, opID)
	if result.Status != SecondBrainSetupReady || result.Path != h.reviewed() || !result.Writable {
		t.Fatalf("result = %+v", result)
	}
	doc = h.document(t)
	source := doc.Connected()
	if source == nil || source.Origin != secondbrain.OriginCreatedByOri || source.RequestID != opID || doc.Setup != nil {
		t.Fatalf("source = %+v setup = %+v", source, doc.Setup)
	}
	if source.FillIn == nil || source.FillIn.ProjectAsk != secondbrain.ProjectDeferred {
		t.Fatalf("project state = %+v, want deferred (not asked)", source.FillIn)
	}
	if _, err := os.Stat(filepath.Join(h.reviewed(), secondbrain.FolderInbox, "README.md")); err != nil {
		t.Fatalf("layout missing: %v", err)
	}
	// The setup receipt is rules-only: no model, and the look covered only the
	// folder Ori just made.
	if calls := h.model.calls.Load(); calls != 0 {
		t.Fatalf("the setup called the model %d times", calls)
	}
	if source.LastLook == nil || source.LastLook.HeadlineBy == secondbrain.HeadlineByModel {
		t.Fatalf("setup look = %+v", source.LastLook)
	}
	if h.connected.Load() != 1 {
		t.Fatalf("onConnected ran %d times", h.connected.Load())
	}

	// Lost response, repeated confirmation, a second tab: nothing new.
	again := h.run(t, opID)
	if again.Status != SecondBrainSetupReady || h.connected.Load() != 1 {
		t.Fatalf("replay = %+v, callbacks %d", again, h.connected.Load())
	}
	h.prepare(t, opID)
	if doc := h.document(t); doc.Setup != nil {
		t.Fatalf("a finished operation was reopened: %+v", doc.Setup)
	}
	entries, _ := os.ReadDir(h.root)
	if len(entries) != 1 {
		t.Fatalf("expected exactly one notes folder, found %d", len(entries))
	}
}

func TestSetup_RetryUsesTheReviewedParentAfterTheRootChanges(t *testing.T) {
	h := newSetupHarness(t)
	opID := SecondBrainSetupOperationID("hq-drift")
	h.prepare(t, opID)
	moved := filepath.Join(filepath.Dir(h.root), "Moved Root")
	if err := os.MkdirAll(moved, 0o750); err != nil {
		t.Fatal(err)
	}
	h.service.deps.DefaultParent = func() string { return moved }
	result, err := h.service.RetrySetup(context.Background(), "local")
	h.service.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != h.reviewed() {
		t.Fatalf("retry went to %q, want the reviewed %q", result.Path, h.reviewed())
	}
	if entries, _ := os.ReadDir(moved); len(entries) != 0 {
		t.Fatalf("the new default root was written to: %d entries", len(entries))
	}
}

func TestSetup_AnotherConnectedSourceStopsItWithoutWriting(t *testing.T) {
	h := newSetupHarness(t)
	opID := SecondBrainSetupOperationID("hq-race")
	h.prepare(t, opID)
	h.connectVault(t) // the user connected their vault from Notes meanwhile
	result := h.run(t, opID)
	if result.Status != SecondBrainSetupStopped || result.Reason != secondbrain.SetupReasonCurrentSource || result.Message == "" {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(h.reviewed()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a folder was made beside an existing connection")
	}
	if doc := h.document(t); doc.Connected() == nil || doc.Connected().ID != h.vaultID {
		t.Fatalf("the user's connection was replaced: %+v", doc.Connected())
	}
}

func TestSetup_AnOccupiedPathIsNeverAdopted(t *testing.T) {
	h := newSetupHarness(t)
	opID := SecondBrainSetupOperationID("hq-occupied")
	h.prepare(t, opID)
	if err := os.MkdirAll(h.reviewed(), 0o750); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(h.reviewed(), "mine.md")
	if err := os.WriteFile(mine, []byte("the user's own note\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		result := h.run(t, opID)
		if result.Status != SecondBrainSetupFailed || result.Reason != secondbrain.SetupReasonOccupied {
			t.Fatalf("attempt %d: %+v", attempt, result)
		}
	}
	if data, _ := os.ReadFile(mine); string(data) != "the user's own note\n" { // #nosec G304 -- the test's own temp tree
		t.Fatalf("the user's file changed: %q", data)
	}
	if entries, _ := os.ReadDir(h.reviewed()); len(entries) != 1 {
		t.Fatalf("Ori wrote into an occupied folder: %d entries", len(entries))
	}
	if doc := h.document(t); doc.Connected() != nil {
		t.Fatal("an occupied folder was connected")
	}
}

// The crash window between making the folder and recording the connection.
func TestSetup_RecoversItsOwnFolderAfterACrashBeforeTheConnection(t *testing.T) {
	h := newSetupHarness(t)
	opID := SecondBrainSetupOperationID("hq-crash")
	h.prepare(t, opID)
	if _, err := (secondbrain.Creator{Provenance: opID}).Create(h.root, secondbrain.DefaultFolderName, secondbrain.Seeds{}); err != nil {
		t.Fatal(err)
	}
	// …and the process died here. A restarted service over the same store:
	restarted := NewSecondBrainService(h.service.store, h.service.deps)
	result, err := restarted.RetrySetup(context.Background(), "local")
	restarted.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != SecondBrainSetupReady || result.Path != h.reviewed() {
		t.Fatalf("recovery = %+v", result)
	}
	entries, _ := os.ReadDir(h.root)
	if len(entries) != 1 {
		t.Fatalf("recovery made a second folder: %d entries", len(entries))
	}
}

func TestSetup_MissingParentFailsThenARetryFinishesOnceItIsBack(t *testing.T) {
	h := newSetupHarness(t)
	opID := SecondBrainSetupOperationID("hq-missing")
	h.prepare(t, opID)
	if err := os.RemoveAll(h.root); err != nil {
		t.Fatal(err)
	}
	result := h.run(t, opID)
	if result.Status != SecondBrainSetupFailed || result.Reason != secondbrain.SetupReasonMissingParent || result.Message == "" {
		t.Fatalf("result = %+v", result)
	}
	if err := os.MkdirAll(h.root, 0o750); err != nil {
		t.Fatal(err)
	}
	retried, err := h.service.RetrySetup(context.Background(), "local")
	h.service.Wait()
	if err != nil || retried.Status != SecondBrainSetupReady {
		t.Fatalf("retry = %+v, %v", retried, err)
	}
}

func TestSetup_UnwritableParentFailsWithGuidance(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	h := newSetupHarness(t)
	opID := SecondBrainSetupOperationID("hq-unwritable")
	h.prepare(t, opID)
	if err := os.Chmod(h.root, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(h.root, 0o750) })
	result := h.run(t, opID)
	if result.Status != SecondBrainSetupFailed || result.Reason != secondbrain.SetupReasonUnwritable {
		t.Fatalf("result = %+v", result)
	}
	if doc := h.document(t); doc.Setup == nil || doc.Setup.Status != secondbrain.SetupFailed {
		t.Fatalf("the failure was not recorded: %+v", doc.Setup)
	}
}

func TestSetup_ConcurrentRunsMakeOneFolderAndOneConnection(t *testing.T) {
	h := newSetupHarness(t)
	opID := SecondBrainSetupOperationID("hq-concurrent")
	h.prepare(t, opID)
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.service.RunSetup(context.Background(), "local", opID)
			errs <- err
		}()
	}
	wg.Wait()
	h.service.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if h.connected.Load() != 1 {
		t.Fatalf("onConnected ran %d times", h.connected.Load())
	}
	if entries, _ := os.ReadDir(h.root); len(entries) != 1 {
		t.Fatalf("%d folders made", len(entries))
	}
}

func TestSetup_NothingToRetryAndAnUnknownOperationAreRefused(t *testing.T) {
	h := newSetupHarness(t)
	if _, err := h.service.RetrySetup(context.Background(), "local"); !errors.Is(err, ErrSecondBrainNoSetup) {
		t.Fatalf("retry with nothing recorded: %v", err)
	}
	h.prepare(t, SecondBrainSetupOperationID("hq-a"))
	if _, err := h.service.RunSetup(context.Background(), "local", SecondBrainSetupOperationID("hq-b")); !errors.Is(err, ErrSecondBrainNoSetup) {
		t.Fatalf("another operation ran: %v", err)
	}
	status, err := h.service.SetupStatus(context.Background(), "local")
	if err != nil || status.Status != SecondBrainSetupPending {
		t.Fatalf("status = %+v, %v", status, err)
	}
}

func TestSetup_StatusReportsAReadOnlyConnectionAsReadyButNotWritable(t *testing.T) {
	h := newSetupHarness(t)
	status, err := h.service.SetupStatus(context.Background(), "local")
	if err != nil || status.Status != SecondBrainSetupNone {
		t.Fatalf("empty status = %+v, %v", status, err)
	}
	h.connectVault(t)
	status, err = h.service.SetupStatus(context.Background(), "local")
	if err != nil || status.Status != SecondBrainSetupReady || status.Writable {
		t.Fatalf("vault status = %+v, %v", status, err)
	}
}
