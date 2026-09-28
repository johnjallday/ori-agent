package workspacecontinuity

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These are source-operation tests with synthetic trusted collectors, not a
// composed production worker or proof that all canonical domains are covered.
func preparedRunFixture(t *testing.T) (PreparationRun, *LocalStore) {
	t.Helper()
	db, local := localFixture(t)
	root, manifest, _ := fixture(t)
	seedCanonicalWorkspace(t, db, manifest.WorkspaceID)
	must(t, local.RegisterNative(t.Context(), manifest.WorkspaceID))
	run := PreparationRun{Local: local, WorkspaceID: manifest.WorkspaceID, Directory: root, SpoolParent: t.TempDir()}
	releases := 0
	run.Acquire = func(context.Context) (func(), error) { return func() { releases++ }, nil }
	t.Cleanup(func() {
		if releases == 0 {
			t.Error("test preparation did not release any permit")
		}
	})
	run.Collect = func(ctx context.Context, query Queryer, spool *Spool) ([]Fingerprint, []string, error) {
		var id string
		if err := query.QueryRowContext(ctx, `SELECT id FROM workspaces WHERE id=? AND owner_user_id='local' AND deleted_at IS NULL`, manifest.WorkspaceID).Scan(&id); err != nil {
			return nil, nil, err
		}
		record, err := EncodeRecord(id, map[string]any{"owner": "local"})
		if err != nil {
			return nil, nil, err
		}
		if err := spool.AddRecord(ctx, "workspace", "workspaces", record); err != nil {
			return nil, nil, err
		}
		return manifest.Files, nil, nil
	}
	run.ValidateFiles = func(ctx context.Context, directory string, files []Fingerprint) error {
		if len(files) != 1 || files[0].Path != "workspace.json" {
			return ErrIncomplete
		}
		data, err := ReadCanonicalFile(ctx, directory, files[0].Path, MaxChunkBytes)
		if err != nil {
			return err
		}
		if Digest(data) != files[0].Digest || int64(len(data)) != files[0].Bytes {
			return ErrChanged
		}
		return nil
	}
	return run, local
}

func TestPreparationRunRefusesUnsupportedWithoutPublishingAndNamesChildren(t *testing.T) {
	run, local := preparedRunFixture(t)
	if _, err := run.PrepareOnce(t.Context()); !errors.Is(err, ErrIncomplete) {
		t.Fatal("unsupported domain published", err)
	}
	status, err := local.Preparation(t.Context(), run.WorkspaceID)
	must(t, err)
	if status.Ready() || !strings.HasSuffix(status.LastError, "_adapter_unavailable") {
		t.Fatal("incomplete preparation advertised ready or hid which domain was missing", status)
	}
	if _, err := Inspect(t.Context(), run.Directory); !errors.Is(err, ErrLegacy) {
		t.Fatal("failed preparation published pointer", err)
	}
	// A parent names its physical children; each child publishes its own
	// generation and review binds every member, so the parent can be Ready.
	original := run.Collect
	run.Collect = func(ctx context.Context, q Queryer, spool *Spool) ([]Fingerprint, []string, error) {
		files, _, err := original(ctx, q, spool)
		if err != nil {
			return nil, nil, err
		}
		for _, domain := range DomainNames() {
			if domain != "workspace" {
				if err := spool.SetAvailability(ctx, domain, Empty, ""); err != nil {
					return nil, nil, err
				}
			}
		}
		return files, []string{"child-b", "child-a"}, nil
	}
	must(t, local.MarkDirty(t.Context(), run.WorkspaceID))
	inspected, err := run.PrepareOnce(t.Context())
	must(t, err)
	if len(inspected.Manifest.Children) != 2 || inspected.Manifest.Children[0] != "child-a" {
		t.Fatal("parent checkpoint did not name its children in order", inspected.Manifest.Children)
	}
}

func TestPreparationRunAcknowledgesOnlyFullyVerifiedNativeGeneration(t *testing.T) {
	run, local := preparedRunFixture(t)
	original := run.Collect
	run.Collect = func(ctx context.Context, q Queryer, spool *Spool) ([]Fingerprint, []string, error) {
		files, children, err := original(ctx, q, spool)
		if err != nil {
			return nil, nil, err
		}
		for _, domain := range DomainNames() {
			if domain != "workspace" {
				if err := spool.SetAvailability(ctx, domain, Empty, ""); err != nil {
					return nil, nil, err
				}
			}
		}
		return files, children, nil
	}
	inspected, err := run.PrepareOnce(t.Context())
	must(t, err)
	status, err := local.Preparation(t.Context(), run.WorkspaceID)
	must(t, err)
	if !status.Ready() || status.Generation != inspected.Pointer.Generation || status.PreparedSequence != status.Sequence || inspected.Manifest.SourceRevision != uint64(status.Sequence) {
		t.Fatal("published manifest was not acknowledged", status, inspected.Pointer)
	}
	// A failed retry keeps the last completed generation but never calls it
	// current merely because that pointer can still be inspected.
	run.Collect = original
	if _, err := run.PrepareOnce(t.Context()); !errors.Is(err, ErrIncomplete) {
		t.Fatal("missing domain adapter retained Ready", err)
	}
	status, err = local.Preparation(t.Context(), run.WorkspaceID)
	must(t, err)
	if status.Ready() || status.Generation != inspected.Pointer.Generation || !strings.HasSuffix(status.LastError, "_adapter_unavailable") {
		t.Fatal("previous generation misreported after failure", status)
	}
	last, err := Inspect(t.Context(), run.Directory)
	must(t, err)
	if last.Pointer != inspected.Pointer {
		t.Fatal("failed collection replaced last completed generation")
	}
	must(t, local.MarkDirty(t.Context(), run.WorkspaceID))
	status, err = local.Preparation(t.Context(), run.WorkspaceID)
	must(t, err)
	if status.Ready() {
		t.Fatal("dirty native workspace still ready")
	}
}

func TestPreparationRunRecordsDisposableSpoolFailureWithoutClaimingReady(t *testing.T) {
	run, local := preparedRunFixture(t)
	run.SpoolParent = filepath.Join(t.TempDir(), "missing-private-spool-parent")
	if _, err := run.PrepareOnce(t.Context()); err == nil {
		t.Fatal("unwritable spool parent was accepted")
	}
	status, err := local.Preparation(t.Context(), run.WorkspaceID)
	must(t, err)
	if status.Ready() || status.LastError != "preparation_failed" {
		t.Fatal("spool failure was not durable", status)
	}
	if _, err := Inspect(t.Context(), run.Directory); !errors.Is(err, ErrLegacy) {
		t.Fatal("spool failure published a pointer", err)
	}
}

func TestPreparationRunPinnedRootCannotPublishIntoReplacementFolder(t *testing.T) {
	run, local := preparedRunFixture(t)
	original := run.Collect
	run.Collect = func(ctx context.Context, q Queryer, spool *Spool) ([]Fingerprint, []string, error) {
		files, children, err := original(ctx, q, spool)
		if err != nil {
			return nil, nil, err
		}
		for _, domain := range DomainNames() {
			if domain != "workspace" {
				if err := spool.SetAvailability(ctx, domain, Empty, ""); err != nil {
					return nil, nil, err
				}
			}
		}
		return files, children, nil
	}
	base := run.ValidateFiles
	calls := 0
	detached := run.Directory + "-detached"
	run.ValidateFiles = func(ctx context.Context, dir string, files []Fingerprint) error {
		calls++
		if calls == 3 { // after pinned object writes but before pointer publication
			if err := os.Rename(dir, detached); err != nil {
				return err
			}
			if err := os.Mkdir(dir, 0700); err != nil {
				return err
			}
			data, err := os.ReadFile(filepath.Join(detached, "workspace.json")) // #nosec G304 -- synthetic source root renamed by this test
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, "workspace.json"), data, 0600); err != nil {
				return err
			}
		}
		return base(ctx, dir, files)
	}
	if _, err := run.PrepareOnce(t.Context()); !errors.Is(err, ErrChanged) {
		t.Fatal("replacement path redirected publication", err)
	}
	if calls < 3 {
		t.Fatal("did not race publication", calls)
	}
	if _, err := Inspect(t.Context(), run.Directory); !errors.Is(err, ErrLegacy) {
		t.Fatal("replacement folder received a pointer", err)
	}
	if _, err := Inspect(t.Context(), detached); !errors.Is(err, ErrIncomplete) {
		t.Fatal("old pinned root unexpectedly published", err)
	}
	status, err := local.Preparation(t.Context(), run.WorkspaceID)
	must(t, err)
	if status.Ready() || status.LastError != "source_changed" {
		t.Fatal("detached source was called Ready", status)
	}
}

func TestPreparationRunDirtyBetweenFinalFenceAndAcknowledgementStaysStale(t *testing.T) {
	run, local := preparedRunFixture(t)
	original := run.Collect
	run.Collect = func(ctx context.Context, q Queryer, spool *Spool) ([]Fingerprint, []string, error) {
		files, children, err := original(ctx, q, spool)
		if err != nil {
			return nil, nil, err
		}
		for _, domain := range DomainNames() {
			if domain != "workspace" {
				if err := spool.SetAvailability(ctx, domain, Empty, ""); err != nil {
					return nil, nil, err
				}
			}
		}
		return files, children, nil
	}
	base := run.ValidateFiles
	calls := 0
	run.ValidateFiles = func(ctx context.Context, dir string, files []Fingerprint) error {
		calls++
		if calls == 4 { // after pointer publication, between the final SQL fence and acknowledgement
			if err := local.MarkDirty(ctx, run.WorkspaceID); err != nil {
				return err
			}
		}
		return base(ctx, dir, files)
	}
	if _, err := run.PrepareOnce(t.Context()); !errors.Is(err, ErrConflict) {
		t.Fatal("dirty post-publication source was acknowledged", err)
	}
	if calls < 4 {
		t.Fatal("did not reach the post-publication check", calls)
	}
	inspected, err := Inspect(t.Context(), run.Directory)
	must(t, err)
	if inspected.Pointer.Generation == "" {
		t.Fatal("generation was not durably published")
	}
	status, err := local.Preparation(t.Context(), run.WorkspaceID)
	must(t, err)
	if status.Ready() || status.PreparedSequence == status.Sequence {
		t.Fatal("newer source was misreported Ready", status)
	}
}

func TestPreparationRunRejectsChangedSourceAfterSharedViewAndKeepsLiveWork(t *testing.T) {
	run, local := preparedRunFixture(t)
	original := run.Collect
	run.Collect = func(ctx context.Context, q Queryer, spool *Spool) ([]Fingerprint, []string, error) {
		files, children, err := original(ctx, q, spool)
		if err != nil {
			return nil, nil, err
		}
		for _, domain := range DomainNames() {
			if domain != "workspace" {
				if err := spool.SetAvailability(ctx, domain, Empty, ""); err != nil {
					return nil, nil, err
				}
			}
		}
		return files, children, nil
	}
	initial := run.ValidateFiles
	calls := 0
	run.ValidateFiles = func(ctx context.Context, dir string, files []Fingerprint) error {
		calls++
		if calls == 2 { // after the first inventory, before pointer publication
			data := []byte(`{"id":"changed","synthetic":true}`)
			if err := os.WriteFile(filepath.Join(dir, "workspace.json"), data, 0600); err != nil {
				return err
			}
		}
		return initial(ctx, dir, files)
	}
	if _, err := run.PrepareOnce(t.Context()); !errors.Is(err, ErrChanged) {
		t.Fatal("changed source was published", err)
	}
	if _, err := Inspect(t.Context(), run.Directory); !errors.Is(err, ErrLegacy) {
		t.Fatal("changed source has a current pointer", err)
	}
	status, err := local.Preparation(t.Context(), run.WorkspaceID)
	must(t, err)
	if status.Ready() || status.LastError != "source_changed" {
		t.Fatal("source change not recorded", status)
	}
	data, err := os.ReadFile(filepath.Join(run.Directory, "workspace.json")) // #nosec G304 -- synthetic test-owned root
	must(t, err)
	if string(data) != `{"id":"changed","synthetic":true}` {
		t.Fatal("preparation rewrote live authored work")
	}
}
