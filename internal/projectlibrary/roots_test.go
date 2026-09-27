package projectlibrary

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Changes provider evidence after the first two Home reads (the first root
// verification), so the second verification after listing must drop rows.
type providerDropsDuringRead struct {
	workspace.Store
	reads     int
	available *bool
}

func (s *providerDropsDuringRead) Get(id string) (*workspace.Workspace, error) {
	s.reads++
	if s.reads == 3 {
		*s.available = false
	}
	return s.Store.Get(id)
}

func TestRoots_ReadDirectoryDiscardsRowsWhenProviderChangesDuringRead(t *testing.T) {
	r, scope, file, _, root := connectedMusicRoot(t)
	available := true
	changing := &providerDropsDuringRead{Store: file, available: &available}
	r.library = NewStore(changing).WithProviderEvidence(func(candidate Scope, home *workspace.Workspace) bool {
		return available && candidate == scope && home.ID == scope.HomeID
	})
	rows, partial, err := r.ReadDirectory(context.Background(), scope, root.ID, "", 50)
	if !errors.Is(err, ErrUnavailable) || len(rows) != 0 || partial || changing.reads < 3 {
		t.Fatalf("provider change exposed observed names: %v rows %+v partial %v reads %d", err, rows, partial, changing.reads)
	}
}

type testRootPicker struct {
	path   string
	chosen bool
}

func (p *testRootPicker) Available() bool { return true }
func (p *testRootPicker) Choose(context.Context, string) (string, bool, error) {
	return p.path, p.chosen, nil
}

func rootTestService(t *testing.T) (*Roots, Scope, *workspace.FileStore, *testRootPicker) {
	t.Helper()
	file, scope := libraryHome(t)
	if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.PluginAvailable = true
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	initializeLibrary(t, NewStore(file), scope)
	stored, getErr := file.Get(scope.HomeID)
	if getErr != nil || !stored.GetAssistantProgramState().PluginAvailable {
		t.Fatalf("provider availability lost: %+v %v", stored.GetAssistantProgramState(), getErr)
	}
	picker := &testRootPicker{chosen: true}
	service := NewRoots(NewStore(file), picker, pathselection.NewStore(), filejanitor.RootGuards{})
	return service, scope, file, picker
}

type portfolioTestResolver func(context.Context, string, string, string) (string, string, error)

func (fn portfolioTestResolver) PortfolioRoot(ctx context.Context, userID, offerID, homeID string) (string, string, error) {
	return fn(ctx, userID, offerID, homeID)
}

func TestRoots_PortfolioHandoffRejectsReplacementBetweenOfferAndPickerToken(t *testing.T) {
	r, scope, _, _ := rootTestService(t)
	original := filepath.Join(t.TempDir(), "Music")
	if err := os.Mkdir(original, 0o750); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(original)
	if err != nil {
		t.Fatal(err)
	}
	originalIdentity, err := DirectoryIdentity(info)
	if err != nil {
		t.Fatal(err)
	}
	resolver := portfolioTestResolver(func(_ context.Context, user, offer, home string) (string, string, error) {
		if user != scope.OwnerUserID || offer != "resolved-offer" || home != scope.HomeID {
			t.Fatalf("resolver called with foreign origin %s/%s/%s", user, offer, home)
		}
		if err := os.Rename(original, original+"-old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(original, 0o750); err != nil {
			t.Fatal(err)
		}
		return original, originalIdentity, nil
	})
	if token, err := r.PickFromPortfolio(context.Background(), scope, "resolved-offer", resolver); token != "" || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a replacement directory got an offer-scoped selection: %q %v", token, err)
	}
}

func TestRoots_PickerReviewCommitAreSeparateAndPersistAcrossRestart(t *testing.T) {
	r, scope, file, picker := rootTestService(t)
	tree := newMusicTree(t)
	picker.path, _ = filepath.EvalSymlinks(tree.root)
	if path, id, err := r.pickedRoot(picker.path); err != nil {
		t.Fatalf("pickedRoot(%q) = %q %q %v", picker.path, path, id, err)
	}
	before := fileDigest(t, filepath.Join(tree.single, "Song.rpp"))
	pick, err := r.Pick(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.selections.Resolve(pick); !errors.Is(err, pathselection.ErrUnavailable) {
		t.Fatalf("scoped token downgraded: %v", err)
	}
	if _, err := r.selections.ResolveFor(pick, "foreign-home"); !errors.Is(err, pathselection.ErrUnavailable) {
		t.Fatalf("foreign consumer resolved selection: %v", err)
	}
	if doc, err := r.library.Read(scope); err != nil || len(doc.Roots) != 0 || len(doc.Reviews) != 0 {
		t.Fatalf("picker was a grant: %+v %v", doc, err)
	}
	review, err := r.Review(scope, pick, 1)
	if err != nil || review.RootPath != picker.path || review.IncludesScan || review.Scope == "" {
		t.Fatalf("review disclosure: %+v %v", review, err)
	}
	if doc, err := r.library.Read(scope); err != nil || len(doc.Roots) != 0 || len(doc.Reviews) != 1 {
		t.Fatalf("review granted a root: %+v %v", doc, err)
	}
	connected, replay, err := r.Commit(scope, review.Token, "connect-once")
	if err != nil || replay || connected.Path != picker.path || connected.FileIdentity == "" {
		t.Fatalf("commit: %+v %v %v", connected, replay, err)
	}
	again, replay, err := r.Commit(scope, review.Token, "connect-once")
	if err != nil || !replay || again.ID != connected.ID {
		t.Fatalf("retry: %+v %v %v", again, replay, err)
	}
	if _, _, err := r.Commit(scope, review.Token, "different-key"); !errors.Is(err, ErrConflict) {
		t.Fatalf("consumed review reused: %v", err)
	}
	if _, err := r.VerifyConnectedRoot(scope, connected.ID); err != nil {
		t.Fatalf("connected root unavailable: %v", err)
	}
	rows, partial, err := r.ReadDirectory(context.Background(), scope, connected.ID, "", 1)
	if err != nil || !partial || len(rows) != 1 {
		t.Fatalf("bounded root listing: %+v %v %v", rows, partial, err)
	}
	rows, partial, err = r.ReadDirectory(context.Background(), scope, connected.ID, "Single", 5)
	if err != nil || partial || len(rows) != 1 || rows[0].Name != "Song.rpp" || rows[0].IsDir {
		t.Fatalf("pinned child directory listing: %+v %v %v", rows, partial, err)
	}
	if err := os.Symlink(tree.single, filepath.Join(tree.root, "Shortcut")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.ReadDirectory(context.Background(), scope, connected.ID, "Shortcut", 5); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("followed symlinked child: %v", err)
	}
	if _, _, err := r.ReadDirectory(context.Background(), scope, connected.ID, "../other", 5); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("accepted traversal: %v", err)
	}
	revoke, err := r.ReviewRevoke(scope, connected.ID, 3)
	if err != nil || revoke.EntryCount != 0 {
		t.Fatalf("revoke review: %+v %v", revoke, err)
	}
	removed, replay, err := r.CommitRevoke(scope, connected.ID, revoke.Token, "revoke-once")
	if err != nil || replay || removed.RevokedAt == nil {
		t.Fatalf("revoke: %+v %v %v", removed, replay, err)
	}
	if _, err := r.VerifyConnectedRoot(scope, connected.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("revoked root still granted: %v", err)
	}
	if _, _, err := r.ReadDirectory(context.Background(), scope, connected.ID, "Single", 5); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("revoked root was still scannable: %v", err)
	}
	removed, replay, err = r.CommitRevoke(scope, connected.ID, revoke.Token, "revoke-once")
	if err != nil || !replay || removed.RevokedAt == nil {
		t.Fatalf("revoke retry: %+v %v %v", removed, replay, err)
	}
	reopened, err := workspace.NewFileStore(filepath.Dir(filepath.Dir(file.GetFilesPath(scope.HomeID))))
	if err != nil {
		t.Fatal(err)
	}
	if doc, err := NewStore(reopened).Read(scope); err != nil || len(doc.Roots) != 1 || len(doc.Reviews) != 2 || doc.Roots[0].RevokedAt == nil {
		t.Fatalf("restart lost root authorization: %+v %v", doc, err)
	}
	if after := fileDigest(t, filepath.Join(tree.single, "Song.rpp")); after != before {
		t.Fatal("root consent modified project contents")
	}
	if len(connected.Path) == 0 || len(connected.ID) == 0 {
		t.Fatal("missing durable root identity")
	}
}

func TestRoots_RevocationAcknowledgementWaitsForInFlightRootOperation(t *testing.T) {
	r, scope, _, picker := rootTestService(t)
	picker.path, _ = filepath.EvalSymlinks(newMusicTree(t).root)
	pick, err := r.Pick(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.Review(scope, pick, 1)
	if err != nil {
		t.Fatal(err)
	}
	root, _, err := r.Commit(scope, review.Token, "gate-root")
	if err != nil {
		t.Fatal(err)
	}
	revoke, err := r.ReviewRevoke(scope, root.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	gate := rootAccessGate(scope)
	gate.RLock() // Simulate a descriptor read holding the shared access gate.
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, _, commitErr := r.CommitRevoke(scope, root.ID, revoke.Token, "gate-revoke")
		done <- commitErr
	}()
	<-started
	select {
	case err := <-done:
		gate.RUnlock()
		t.Fatalf("revocation acknowledged before read completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	gate.RUnlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("revocation deadlocked against root access")
	}
	if _, _, err := r.ReadDirectory(context.Background(), scope, root.ID, "", 5); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("post-acknowledgement directory operation succeeded: %v", err)
	}
}

func TestRoots_PinnedChildIdentityRefusesSameNameReplacement(t *testing.T) {
	r, scope, _, picker := rootTestService(t)
	tree := newMusicTree(t)
	picker.path, _ = filepath.EvalSymlinks(tree.root)
	pick, err := r.Pick(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.Review(scope, pick, 1)
	if err != nil {
		t.Fatal(err)
	}
	root, _, err := r.Commit(scope, review.Token, "pin-child")
	if err != nil {
		t.Fatal(err)
	}
	rows, _, err := r.ReadDirectory(context.Background(), scope, root.ID, "", 5000)
	if err != nil {
		t.Fatal(err)
	}
	var identity string
	for _, row := range rows {
		if row.Name == "Single" {
			identity = row.Identity
		}
	}
	if identity == "" {
		t.Fatal("child identity missing")
	}
	if err := os.Rename(tree.single, tree.single+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(tree.single, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.readDirectoryWithIdentity(context.Background(), scope, root.ID, "Single", identity, 50); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("replacement passed pinned child identity: %v", err)
	}
}

func TestRoots_ProviderRevocationAndReinstallStalesRootReview(t *testing.T) {
	r, scope, file, picker := rootTestService(t)
	picker.path, _ = filepath.EvalSymlinks(newMusicTree(t).root)
	selection, err := r.Pick(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.Review(scope, selection, 1)
	if err != nil {
		t.Fatal(err)
	}
	setAvailable := func(available bool) {
		t.Helper()
		if err := file.Update(scope.HomeID, func(home *workspace.Workspace) error {
			state := home.GetAssistantProgramState()
			state.PluginAvailable = available
			state.StateRevision++
			home.SetAssistantProgramState(state)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	setAvailable(false)
	if _, _, err := r.Commit(scope, review.Token, "provider-stale"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("disabled provider granted root: %v", err)
	}
	setAvailable(true)
	if _, _, err := r.Commit(scope, review.Token, "provider-stale"); !errors.Is(err, ErrConflict) {
		t.Fatalf("reinstalled provider reused old review: %v", err)
	}
	if doc, err := r.library.Read(scope); err != nil || len(doc.Roots) != 0 {
		t.Fatalf("provider drift granted root: %+v %v", doc, err)
	}
}

func TestRoots_RejectsSymlinksBroadRootsForeignSelectionsAndChanges(t *testing.T) {
	r, scope, _, picker := rootTestService(t)
	tree := newMusicTree(t)
	picker.path, _ = filepath.EvalSymlinks(tree.root)
	other := scope
	other.HomeID = "other-home"
	selection, err := r.Pick(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Review(other, selection, 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("foreign Home was permitted: %v", err)
	}
	if _, err := r.Review(scope, selection, 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale root review: %v", err)
	}
	pick, err := r.Pick(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.Review(scope, pick, 1)
	if err != nil {
		t.Fatal(err)
	}
	moved := picker.path + "-moved"
	if err := os.Rename(picker.path, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(picker.path, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Commit(scope, review.Token, "changed-folder"); !errors.Is(err, ErrConflict) {
		// A replaced identity cannot acquire the reviewed root grant.
		t.Fatalf("replaced folder was granted: %v", err)
	}
	picker.path = filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(moved, picker.path); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Pick(context.Background(), scope); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("symlink root was granted: %v", err)
	}
	picker.path = string(filepath.Separator)
	if _, err := r.Pick(context.Background(), scope); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("volume root was granted: %v", err)
	}
	// A broad parent that contains Ori's managed storage is not a root grant.
	picker.path, _ = filepath.EvalSymlinks(moved)
	r.guards.DataDir = filepath.Join(picker.path, "Ori Data")
	if _, err := r.Pick(context.Background(), scope); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("root containing guarded data was granted: %v", err)
	}
}
