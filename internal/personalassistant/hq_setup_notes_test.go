package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
)

// Personal HQ with notes included (assistant-hq-onboarding 3.4, 3.8): the real
// notes service on a real temporary tree, behind the HQ coordinator's fakes.

type hqNotesFixture struct {
	*hqFixture
	notes *setupHarness
}

func newHQNotesFixture(t *testing.T) *hqNotesFixture {
	t.Helper()
	store, _ := newTestStore(t)
	f := &hqNotesFixture{hqFixture: newHQFixture(t, store), notes: newSetupHarness(t)}
	f.coordinator.SetNotesSetup(hqGatedNotes{inner: f.notes.service, states: store})
	return f
}

// hqGatedNotes holds the notes service to the real knowledge binding's rule:
// the notes record exists only for an active (or paused) relationship, so
// nothing can be written there while HQ is still being built.
type hqGatedNotes struct {
	inner  *SecondBrainService
	states Store
}

func (g hqGatedNotes) bound(ctx context.Context) error {
	state, err := g.states.GetState(ctx, "local")
	if err != nil {
		return err
	}
	if state.Status != StatusActive && state.Status != StatusPaused {
		return ErrNeedsHQ
	}
	return nil
}

func (g hqGatedNotes) ResolveSetupDestination(witness, token, name string) (SecondBrainSetupDestination, error) {
	return g.inner.ResolveSetupDestination(witness, token, name)
}

func (g hqGatedNotes) PrepareSetup(ctx context.Context, userID, opID, hqRequestID string, dest SecondBrainSetupDestination) error {
	if err := g.bound(ctx); err != nil {
		return err
	}
	return g.inner.PrepareSetup(ctx, userID, opID, hqRequestID, dest)
}

func (g hqGatedNotes) RunSetup(ctx context.Context, userID, opID string) (SecondBrainSetupResult, error) {
	if err := g.bound(ctx); err != nil {
		return SecondBrainSetupResult{}, err
	}
	return g.inner.RunSetup(ctx, userID, opID)
}

func (g hqGatedNotes) SetupStatus(ctx context.Context, userID string) (SecondBrainSetupResult, error) {
	if err := g.bound(ctx); err != nil {
		return SecondBrainSetupResult{Status: SecondBrainSetupNone}, nil
	}
	return g.inner.SetupStatus(ctx, userID)
}

func (f *hqNotesFixture) withNotes() HQSetupRequest {
	request := f.request()
	request.Notes = &HQNotesChoice{Include: true, Witness: f.notes.reviewed()}
	return request
}

func (f *hqNotesFixture) folders(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir(f.notes.root)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func TestHQSetupNotes_BuildsHQAndTheReviewedNotesFolderTogether(t *testing.T) {
	ctx := context.Background()
	f := newHQNotesFixture(t)
	result, err := f.coordinator.Setup(ctx, "local", f.withNotes())
	f.notes.service.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Status != StatusActive {
		t.Fatalf("HQ status = %s", result.State.Status)
	}
	if result.Notes == nil || result.Notes.Status != SecondBrainSetupReady || result.Notes.Path != f.notes.reviewed() || !result.Notes.Writable {
		t.Fatalf("notes = %+v", result.Notes)
	}
	if f.folders(t) != 1 || f.creator.calls != 1 || f.briefs.updateCalls != 1 {
		t.Fatalf("folders %d, HQ creates %d, schedules %d", f.folders(t), f.creator.calls, f.briefs.updateCalls)
	}
	doc := f.notes.document(t)
	if doc.Connected() == nil || doc.Connected().FillIn.ProjectAsk != secondbrain.ProjectDeferred {
		t.Fatalf("project question state = %+v", doc.Connected())
	}
	if f.notes.model.calls.Load() != 0 {
		t.Fatal("building the bundle called the model")
	}

	// Lost response: the same confirmation again rebuilds nothing.
	replay, err := f.coordinator.Setup(ctx, "local", f.withNotes())
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Resumed || replay.Notes == nil || replay.Notes.Status != SecondBrainSetupReady {
		t.Fatalf("replay = %+v notes %+v", replay, replay.Notes)
	}
	if f.folders(t) != 1 || f.creator.calls != 1 || f.briefs.updateCalls != 1 || f.notes.connected.Load() != 1 {
		t.Fatalf("replay duplicated: folders %d creates %d schedules %d connects %d",
			f.folders(t), f.creator.calls, f.briefs.updateCalls, f.notes.connected.Load())
	}
}

func TestHQSetupNotes_LegacyAndDeferredRequestsWriteNoNotes(t *testing.T) {
	for name, notes := range map[string]*HQNotesChoice{"legacy": nil, "deferred": {Include: false}} {
		t.Run(name, func(t *testing.T) {
			f := newHQNotesFixture(t)
			request := f.request()
			request.Notes = notes
			result, err := f.coordinator.Setup(context.Background(), "local", request)
			if err != nil {
				t.Fatal(err)
			}
			if result.State.Status != StatusActive || result.Notes != nil {
				t.Fatalf("status %s notes %+v", result.State.Status, result.Notes)
			}
			if f.folders(t) != 0 {
				t.Fatal("a notes folder was written")
			}
			if doc := f.notes.document(t); doc.Setup != nil || doc.Connected() != nil {
				t.Fatalf("the notes record changed: %+v", doc.Setup)
			}
		})
	}
}

// A legacy payload's exact bytes and hash are unchanged by the new field.
func TestHQSetupNotes_LegacyPayloadHashIsUnchanged(t *testing.T) {
	legacy, err := normalizeHQSetupRequest(validHQSetupRequest())
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(legacy)
	if string(payload) == "" || json.Valid(payload) && containsKey(payload, "notes") {
		t.Fatalf("a legacy payload carries a notes key: %s", payload)
	}
	withDefer := validHQSetupRequest()
	withDefer.Notes = &HQNotesChoice{Include: false}
	deferred, _ := normalizeHQSetupRequest(withDefer)
	if deferred.Hash == legacy.Hash {
		t.Fatal("an explicit defer hashes like a legacy request")
	}
}

func containsKey(payload []byte, key string) bool {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(payload, &fields)
	_, ok := fields[key]
	return ok
}

func TestHQSetupNotes_AChangedReviewIsRefusedBeforeAnyEffect(t *testing.T) {
	f := newHQNotesFixture(t)
	request := f.withNotes()
	request.Notes.Witness = filepath.Join(filepath.Dir(f.notes.root), "Old Root", secondbrain.DefaultFolderName)
	_, err := f.coordinator.Setup(context.Background(), "local", request)
	if !errors.Is(err, ErrSecondBrainDestinationChanged) {
		t.Fatalf("err = %v, want ErrSecondBrainDestinationChanged", err)
	}
	state, _ := f.store.GetState(context.Background(), "local")
	if state.Status != StatusAwaitingHQ || state.LastHQRequestID != "" || f.creator.calls != 0 {
		t.Fatalf("an effect happened: status %s request %q creates %d", state.Status, state.LastHQRequestID, f.creator.calls)
	}
	if doc := f.notes.document(t); doc.Setup != nil {
		t.Fatal("a notes operation was recorded for a refused review")
	}
}

func TestHQSetupNotes_NotesFailureLeavesHQActiveAndRetriesNotesOnly(t *testing.T) {
	ctx := context.Background()
	f := newHQNotesFixture(t)
	if err := os.MkdirAll(f.notes.reviewed(), 0o750); err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(f.notes.reviewed(), "keep.md")
	if err := os.WriteFile(userFile, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := f.coordinator.Setup(ctx, "local", f.withNotes())
	if err != nil {
		t.Fatalf("a notes failure failed HQ: %v", err)
	}
	if result.State.Status != StatusActive {
		t.Fatalf("HQ status = %s", result.State.Status)
	}
	if result.Notes == nil || result.Notes.Status != SecondBrainSetupFailed || result.Notes.Reason != secondbrain.SetupReasonOccupied {
		t.Fatalf("notes = %+v", result.Notes)
	}
	// The user moves their folder away; the notes-only retry makes the
	// reviewed one and touches nothing of HQ.
	if err := os.Rename(f.notes.reviewed(), filepath.Join(f.notes.root, "Moved")); err != nil {
		t.Fatal(err)
	}
	retried, err := f.notes.service.RetrySetup(ctx, "local")
	f.notes.service.Wait()
	if err != nil || retried.Status != SecondBrainSetupReady {
		t.Fatalf("retry = %+v, %v", retried, err)
	}
	if f.creator.calls != 1 || f.briefs.updateCalls != 1 || f.hq.designateCalls != 1 {
		t.Fatalf("the notes retry touched HQ: creates %d schedules %d designations %d",
			f.creator.calls, f.briefs.updateCalls, f.hq.designateCalls)
	}
	if data, _ := os.ReadFile(filepath.Join(f.notes.root, "Moved", "keep.md")); string(data) != "mine\n" { // #nosec G304 -- the test's own temp tree
		t.Fatal("the user's file was touched")
	}
}

func TestHQSetupNotes_ResumeAfterACrashCarriesTheConfirmedNotesChoice(t *testing.T) {
	ctx := context.Background()
	f := newHQNotesFixture(t)
	f.briefs.updateErr = errors.New("injected config failure")
	if _, err := f.coordinator.Setup(ctx, "local", f.withNotes()); err == nil {
		t.Fatal("expected the injected partial failure")
	}
	// The reviewed folder was journalled with the claim, before any effect.
	state, _ := f.store.GetState(ctx, "local")
	stored, err := decodeStoredHQRequest(state)
	if err != nil || stored.Notes == nil || stored.Notes.Parent != f.notes.root || stored.Notes.Folder != secondbrain.DefaultFolderName {
		t.Fatalf("journalled notes = %+v, %v", stored.Notes, err)
	}
	if _, err := os.Stat(f.notes.reviewed()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("notes were written while HQ was not yet active")
	}
	// The Workspace Directory moves before the resume: the resume still uses
	// the reviewed place.
	moved := filepath.Join(filepath.Dir(f.notes.root), "Moved Root")
	if err := os.MkdirAll(moved, 0o750); err != nil {
		t.Fatal(err)
	}
	f.notes.service.deps.DefaultParent = func() string { return moved }
	f.briefs.updateErr = nil
	result, err := f.coordinator.Resume(ctx, "local")
	f.notes.service.Wait()
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.State.Status != StatusActive || result.Notes == nil || result.Notes.Path != f.notes.reviewed() {
		t.Fatalf("resume = %s notes %+v", result.State.Status, result.Notes)
	}
	if entries, _ := os.ReadDir(moved); len(entries) != 0 {
		t.Fatal("the resume wrote under the new root")
	}
}

// The residual window: HQ became active, then the process died before the
// notes operation was recorded. The client's retry of the same confirmation
// re-arms the notes from the same review; a later replay never duplicates.
func TestHQSetupNotes_AReplayAfterACrashBeforeTheNotesRecordFinishesTheNotes(t *testing.T) {
	ctx := context.Background()
	f := newHQNotesFixture(t)
	f.coordinator.SetNotesSetup(nil) // the notes half never ran
	request := f.withNotes()
	if _, err := f.coordinator.Setup(ctx, "local", request); !errors.Is(err, ErrSecondBrainUnavailable) {
		t.Fatalf("without a notes service the confirmed notes must be refused: %v", err)
	}
	// Model the crash: build HQ with the notes service bound, but drop the
	// notes step by unbinding it right after the claim journalled the review.
	f.coordinator.SetNotesSetup(hqGatedNotes{inner: f.notes.service, states: f.store})
	f.creator.err = nil
	crash := &crashAfterActivation{hqGatedNotes: hqGatedNotes{inner: f.notes.service, states: f.store}}
	f.coordinator.SetNotesSetup(crash)
	first, err := f.coordinator.Setup(ctx, "local", request)
	if err != nil {
		t.Fatal(err)
	}
	if first.State.Status != StatusActive || first.Notes == nil || first.Notes.Status != SecondBrainSetupFailed {
		t.Fatalf("first = %s notes %+v", first.State.Status, first.Notes)
	}
	if f.folders(t) != 0 {
		t.Fatal("the dropped step wrote notes")
	}
	f.coordinator.SetNotesSetup(hqGatedNotes{inner: f.notes.service, states: f.store})
	replay, err := f.coordinator.Setup(ctx, "local", request)
	f.notes.service.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if replay.Notes == nil || replay.Notes.Status != SecondBrainSetupReady || replay.Notes.Path != f.notes.reviewed() {
		t.Fatalf("replay notes = %+v", replay.Notes)
	}
	if _, err := f.coordinator.Setup(ctx, "local", request); err != nil {
		t.Fatal(err)
	}
	if f.folders(t) != 1 || f.creator.calls != 1 || f.notes.connected.Load() != 1 {
		t.Fatalf("folders %d creates %d connects %d", f.folders(t), f.creator.calls, f.notes.connected.Load())
	}
}

// crashAfterActivation refuses to record the notes operation, as if the
// process had stopped between HQ activation and the notes record.
type crashAfterActivation struct{ hqGatedNotes }

func (c *crashAfterActivation) PrepareSetup(context.Context, string, string, string, SecondBrainSetupDestination) error {
	return errors.New("process stopped")
}

func TestHQSetupNotes_AlreadyConnectedSourceIsKeptAndHQStillBuilds(t *testing.T) {
	f := newHQNotesFixture(t)
	f.notes.connectVault(t)
	result, err := f.coordinator.Setup(context.Background(), "local", f.withNotes())
	f.notes.service.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Status != StatusActive || result.Notes == nil || result.Notes.Reason != secondbrain.SetupReasonCurrentSource {
		t.Fatalf("status %s notes %+v", result.State.Status, result.Notes)
	}
	if f.folders(t) != 0 {
		t.Fatal("a second notes folder was made")
	}
	if doc := f.notes.document(t); doc.Connected() == nil || doc.Connected().ID != f.notes.vaultID {
		t.Fatal("the existing connection was replaced")
	}
}

func TestHQSetupNotes_ConcurrentConfirmationsMakeOneHQAndOneFolder(t *testing.T) {
	f := newHQNotesFixture(t)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = f.coordinator.Setup(context.Background(), "local", f.withNotes())
		}()
	}
	wg.Wait()
	f.notes.service.Wait()
	if f.creator.calls != 1 || f.briefs.updateCalls != 1 || f.folders(t) != 1 || f.notes.connected.Load() != 1 {
		t.Fatalf("creates %d schedules %d folders %d connects %d",
			f.creator.calls, f.briefs.updateCalls, f.folders(t), f.notes.connected.Load())
	}
}

func TestHQSetupNotes_WithoutANotesServiceNotesAreRefusedButLegacyBuilds(t *testing.T) {
	store, _ := newTestStore(t)
	f := newHQFixture(t, store)
	request := f.request()
	request.Notes = &HQNotesChoice{Include: true, Witness: "/tmp/x/Second Brain"}
	if _, err := f.coordinator.Setup(context.Background(), "local", request); !errors.Is(err, ErrSecondBrainUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if f.creator.calls != 0 {
		t.Fatal("HQ was built although its confirmed notes could not be recorded")
	}
	if _, err := f.coordinator.Setup(context.Background(), "local", f.request()); err != nil {
		t.Fatalf("a legacy request failed: %v", err)
	}
}
