//go:build music_crossprocess_fence_contract

package projectlibrary

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// These opt-in contracts launch a second OS process with its own SQLite and
// FileStore handles over the same isolated Home. They assert safety: both
// independently reopened mirrors keep the parent's acknowledged write and the
// external song bytes never change, whether the child's stale write is refused
// or serialized. Run with:
//
//	go test -tags music_crossprocess_fence_contract -short -race ./internal/projectlibrary -run '^TestCrossProcess' -count=1
//
// The tag stays until the shared fence, journal and reconciliation protocol
// have passed every adversarial interleaving the checklist names; the normal
// suite never enables it.

// TestCrossProcessHomeFencePreservesAcknowledgedQueue: the child captures an
// old Home, the parent acknowledges a queue, then the child issues a plain
// stale Save through SyncStore.
func TestCrossProcessHomeFencePreservesAcknowledgedQueue(t *testing.T) {
	switch os.Getenv("ORI_MUSIC_FENCE_CHILD") {
	case "stale-save":
		runCrossProcessStaleWriter(t)
		return
	case "":
	default:
		t.Skip("child mode belongs to another contract")
	}
	fx := newCrossProcessFixture(t)
	cmd, output := fx.startChild(t, "^TestCrossProcessHomeFencePreservesAcknowledgedQueue$", "stale-save")
	waitForFenceSignal(t, fx.ready, output)
	queue, _, err := fx.store.StartActivationQueue(fx.scope, []string{"single", "alternates"}, "acknowledged-cross-process-queue")
	if err != nil {
		t.Fatal(err)
	}
	outcome := fx.releaseChild(t, cmd, output)
	sqlDoc, folderDoc := fx.reopenBothMirrors(t)
	if sqlDoc.Queue == nil || folderDoc.Queue == nil || sqlDoc.Queue.ID != queue.ID || folderDoc.Queue.ID != queue.ID ||
		sqlDoc.Revision != folderDoc.Revision || !fx.songUnchanged(t) {
		t.Fatalf("acknowledged queue lost or mirrors split after second OS process (child %s): sqlite queue=%v folder queue=%v revisions=%d/%d song unchanged=%v",
			outcome, sqlDoc.Queue != nil, folderDoc.Queue != nil, sqlDoc.Revision, folderDoc.Revision, fx.songUnchanged(t))
	}
	if outcome != "refused" {
		t.Fatalf("a stale Home snapshot from another process was persisted (%s); the fence must refuse it", outcome)
	}
}

// TestCrossProcessQueueSkipStaleWriterIsFencedByHomeVersion: two real library
// mutators. The child reads the Home inside its own queue Skip, pauses before
// its two-mirror save while the parent skips the same item, then resumes. The
// child's write is based on a Home the parent has since replaced and must be
// refused; exactly one skip receipt exists afterwards in both mirrors.
func TestCrossProcessQueueSkipStaleWriterIsFencedByHomeVersion(t *testing.T) {
	switch os.Getenv("ORI_MUSIC_FENCE_CHILD") {
	case "queue-skip":
		runCrossProcessQueueSkipper(t)
		return
	case "":
	default:
		t.Skip("child mode belongs to another contract")
	}
	fx := newCrossProcessFixture(t)
	queue, _, err := fx.store.StartActivationQueue(fx.scope, []string{"single", "alternates"}, "cross-process-skip-queue")
	if err != nil {
		t.Fatal(err)
	}
	cmd, output := fx.startChild(t, "^TestCrossProcessQueueSkipStaleWriterIsFencedByHomeVersion$", "queue-skip",
		"ORI_MUSIC_FENCE_QUEUE="+queue.ID)
	waitForFenceSignal(t, fx.ready, output)
	parentQueue, _, err := fx.store.ProgressActivationQueue(fx.scope, queue.ID, "single", "skip", "parent-skip", queue.Revision)
	if err != nil || parentQueue.Index != 1 || len(parentQueue.Skipped) != 1 {
		t.Fatalf("parent skip while the child held an older Home: %+v %v", parentQueue, err)
	}
	outcome := fx.releaseChild(t, cmd, output)
	sqlDoc, folderDoc := fx.reopenBothMirrors(t)
	if sqlDoc.Queue == nil || folderDoc.Queue == nil || sqlDoc.Revision != folderDoc.Revision ||
		sqlDoc.Queue.Index != 1 || folderDoc.Queue.Index != 1 || len(sqlDoc.Queue.Skipped) != 1 || len(folderDoc.Queue.Skipped) != 1 ||
		countOperations(sqlDoc, "queue_skip") != 1 || countOperations(folderDoc, "queue_skip") != 1 || !fx.songUnchanged(t) {
		t.Fatalf("stale child skip corrupted the queue or split the mirrors (child %s): sqlite=%+v folder=%+v song unchanged=%v",
			outcome, sqlDoc.Queue, folderDoc.Queue, fx.songUnchanged(t))
	}
	if outcome != "refused" {
		t.Fatalf("the child's stale skip reported %s; the fence must refuse a write based on a replaced Home", outcome)
	}
	if !strings.Contains(output.String(), "reload and retry") {
		t.Fatalf("child refusal did not name the fence: %s", output.String())
	}
}

func countOperations(doc Document, action string) int {
	n := 0
	for _, op := range doc.Operations {
		if op.Action == action {
			n++
		}
	}
	return n
}

type crossProcessFixture struct {
	scope      Scope
	store      *Store
	primary    *session.WorkspaceStoreAdapter
	base       string
	dbpath     string
	songPath   string
	beforeSong [32]byte
	ready      string
	release    string
	result     string
}

func newCrossProcessFixture(t *testing.T) *crossProcessFixture {
	t.Helper()
	a, scope, _, file, tree, _ := activationFixture(t)
	songPath := filepath.Join(tree.single, "Song.rpp")
	base := filepath.Dir(filepath.Dir(file.GetFilesPath(scope.HomeID)))
	dbpath := filepath.Join(t.TempDir(), "two-process.db")
	db, err := database.Open(t.Context(), &database.Config{Path: dbpath, WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	primary := session.NewWorkspaceStoreAdapter(session.NewHybridStoreWithDB(db, 10))
	home, err := file.Get(scope.HomeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := primary.Save(home); err != nil {
		t.Fatal(err)
	}
	signals := t.TempDir()
	return &crossProcessFixture{
		scope:   scope,
		store:   NewStore(workspace.NewSyncStore(primary, file)).WithProviderEvidence(a.library.providerEvidence),
		primary: primary, base: base, dbpath: dbpath, songPath: songPath, beforeSong: fileDigest(t, songPath),
		ready: filepath.Join(signals, "ready"), release: filepath.Join(signals, "release"), result: filepath.Join(signals, "result"),
	}
}

func (fx *crossProcessFixture) startChild(t *testing.T, run, mode string, extraEnv ...string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run="+run, "-test.v")
	cmd.Env = append(os.Environ(), "ORI_MUSIC_FENCE_CHILD="+mode, "ORI_MUSIC_FENCE_DB="+fx.dbpath,
		"ORI_MUSIC_FENCE_BASE="+fx.base, "ORI_MUSIC_FENCE_HOME="+fx.scope.HomeID,
		"ORI_MUSIC_FENCE_OWNER="+fx.scope.OwnerUserID, "ORI_MUSIC_FENCE_PROVIDER="+fx.scope.ProviderID,
		"ORI_MUSIC_FENCE_PROGRAM="+fx.scope.ProgramID,
		"ORI_MUSIC_FENCE_READY="+fx.ready, "ORI_MUSIC_FENCE_RELEASE="+fx.release, "ORI_MUSIC_FENCE_RESULT="+fx.result)
	cmd.Env = append(cmd.Env, extraEnv...)
	output := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return cmd, output
}

func (fx *crossProcessFixture) releaseChild(t *testing.T, cmd *exec.Cmd, output *bytes.Buffer) string {
	t.Helper()
	if err := os.WriteFile(fx.release, []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child exited before recording its result: %v %s", err, output.String())
	}
	outcome, err := os.ReadFile(fx.result) // #nosec G304 -- server-minted disposable test file, not a request path.
	if err != nil || (string(outcome) != "refused" && string(outcome) != "saved") {
		t.Fatalf("child did not report a bounded outcome: %q %v %s", outcome, err, output.String())
	}
	return strings.TrimSpace(string(outcome))
}

func (fx *crossProcessFixture) reopenBothMirrors(t *testing.T) (Document, Document) {
	t.Helper()
	folder, err := workspace.NewFileStore(fx.base)
	if err != nil {
		t.Fatal(err)
	}
	sqlDoc, sqlErr := NewStore(fx.primary).Read(fx.scope)
	folderDoc, folderErr := NewStore(folder).Read(fx.scope)
	if sqlErr != nil || folderErr != nil {
		t.Fatalf("reopened mirrors unreadable: sqlite=%v folder=%v", sqlErr, folderErr)
	}
	return sqlDoc, folderDoc
}

func (fx *crossProcessFixture) songUnchanged(t *testing.T) bool {
	t.Helper()
	return fileDigest(t, fx.songPath) == fx.beforeSong
}

func waitForFenceSignal(t *testing.T, ready string, output *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("child never signaled readiness: %s", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func childScope() Scope {
	return Scope{OwnerUserID: os.Getenv("ORI_MUSIC_FENCE_OWNER"), HomeID: os.Getenv("ORI_MUSIC_FENCE_HOME"),
		ProviderID: os.Getenv("ORI_MUSIC_FENCE_PROVIDER"), ProgramID: os.Getenv("ORI_MUSIC_FENCE_PROGRAM")}
}

func childStores(t *testing.T) *workspace.SyncStore {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{Path: os.Getenv("ORI_MUSIC_FENCE_DB"), WALMode: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	file, err := workspace.NewFileStore(os.Getenv("ORI_MUSIC_FENCE_BASE"))
	if err != nil {
		t.Fatal(err)
	}
	return workspace.NewSyncStore(session.NewWorkspaceStoreAdapter(session.NewHybridStoreWithDB(db, 10)), file)
}

func signalReadyAndWait(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(os.Getenv("ORI_MUSIC_FENCE_READY"), []byte("ready"), 0o600); err != nil { // #nosec G304 -- server-minted disposable test file, not a request path.
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(os.Getenv("ORI_MUSIC_FENCE_RELEASE")); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("parent never released the child")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func recordChildOutcome(t *testing.T, err error) {
	t.Helper()
	outcome := "saved"
	if err != nil {
		outcome = "refused"
		t.Logf("child write refused: %v", err)
	}
	if err := os.WriteFile(os.Getenv("ORI_MUSIC_FENCE_RESULT"), []byte(outcome), 0o600); err != nil { // #nosec G304 -- server-minted disposable test file, not a request path.
		t.Fatal(err)
	}
}

func runCrossProcessStaleWriter(t *testing.T) {
	store := childStores(t)
	old, err := store.Get(os.Getenv("ORI_MUSIC_FENCE_HOME"))
	if err != nil {
		t.Fatal(err)
	}
	signalReadyAndWait(t)
	recordChildOutcome(t, store.Save(old))
}

// pauseBetweenReadAndSave lets the child run a real library mutator whose
// callback has already read the Home before the parent's write lands.
type pauseBetweenReadAndSave struct {
	*workspace.SyncStore
	t *testing.T
}

func (p *pauseBetweenReadAndSave) Update(wsID string, fn func(*workspace.Workspace) error) error {
	return p.SyncStore.Update(wsID, func(ws *workspace.Workspace) error {
		if err := fn(ws); err != nil {
			return err
		}
		signalReadyAndWait(p.t)
		return nil
	})
}

func runCrossProcessQueueSkipper(t *testing.T) {
	scope := childScope()
	paused := &pauseBetweenReadAndSave{SyncStore: childStores(t), t: t}
	library := NewStore(paused).WithProviderEvidence(func(Scope, *workspace.Workspace) bool { return true })
	view, err := library.CurrentActivationQueue(scope)
	if err != nil || view.Queue == nil || view.Queue.ID != os.Getenv("ORI_MUSIC_FENCE_QUEUE") {
		t.Fatalf("child could not read the parent's queue: %+v %v", view, err)
	}
	_, _, err = library.ProgressActivationQueue(scope, view.Queue.ID, "single", "skip", "child-skip", view.Queue.Revision)
	if err != nil && !errors.Is(err, ErrConflict) {
		t.Fatalf("child skip failed for a reason other than a fenced conflict: %v", err)
	}
	recordChildOutcome(t, err)
}
