package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The fence contract: an Assistant Home (or split child) write whose base
// version no longer matches the record on disk is refused, on every handle,
// so an acknowledged write from another handle or process is never silently
// replaced. Ordinary workspaces keep the older permissive behavior.

func newFencedHome(id string) *Workspace {
	ws := &Workspace{ID: id, Name: "Music Home", OwnerUserID: "owner", Status: StatusActive}
	ws.SetAssistantProgramState(&AssistantProgramState{
		SchemaVersion: AssistantProgramStateSchemaVersion,
		Key:           AssistantProgramKey{OwnerUserID: "owner", ProgramID: "music"},
	})
	return ws
}

func TestFileStoreFence_StaleProtectedSaveRefusedAcrossIndependentHandles(t *testing.T) {
	base := t.TempDir()
	first, err := NewFileStore(base)
	if err != nil {
		t.Fatal(err)
	}
	home := newFencedHome("home-fence")
	if err := first.Save(home); err != nil {
		t.Fatal(err)
	}
	// A second handle over the same folder stands in for a second process: it
	// loaded the Home before the first handle's next write.
	second, err := NewFileStore(base)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := second.Get(home.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := first.Get(home.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.Description = "queue acknowledged"
	if err := first.Save(current); err != nil {
		t.Fatal(err)
	}
	stale.Description = "old snapshot"
	if err := second.Save(stale); !errors.Is(err, ErrStaleWorkspaceVersion) {
		t.Fatalf("stale Home write was not refused: %v", err)
	}
	reopened, err := NewFileStore(base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(home.ID)
	if err != nil || got.Description != "queue acknowledged" || got.Version != current.Version {
		t.Fatalf("acknowledged write lost after stale save: %+v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(base, got.FolderSlug, WorkspaceFenceLockFile)); err != nil {
		t.Fatalf("fence lock file missing beside workspace.json: %v", err)
	}
}

func TestFileStoreFence_OrdinaryStaleSaveStillProceeds(t *testing.T) {
	base := t.TempDir()
	first, err := NewFileStore(base)
	if err != nil {
		t.Fatal(err)
	}
	plain := &Workspace{ID: "plain-ws", Name: "Plain", Status: StatusActive}
	if err := first.Save(plain); err != nil {
		t.Fatal(err)
	}
	second, err := NewFileStore(base)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := second.Get(plain.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := first.Get(plain.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.Description = "newer"
	if err := first.Save(current); err != nil {
		t.Fatal(err)
	}
	// Not a Home or project link: the write is logged, not refused. This is
	// the documented scope boundary of the fence, not an endorsement.
	if err := second.Save(stale); err != nil {
		t.Fatalf("ordinary stale save changed behavior: %v", err)
	}
}

func TestSyncStoreFence_PrimaryFailureRestoresExactFolderVersionAndRetries(t *testing.T) {
	primary := NewInMemoryStore()
	disk, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sync := NewSyncStore(primary, disk)
	home := newFencedHome("home-retry")
	if err := sync.Save(home); err != nil {
		t.Fatal(err)
	}
	base := home.Version
	failing := NewSyncStore(&failingWorkspaceSaveStore{Store: primary, err: os.ErrPermission}, disk)
	attempt, err := sync.Get(home.ID)
	if err != nil {
		t.Fatal(err)
	}
	attempt, _ = cloneWorkspaceForRebind(attempt)
	attempt.Description = "second try"
	if err := failing.Save(attempt); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("injected primary failure not reported: %v", err)
	}
	onDisk, err := disk.Get(home.ID)
	if err != nil || onDisk.Version != base || onDisk.Description != "" {
		t.Fatalf("folder was not restored to its exact previous record: %+v %v", onDisk, err)
	}
	if attempt.Version != base {
		t.Fatalf("caller's record drifted from the restored folder: %d != %d", attempt.Version, base)
	}
	if err := sync.Save(attempt); err != nil {
		t.Fatalf("retry after exact rollback refused: %v", err)
	}
	onDisk, err = disk.Get(home.ID)
	if err != nil || onDisk.Version != base+1 || onDisk.Description != "second try" {
		t.Fatalf("retry did not land: %+v %v", onDisk, err)
	}
}

func TestSyncStoreFence_LegacyFolderOnlyWriteDoesNotFenceAnAgreeingHome(t *testing.T) {
	primary := &casMemoryPrimary{InMemoryStore: NewInMemoryStore()}
	disk, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sync := NewSyncStore(primary, disk)
	home := newFencedHome("home-legacy-folder-write")
	if err := sync.Save(home); err != nil {
		t.Fatal(err)
	}
	// Session handlers still write the folder directly (tags, backfills,
	// portable-state sync). Such a write moves the folder version but carries
	// the Home envelope unchanged, and must not fence the next Home write.
	legacy, err := disk.Get(home.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Tags = []string{"folder-only"}
	if err := disk.Save(legacy); err != nil {
		t.Fatal(err)
	}
	next, err := sync.Get(home.ID)
	if err != nil {
		t.Fatal(err)
	}
	next, _ = cloneWorkspaceForRebind(next)
	next.Description = "lands"
	if err := sync.Save(next); err != nil {
		t.Fatalf("legacy folder-only write fenced an agreeing Home: %v", err)
	}
	if got, err := disk.Get(home.ID); err != nil || got.Description != "lands" || got.Version != next.Version {
		t.Fatalf("mirrored write did not land: %+v %v", got, err)
	}
}

func TestSyncStoreFence_EnvelopeSplitFailsClosedUntilExactRestore(t *testing.T) {
	primary := &casMemoryPrimary{InMemoryStore: NewInMemoryStore()}
	disk, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sync := NewSyncStore(primary, disk)
	home := newFencedHome("home-split")
	if err := sync.Save(home); err != nil {
		t.Fatal(err)
	}
	// A Home state that reached the folder only (what an interrupted mirror
	// write without a journal, or an older unfenced build, leaves behind).
	if err := disk.Update(home.ID, func(folder *Workspace) error {
		state := folder.GetAssistantProgramState()
		state.StateRevision++
		folder.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	next, err := sync.Get(home.ID)
	if err != nil {
		t.Fatal(err)
	}
	next, _ = cloneWorkspaceForRebind(next)
	next.Description = "must not land"
	if err := sync.Save(next); !errors.Is(err, ErrWorkspaceMirrorsDiverged) {
		t.Fatalf("split Home envelopes accepted a new write: %v", err)
	}
	folder, err := disk.Get(home.ID)
	if err != nil || folder.Description != "" {
		t.Fatalf("refused write reached the folder: %+v %v", folder, err)
	}
	// Explicit exact restoration from the unchanged primary (an operator or a
	// reviewed reconciliation, never an automatic winner) clears the fence.
	restored, err := sync.Get(home.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored, _ = cloneWorkspaceForRebind(restored)
	if err := disk.RestoreMirrorRecord(restored); err != nil {
		t.Fatal(err)
	}
	if err := sync.Save(next); err != nil {
		t.Fatalf("write refused after exact restoration: %v", err)
	}
}

// casMemoryPrimary is a clone-semantics in-memory primary with the conditional
// save a real SQLite primary offers, so a stale second handle can be refused
// without importing the session package here.
type casMemoryPrimary struct{ *InMemoryStore }

func (p *casMemoryPrimary) Get(id string) (*Workspace, error) {
	ws, err := p.InMemoryStore.Get(id)
	if err != nil {
		return nil, err
	}
	return cloneWorkspaceForRebind(ws)
}

func (p *casMemoryPrimary) Save(ws *Workspace) error {
	clone, err := cloneWorkspaceForRebind(ws)
	if err != nil {
		return err
	}
	return p.InMemoryStore.Save(clone)
}

func (p *casMemoryPrimary) SaveExpecting(ws *Workspace, expected int64) error {
	if stored, err := p.InMemoryStore.Get(ws.ID); err == nil && stored.Version != expected {
		return fmt.Errorf("%w: primary at %d, expected %d", ErrStaleWorkspaceVersion, stored.Version, expected)
	}
	return p.Save(ws)
}

func TestSyncStoreFence_StaleUpdateFromSecondFolderHandleIsRefused(t *testing.T) {
	primary := &casMemoryPrimary{InMemoryStore: NewInMemoryStore()}
	base := t.TempDir()
	diskA, err := NewFileStore(base)
	if err != nil {
		t.Fatal(err)
	}
	a := NewSyncStore(primary, diskA)
	home := newFencedHome("home-two-handles")
	if err := a.Save(home); err != nil {
		t.Fatal(err)
	}
	diskB, err := NewFileStore(base)
	if err != nil {
		t.Fatal(err)
	}
	b := NewSyncStore(primary, diskB)
	stale, err := b.Get(home.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Update(home.ID, func(current *Workspace) error {
		current.Description = "acknowledged"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stale.Description = "stale"
	if err := b.Save(stale); !errors.Is(err, ErrStaleWorkspaceVersion) {
		t.Fatalf("second handle overwrote the acknowledged write: %v", err)
	}
	reopened, err := NewFileStore(base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(home.ID)
	if err != nil || got.Description != "acknowledged" {
		t.Fatalf("acknowledged write lost in the folder: %+v %v", got, err)
	}
	if kept, err := primary.Get(home.ID); err != nil || kept.Description != "acknowledged" {
		t.Fatalf("acknowledged write lost in the primary: %+v %v", kept, err)
	}
}
