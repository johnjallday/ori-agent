//go:build darwin || linux

package filejanitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/directoryid"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// countingMover wraps the real conditional mover so tests can count what reached
// it and simulate a process that dies around a move or a folder creation.
type countingMover struct {
	ConditionalMover
	mu              sync.Mutex
	moves, folders  int
	exitBeforeMove  bool
	exitAfterMove   bool
	exitAfterFolder bool
	folderErr       error
}

func (m *countingMover) MoveIfUnchanged(ctx context.Context, move ConditionalMove) (Fingerprint, error) {
	m.mu.Lock()
	m.moves++
	exitBefore, exitAfter := m.exitBeforeMove, m.exitAfterMove
	m.mu.Unlock()
	if exitBefore {
		runtime.Goexit()
	}
	after, err := m.ConditionalMover.MoveIfUnchanged(ctx, move)
	if exitAfter {
		runtime.Goexit()
	}
	return after, err
}

func (m *countingMover) MakeFolder(ctx context.Context, root, rootID string, parent DirRef, name string) (DirRef, error) {
	m.mu.Lock()
	m.folders++
	folderErr, exitAfter := m.folderErr, m.exitAfterFolder
	m.mu.Unlock()
	if folderErr != nil {
		return DirRef{}, folderErr
	}
	made, err := m.ConditionalMover.MakeFolder(ctx, root, rootID, parent, name)
	if exitAfter {
		runtime.Goexit()
	}
	return made, err
}

func (m *countingMover) counts() (moves, folders int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.moves, m.folders
}

type docsFixture struct {
	t          *testing.T
	s          *Service
	batch      JanitorBatch
	candidates []JanitorCandidate
	root       string
	rootID     string
	real       *platformMover
	mover      *countingMover
}

func newDocsFixture(t *testing.T, names ...string) *docsFixture {
	t.Helper()
	s, batch, candidates, root := planFixture(t, names...)
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	f := &docsFixture{t: t, s: s, batch: batch, candidates: candidates, root: root, rootID: directoryid.Witness(info), real: &platformMover{}}
	if err := f.real.Supported(root, f.rootID); err != nil {
		t.Skipf("exclusive rename is not supported under %s: %v", os.TempDir(), err)
	}
	f.mover = &countingMover{ConditionalMover: f.real}
	s.SetConditionalMover(f.mover)
	return f
}

func (f *docsFixture) abs(rel string) string { return filepath.Join(f.root, filepath.FromSlash(rel)) }

func (f *docsFixture) mkdir(rel string) {
	f.t.Helper()
	if err := os.MkdirAll(f.abs(rel), 0o750); err != nil {
		f.t.Fatal(err)
	}
}

func (f *docsFixture) write(rel, content string) {
	f.t.Helper()
	if err := os.WriteFile(f.abs(rel), []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *docsFixture) read(rel string) string {
	f.t.Helper()
	b, err := os.ReadFile(f.abs(rel))
	if err != nil {
		f.t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func (f *docsFixture) exists(rel string) bool {
	_, err := os.Lstat(f.abs(rel))
	return err == nil
}

func (f *docsFixture) inode(rel string) string {
	f.t.Helper()
	fp, err := currentFingerprintAt(filepath.Dir(f.abs(rel)), filepath.Base(rel))
	if err != nil {
		f.t.Fatal(err)
	}
	return fp.FileID
}

// tree is every entry under the root with its bytes, to prove nothing changed.
func (f *docsFixture) tree() map[string]string {
	f.t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(f.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == f.root {
			return err
		}
		rel, _ := filepath.Rel(f.root, p)
		if d.Type()&fs.ModeSymlink != 0 {
			target, _ := os.Readlink(p)
			out[rel] = "<link to " + target + ">"
			return nil
		}
		if d.IsDir() {
			out[rel] = "<dir>"
			return nil
		}
		b, err := os.ReadFile(p)
		out[rel] = string(b)
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *docsFixture) match(names ...string) (DocumentsSelection, error) {
	ls, err := workspace.ListContainedEntries(f.root, "", 1)
	if err != nil {
		return DocumentsSelection{}, err
	}
	versions := map[string]workspace.SourceVersion{}
	for _, e := range ls.Entries {
		if e.Kind == "file" {
			versions[e.Name] = e.Version
		}
	}
	files := make([]ReviewedLooseFile, 0, len(names))
	for _, name := range names {
		files = append(files, ReviewedLooseFile{Name: name, Version: versions[name]})
	}
	sel, err := f.s.MatchDocumentsSelection(f.t.Context(), "local", "ws-1", f.root, f.rootID, files)
	sel.Key = "view-1|" + strings.Join(names, ",")
	return sel, err
}

func (f *docsFixture) selection(names ...string) DocumentsSelection {
	f.t.Helper()
	sel, err := f.match(names...)
	if err != nil || sel.State != "matched" {
		f.t.Fatalf("selection %v: %+v %v", names, sel, err)
	}
	return sel
}

func (f *docsFixture) reselect(names ...string) func(context.Context) (DocumentsSelection, error) {
	return func(context.Context) (DocumentsSelection, error) { return f.match(names...) }
}

func (f *docsFixture) preview(dest DocumentsDestination, names ...string) DocumentsPreview {
	f.t.Helper()
	p, err := f.s.PreviewDocumentsFiling(f.t.Context(), "local", "ws-1", dest, f.selection(names...))
	if err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f *docsFixture) readyPreview(dest DocumentsDestination, names ...string) DocumentsPreview {
	f.t.Helper()
	p := f.preview(dest, names...)
	if p.State != documentsPreviewReady || p.Digest == "" {
		f.t.Fatalf("preview is not ready: %+v", p)
	}
	return p
}

func decisionFor(key string) string { return "decision-" + key }

func (f *docsFixture) pressWith(dest DocumentsDestination, key, digest string, names ...string) (DocumentsFilingResult, error) {
	req := DocumentsFileRequest{Destination: dest, Digest: digest, ClientKey: key, DecisionID: decisionFor(key)}
	return f.s.FileDocumentsSelection(f.t.Context(), "local", "ws-1", req, f.reselect(names...))
}

func (f *docsFixture) press(dest DocumentsDestination, key string, names ...string) (DocumentsFilingResult, error) {
	f.t.Helper()
	return f.pressWith(dest, key, f.readyPreview(dest, names...).Digest, names...)
}

func (f *docsFixture) mustPress(dest DocumentsDestination, key string, names ...string) DocumentsFilingResult {
	f.t.Helper()
	result, err := f.press(dest, key, names...)
	if err != nil {
		f.t.Fatalf("press: %v", err)
	}
	return result
}

const (
	keyA = "key-aaaaaaaaaaaaaaaa"
	keyB = "key-bbbbbbbbbbbbbbbb"
)

func TestDocumentsPreview_WritesNothing(t *testing.T) {
	f := newDocsFixture(t, "estimate.pdf", "other.pdf")
	f.mkdir("Taxes")
	before := f.tree()
	stateBefore, err := f.s.store.LoadScanState("ws-1")
	if err != nil {
		t.Fatal(err)
	}

	existing := f.preview(DocumentsDestination{Folder: "Taxes"}, "estimate.pdf")
	created := f.preview(DocumentsDestination{Folder: "Taxes", New: "2025"}, "estimate.pdf", "other.pdf")
	if existing.State != documentsPreviewReady || created.State != documentsPreviewReady || len(created.Create) != 1 || created.Create[0] != "Taxes/2025" {
		t.Fatalf("previews: %+v %+v", existing, created)
	}
	if !reflect.DeepEqual(f.tree(), before) {
		t.Fatal("a preview changed the folder")
	}
	stateAfter, err := f.s.store.LoadScanState("ws-1")
	if err != nil || !reflect.DeepEqual(stateBefore, stateAfter) {
		t.Fatalf("a preview changed the scan state: %v", err)
	}
	if f.exists("Taxes/2025") {
		t.Fatal("a preview made a folder")
	}
}

func TestDocumentsPreview_ShowsEveryNameACollisionAndWhatWillBeCreated(t *testing.T) {
	f := newDocsFixture(t, "estimate.pdf", "other.pdf")
	f.mkdir("Taxes")
	f.write("Taxes/estimate.pdf", "already filed")

	p := f.readyPreview(DocumentsDestination{Folder: "Taxes"}, "estimate.pdf", "other.pdf")
	if len(p.Items) != 2 || p.Folder != "Taxes" || len(p.Create) != 0 {
		t.Fatalf("preview: %+v", p)
	}
	byName := map[string]DocumentsPreviewItem{}
	for _, item := range p.Items {
		byName[item.Name] = item
	}
	if got := byName["estimate.pdf"]; got.FinalName != "estimate (2).pdf" || !got.Renamed || got.To != "Taxes/estimate (2).pdf" {
		t.Fatalf("the taken name must be adjusted and shown: %+v", got)
	}
	if got := byName["other.pdf"]; got.FinalName != "other.pdf" || got.Renamed || got.To != "Taxes/other.pdf" {
		t.Fatalf("an untouched name: %+v", got)
	}
	again := f.readyPreview(DocumentsDestination{Folder: "Taxes"}, "estimate.pdf", "other.pdf")
	if again.Digest != p.Digest {
		t.Fatal("the digest must be stable for the same plan")
	}
	if other := f.readyPreview(DocumentsDestination{Folder: "Taxes", New: "2025"}, "estimate.pdf", "other.pdf"); other.Digest == p.Digest {
		t.Fatal("a different destination must change the digest")
	}
	if fewer := f.readyPreview(DocumentsDestination{Folder: "Taxes"}, "estimate.pdf"); fewer.Digest == p.Digest {
		t.Fatal("a different selection must change the digest")
	}
}

func TestDocumentsPreview_RefusesDestinationsThatAreNotAllowed(t *testing.T) {
	f := newDocsFixture(t, "estimate.pdf")
	f.mkdir("Taxes/2024/Deep")
	f.mkdir("Filed/Documents")
	f.write("Taxes/note.txt", "a file")
	if err := os.Symlink("Taxes", f.abs("Linked")); err != nil {
		t.Fatal(err)
	}
	cases := map[string]DocumentsDestination{
		"the managed folder itself": {Folder: ""},
		"three levels deep":         {Folder: "Taxes/2024/Deep"},
		"the filing folder":         {Folder: "Filed/Documents"},
		"the filing folder in case": {Folder: "filed"},
		"a new filing folder":       {Folder: "", New: "Filed"},
		"a missing folder":          {Folder: "Nowhere"},
		"a parent segment":          {Folder: "../Taxes"},
		"a new name that exists":    {Folder: "", New: "Taxes"},
		"a new name of a file":      {Folder: "Taxes", New: "note.txt"},
		"a new folder too deep":     {Folder: "Taxes/2024", New: "2025"},
		"a link":                    {Folder: "Linked"},
		"an unusable new name":      {Folder: "Taxes", New: "a/b"},
	}
	for name, dest := range cases {
		t.Run(name, func(t *testing.T) {
			before := f.tree()
			p := f.preview(dest, "estimate.pdf")
			if p.State != documentsPreviewDestination || p.Digest != "" {
				t.Fatalf("want a refused destination, got %+v", p)
			}
			if !reflect.DeepEqual(f.tree(), before) {
				t.Fatal("a refused preview changed the folder")
			}
		})
	}
}

// Whether two names that differ only in case collide is the filesystem's call:
// APFS says yes, most Linux disks say no. Ori asks the filesystem and never folds
// names itself, so the preview must agree with whichever disk it is on.
func TestDocumentsPreview_ANewNameThatDiffersOnlyInCaseFollowsTheFilesystem(t *testing.T) {
	f := newDocsFixture(t, "estimate.pdf")
	f.mkdir("Taxes")
	caseInsensitive := f.exists("taxes")
	before := f.tree()

	p := f.preview(DocumentsDestination{Folder: "", New: "taxes"}, "estimate.pdf")
	if caseInsensitive {
		if p.State != documentsPreviewDestination || p.Digest != "" {
			t.Fatalf("this disk treats Taxes and taxes as one name, so the new folder must be refused: %+v", p)
		}
	} else if p.State != documentsPreviewReady || len(p.Create) != 1 || p.Create[0] != "taxes" || p.Folder != "taxes" {
		t.Fatalf("this disk keeps Taxes and taxes apart, so a new folder named taxes is allowed: %+v", p)
	}
	if !reflect.DeepEqual(f.tree(), before) {
		t.Fatal("a preview changed the folder")
	}
}

func TestDocumentsFiling_FilesIntoExistingNewAndNestedFolders(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(f *docsFixture)
		dest   DocumentsDestination
		folder string
		create bool
	}{
		{"existing top-level folder", func(f *docsFixture) { f.mkdir("Taxes") }, DocumentsDestination{Folder: "Taxes"}, "Taxes", false},
		{"existing nested folder", func(f *docsFixture) { f.mkdir("Taxes/2024") }, DocumentsDestination{Folder: "Taxes/2024"}, "Taxes/2024", false},
		{"new folder in the managed folder", func(f *docsFixture) {}, DocumentsDestination{Folder: "", New: "Receipts"}, "Receipts", true},
		{"new folder in an existing folder", func(f *docsFixture) { f.mkdir("Taxes") }, DocumentsDestination{Folder: "Taxes", New: "2025"}, "Taxes/2025", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDocsFixture(t, "estimate.pdf", "other.pdf")
			tc.setup(f)
			wantBytes := map[string]string{"estimate.pdf": f.read("estimate.pdf"), "other.pdf": f.read("other.pdf")}
			wantInode := map[string]string{"estimate.pdf": f.inode("estimate.pdf"), "other.pdf": f.inode("other.pdf")}

			result := f.mustPress(tc.dest, keyA, "estimate.pdf", "other.pdf")
			if result.Filed != 2 || result.Failed != 0 || result.Stale != 0 || result.NotAttempted != 0 || result.Interrupted || result.Replayed {
				t.Fatalf("result: %+v", result)
			}
			if result.Folder != tc.folder || result.FolderCreated != tc.create || result.FolderUnconfirmed || result.DecisionID != decisionFor(keyA) {
				t.Fatalf("folder facts: %+v", result)
			}
			for name, content := range wantBytes {
				rel := tc.folder + "/" + name
				if f.read(rel) != content || f.inode(rel) != wantInode[name] {
					t.Fatalf("%s: not the same file after the move", name)
				}
				if f.exists(name) {
					t.Fatalf("%s is still in the managed folder", name)
				}
			}
			for _, outcome := range result.Outcomes {
				if outcome.Operation != OperationPlace || outcome.Result != ResultApplied || !outcome.Undoable {
					t.Fatalf("outcome: %+v", outcome)
				}
			}

			// The receipt groups by the chosen folder and offers Undo.
			receipt, err := f.s.ReadFiling(t.Context(), "local", "ws-1", result.FilingID, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(receipt.ByDestination) != 1 || receipt.ByDestination[0].Destination != tc.folder || receipt.ByDestination[0].Count != 2 || !receipt.UndoAvailable {
				t.Fatalf("receipt: %+v", receipt)
			}
		})
	}
}

func TestDocumentsFiling_NeverReplacesAnOccupiedName(t *testing.T) {
	t.Run("it appears after the preview", func(t *testing.T) {
		f := newDocsFixture(t, "estimate.pdf")
		f.mkdir("Taxes")
		dest := DocumentsDestination{Folder: "Taxes"}
		digest := f.readyPreview(dest, "estimate.pdf").Digest
		f.write("Taxes/estimate.pdf", "arrived after the preview")
		before := f.tree()

		result, err := f.pressWith(dest, keyA, digest, "estimate.pdf")
		if !errors.Is(err, ErrReviewPlanChanged) || result.Preview == nil || result.Preview.Items[0].FinalName != "estimate (2).pdf" {
			t.Fatalf("want a changed plan with the fresh names, got %+v %v", result, err)
		}
		if !reflect.DeepEqual(f.tree(), before) {
			t.Fatal("nothing may move when the plan changed")
		}
		if moves, _ := f.mover.counts(); moves != 0 {
			t.Fatalf("the mover must not be reached, got %d", moves)
		}
	})
	t.Run("it appears after admission, before the rename", func(t *testing.T) {
		f := newDocsFixture(t, "estimate.pdf")
		f.mkdir("Taxes")
		source := f.read("estimate.pdf")
		f.real.hook = func(stage moveStage) {
			if stage == stageBeforeRename {
				f.write("Taxes/estimate (2).pdf", "arrived at the last instant")
			}
		}
		// Arrange the same name the preview will choose.
		f.write("Taxes/estimate.pdf", "already there")
		result := f.mustPress(DocumentsDestination{Folder: "Taxes"}, keyA, "estimate.pdf")
		if result.Filed != 0 || result.Stale != 1 {
			t.Fatalf("the taken name must leave the file where it was: %+v", result)
		}
		if f.read("estimate.pdf") != source || f.read("Taxes/estimate (2).pdf") != "arrived at the last instant" || f.read("Taxes/estimate.pdf") != "already there" {
			t.Fatal("a file was replaced or lost")
		}
	})
}

func TestDocumentsFiling_ARefusedPlanMovesNothing(t *testing.T) {
	t.Run("the source changed before the press", func(t *testing.T) {
		f := newDocsFixture(t, "estimate.pdf")
		f.mkdir("Taxes")
		dest := DocumentsDestination{Folder: "Taxes"}
		digest := f.readyPreview(dest, "estimate.pdf").Digest
		f.write("estimate.pdf", "edited after the preview, and longer")

		result, err := f.pressWith(dest, keyA, digest, "estimate.pdf")
		if !errors.Is(err, ErrReviewPlanChanged) || result.Preview == nil || result.Preview.State != "changed" {
			t.Fatalf("want changed, got %+v %v", result, err)
		}
		if f.exists("Taxes/estimate.pdf") || !f.exists("estimate.pdf") {
			t.Fatal("the file moved")
		}
	})
	t.Run("the source changed after admission", func(t *testing.T) {
		f := newDocsFixture(t, "estimate.pdf")
		f.mkdir("Taxes")
		f.real.hook = func(stage moveStage) {
			if stage == stageBeforeRename {
				f.write("estimate.pdf", "edited at the last instant, and longer")
			}
		}
		result := f.mustPress(DocumentsDestination{Folder: "Taxes"}, keyA, "estimate.pdf")
		if result.Filed != 0 || result.Stale != 1 || f.exists("Taxes/estimate.pdf") || !f.exists("estimate.pdf") {
			t.Fatalf("a changed source must stay put: %+v", result)
		}
	})
	t.Run("the destination folder was replaced before the press", func(t *testing.T) {
		f := newDocsFixture(t, "estimate.pdf")
		f.mkdir("Taxes")
		dest := DocumentsDestination{Folder: "Taxes"}
		digest := f.readyPreview(dest, "estimate.pdf").Digest
		if err := os.Mkdir(f.abs("Taxes.fresh"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(f.abs("Taxes"), f.abs("Taxes.old")); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(f.abs("Taxes.fresh"), f.abs("Taxes")); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pressWith(dest, keyA, digest, "estimate.pdf"); !errors.Is(err, ErrReviewPlanChanged) {
			t.Fatalf("a different folder under the same name must change the plan, got %v", err)
		}
		if f.exists("Taxes/estimate.pdf") || !f.exists("estimate.pdf") {
			t.Fatal("the file moved")
		}
	})
	t.Run("the destination folder was replaced after admission", func(t *testing.T) {
		f := newDocsFixture(t, "estimate.pdf")
		f.mkdir("Taxes")
		f.real.hook = func(stage moveStage) {
			if stage == stageBeforeRename {
				_ = os.Mkdir(f.abs("Taxes.fresh"), 0o750)
				_ = os.Rename(f.abs("Taxes"), f.abs("Taxes.old"))
				_ = os.Rename(f.abs("Taxes.fresh"), f.abs("Taxes"))
			}
		}
		result := f.mustPress(DocumentsDestination{Folder: "Taxes"}, keyA, "estimate.pdf")
		if result.Filed != 0 || result.Stale != 1 || !f.exists("estimate.pdf") || f.exists("Taxes.old/estimate.pdf") || f.exists("Taxes/estimate.pdf") {
			t.Fatalf("the file must be put back: %+v", result)
		}
	})
	t.Run("the new folder appeared before the press", func(t *testing.T) {
		f := newDocsFixture(t, "estimate.pdf")
		dest := DocumentsDestination{Folder: "", New: "Receipts"}
		digest := f.readyPreview(dest, "estimate.pdf").Digest
		f.mkdir("Receipts")
		f.write("Receipts/mine.txt", "someone else's")

		if _, err := f.pressWith(dest, keyA, digest, "estimate.pdf"); !errors.Is(err, ErrReviewPlanChanged) {
			t.Fatalf("want a changed plan, got %v", err)
		}
		if f.exists("Receipts/estimate.pdf") || f.read("Receipts/mine.txt") != "someone else's" {
			t.Fatal("an existing folder was adopted")
		}
	})
	t.Run("the new folder cannot be made after admission", func(t *testing.T) {
		f := newDocsFixture(t, "estimate.pdf", "other.pdf")
		f.mover.folderErr = ErrMoveDestinationOccupied
		result := f.mustPress(DocumentsDestination{Folder: "", New: "Receipts"}, keyA, "estimate.pdf", "other.pdf")
		if result.Filed != 0 || result.Failed != 2 || result.FolderCreated || f.exists("Receipts") {
			t.Fatalf("nothing may move when the folder is not made: %+v", result)
		}
		if moves, folders := f.mover.counts(); moves != 0 || folders != 1 {
			t.Fatalf("one attempt to make the folder and no move without it, got %d moves and %d folder attempts", moves, folders)
		}
	})
}

func TestDocumentsFiling_ANestedExampleFolderIsNeverOverwritten(t *testing.T) {
	f := newDocsFixture(t, "inv.pdf")
	f.mkdir("Maple Street/Invoices")
	f.write("Maple Street/Invoices/inv.pdf", "the example the review learned from")

	result := f.mustPress(DocumentsDestination{Folder: "Maple Street/Invoices"}, keyA, "inv.pdf")
	if result.Filed != 1 || f.read("Maple Street/Invoices/inv.pdf") != "the example the review learned from" || !f.exists("Maple Street/Invoices/inv (2).pdf") {
		t.Fatalf("the example must stay and the new file get another name: %+v", result)
	}
}

func TestDocumentsFiling_UnrelatedTicksAndTrashAreUntouched(t *testing.T) {
	f := newDocsFixture(t, "estimate.pdf", "other.pdf", "trash.pdf")
	f.mkdir("Taxes")
	editDraft(t, f.s, f.batch.ID, "mark_trash", func(e *ReviewDraftEdit) { e.CandidateIDs = []string{candidateNamed(t, f.candidates, "trash.pdf").ID} })
	draftBefore, err := f.s.ReadReviewDraft(t.Context(), "local", "ws-1", f.batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	stateBefore, _ := f.s.store.LoadScanState("ws-1")

	result := f.mustPress(DocumentsDestination{Folder: "Taxes"}, keyA, "estimate.pdf")
	if result.Filed != 1 {
		t.Fatalf("result: %+v", result)
	}
	if !f.exists("other.pdf") || !f.exists("trash.pdf") || f.exists("Taxes/other.pdf") || f.exists("Taxes/trash.pdf") {
		t.Fatal("a file outside the selection moved")
	}
	stateAfter, _ := f.s.store.LoadScanState("ws-1")
	for _, name := range []string{"other.pdf", "trash.pdf"} {
		id := candidateNamed(t, f.candidates, name).ID
		before, _ := stateBefore.Candidate(id)
		after, _ := stateAfter.Candidate(id)
		if before.State != after.State || before.DecisionCategory != after.DecisionCategory {
			t.Fatalf("%s: candidate changed %+v -> %+v", name, before, after)
		}
	}
	draftAfter, err := f.s.ReadReviewDraft(t.Context(), "local", "ws-1", f.batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	keep := func(v ReviewDraftView) []ReviewChoiceView {
		var out []ReviewChoiceView
		for _, c := range v.Choices {
			if c.CandidateID != candidateNamed(t, f.candidates, "estimate.pdf").ID {
				out = append(out, c)
			}
		}
		return out
	}
	if !reflect.DeepEqual(keep(draftBefore), keep(draftAfter)) {
		t.Fatal("unrelated saved ticks or the Trash choice changed")
	}
	// A file marked for the Trash is never placed, even if selected by hand.
	sel, _ := f.match("trash.pdf")
	if sel.State == "matched" {
		t.Fatalf("a Trash-marked file must not match: %+v", sel)
	}
}

func TestDocumentsFiling_ARetryReportsTheFirstResultAndMovesNothingTwice(t *testing.T) {
	t.Run("sequential", func(t *testing.T) {
		f := newDocsFixture(t, "estimate.pdf", "other.pdf")
		f.mkdir("Taxes")
		dest := DocumentsDestination{Folder: "Taxes"}
		digest := f.readyPreview(dest, "estimate.pdf", "other.pdf").Digest
		first, err := f.pressWith(dest, keyA, digest, "estimate.pdf", "other.pdf")
		if err != nil || first.Filed != 2 {
			t.Fatalf("first: %+v %v", first, err)
		}
		again, err := f.pressWith(dest, keyA, digest, "estimate.pdf", "other.pdf")
		if err != nil || !again.Replayed || again.FilingID != first.FilingID || again.Filed != 2 || again.DecisionID != first.DecisionID {
			t.Fatalf("replay: %+v %v", again, err)
		}
		again.Replayed = false
		if !reflect.DeepEqual(again.Outcomes, first.Outcomes) {
			t.Fatal("a replay must be identical to the first answer")
		}
		if moves, _ := f.mover.counts(); moves != 2 {
			t.Fatalf("each file moves once, got %d", moves)
		}
	})
	t.Run("a second press while the first is running", func(t *testing.T) {
		f := newDocsFixture(t, "estimate.pdf")
		f.mkdir("Taxes")
		dest := DocumentsDestination{Folder: "Taxes"}
		digest := f.readyPreview(dest, "estimate.pdf").Digest
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		f.real.hook = func(stage moveStage) {
			if stage == stageBeforeRename {
				once.Do(func() { close(entered) })
				<-release
			}
		}
		type answer struct {
			r   DocumentsFilingResult
			err error
		}
		results := make(chan answer, 2)
		go func() { r, err := f.pressWith(dest, keyA, digest, "estimate.pdf"); results <- answer{r, err} }()
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("the first press never reached the move")
		}
		go func() { r, err := f.pressWith(dest, keyA, digest, "estimate.pdf"); results <- answer{r, err} }()
		time.Sleep(50 * time.Millisecond) // the second press is now waiting on the root lock
		close(release)

		var replayed int
		for range 2 {
			a := <-results
			if a.err != nil || a.r.Filed != 1 {
				t.Fatalf("answer: %+v %v", a.r, a.err)
			}
			if a.r.Replayed {
				replayed++
			}
		}
		if replayed != 1 {
			t.Fatalf("exactly one answer is the replay, got %d", replayed)
		}
		if moves, _ := f.mover.counts(); moves != 1 {
			t.Fatalf("the file moves once, got %d", moves)
		}
	})
}

func TestDocumentsFiling_AReusedKeyForADifferentFilingIsRefused(t *testing.T) {
	f := newDocsFixture(t, "estimate.pdf", "other.pdf")
	f.mkdir("Taxes")
	f.mkdir("Receipts")
	first := f.mustPress(DocumentsDestination{Folder: "Taxes"}, keyA, "estimate.pdf")
	if first.Filed != 1 {
		t.Fatalf("first: %+v", first)
	}
	other := DocumentsDestination{Folder: "Receipts"}
	digest := f.readyPreview(other, "other.pdf").Digest
	before := f.tree()
	if _, err := f.pressWith(other, keyA, digest, "other.pdf"); !errors.Is(err, ErrFilingKeyReused) {
		t.Fatalf("the same key for different intent must be refused, got %v", err)
	}
	if !reflect.DeepEqual(f.tree(), before) {
		t.Fatal("a refused press moved something")
	}
	// A different key is a different filing, so the same preview files under it.
	if second := f.mustPress(other, keyB, "other.pdf"); second.Filed != 1 || second.Replayed || second.FilingID == first.FilingID || second.DecisionID == first.DecisionID {
		t.Fatalf("a new key must be a new filing: %+v", second)
	}
	before = f.tree()

	// The legacy endpoint must not replay a named filing either.
	f.s.SetMover(&realMover{})
	_, err := f.s.FileReviewPlan(t.Context(), "local", "ws-1", f.batch.ID, FileRequest{
		ExpectedRevision: strings.Repeat("a", 64), ExpectedGroupRevision: strings.Repeat("b", 64), PlanDigest: strings.Repeat("c", 64), ClientKey: keyA,
	})
	if !errors.Is(err, ErrReviewConflict) {
		t.Fatalf("the legacy press must refuse a named filing's key, got %v", err)
	}
	if !reflect.DeepEqual(f.tree(), before) {
		t.Fatal("the legacy press moved something")
	}
}

func TestDocumentsFiling_AnInterruptedRunIsReconciledFromTheDisk(t *testing.T) {
	t.Run("the process dies before the move", func(t *testing.T) {
		f := newDocsFixture(t, "estimate.pdf")
		f.mkdir("Taxes")
		dest := DocumentsDestination{Folder: "Taxes"}
		digest := f.readyPreview(dest, "estimate.pdf").Digest
		f.mover.exitBeforeMove = true
		runInterrupted(t, func() { _, _ = f.pressWith(dest, keyA, digest, "estimate.pdf") })
		movesBefore, _ := f.mover.counts()

		replay, err := f.pressWith(dest, keyA, digest, "estimate.pdf")
		if err != nil || !replay.Replayed || !replay.Interrupted {
			t.Fatalf("replay: %+v %v", replay, err)
		}
		if replay.Filed != 0 || replay.Stale != 1 || replay.NotAttempted != 0 || !f.exists("estimate.pdf") || f.exists("Taxes/estimate.pdf") {
			t.Fatalf("an unmoved file must be reported as not moved: %+v", replay)
		}
		if moves, _ := f.mover.counts(); moves != movesBefore {
			t.Fatal("reconciling must never call the mover")
		}
		state, _ := f.s.store.LoadScanState("ws-1")
		if c, _ := state.Candidate(candidateNamed(t, f.candidates, "estimate.pdf").ID); c.State != CandidatePending {
			t.Fatalf("an unmoved file goes back to pending, got %q", c.State)
		}
	})
	t.Run("the process dies after the move", func(t *testing.T) {
		f := newDocsFixture(t, "estimate.pdf")
		f.mkdir("Taxes")
		dest := DocumentsDestination{Folder: "Taxes"}
		digest := f.readyPreview(dest, "estimate.pdf").Digest
		content := f.read("estimate.pdf")
		f.mover.exitAfterMove = true
		runInterrupted(t, func() { _, _ = f.pressWith(dest, keyA, digest, "estimate.pdf") })
		movesBefore, _ := f.mover.counts()

		replay, err := f.pressWith(dest, keyA, digest, "estimate.pdf")
		if err != nil || !replay.Replayed || replay.Filed != 1 || replay.Stale != 0 {
			t.Fatalf("a file that moved must be reported as filed: %+v %v", replay, err)
		}
		if f.read("Taxes/estimate.pdf") != content || f.exists("estimate.pdf") {
			t.Fatal("the file is not where the journal says")
		}
		if moves, _ := f.mover.counts(); moves != movesBefore {
			t.Fatal("reconciling must never call the mover")
		}
	})
	t.Run("two files, the process dies on the second", func(t *testing.T) {
		f := newDocsFixture(t, "estimate.pdf", "other.pdf")
		f.mkdir("Taxes")
		dest := DocumentsDestination{Folder: "Taxes"}
		digest := f.readyPreview(dest, "estimate.pdf", "other.pdf").Digest
		var seen int
		f.real.hook = func(stage moveStage) {
			if stage == stageBeforeRename {
				seen++
				if seen == 2 {
					runtime.Goexit()
				}
			}
		}
		runInterrupted(t, func() { _, _ = f.pressWith(dest, keyA, digest, "estimate.pdf", "other.pdf") })
		replay, err := f.pressWith(dest, keyA, digest, "estimate.pdf", "other.pdf")
		if err != nil || replay.Filed != 1 || replay.Stale != 1 || !replay.Interrupted {
			t.Fatalf("one filed, one not moved: %+v %v", replay, err)
		}
	})
	t.Run("the process dies right after the folder is made", func(t *testing.T) {
		f := newDocsFixture(t, "estimate.pdf")
		dest := DocumentsDestination{Folder: "", New: "Receipts"}
		digest := f.readyPreview(dest, "estimate.pdf").Digest
		f.mover.exitAfterFolder = true
		runInterrupted(t, func() { _, _ = f.pressWith(dest, keyA, digest, "estimate.pdf") })

		replay, err := f.pressWith(dest, keyA, digest, "estimate.pdf")
		if err != nil || !replay.Replayed || !replay.FolderUnconfirmed || replay.FolderCreated || replay.Filed != 0 || replay.NotAttempted != 1 {
			t.Fatalf("an unconfirmed folder must be said so, with nothing moved: %+v %v", replay, err)
		}
		if !f.exists("Receipts") || f.exists("Receipts/estimate.pdf") || !f.exists("estimate.pdf") {
			t.Fatal("the folder exists on disk and the file must not have moved")
		}
	})
}

// runInterrupted runs fn on its own goroutine, which the test then lets die
// without returning (runtime.Goexit inside), like a process that was killed
// mid-press: deferred cleanup runs, the journal keeps what was written.
func runInterrupted(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the interrupted press did not stop")
	}
}

func TestDocumentsFiling_FailsClosedWhenTheMoverIsUnavailable(t *testing.T) {
	f := newDocsFixture(t, "estimate.pdf")
	f.mkdir("Taxes")
	dest := DocumentsDestination{Folder: "Taxes"}
	digest := f.readyPreview(dest, "estimate.pdf").Digest
	f.s.SetConditionalMover(refusingMover{})
	before := f.tree()

	if p := f.preview(dest, "estimate.pdf"); p.State != documentsPreviewUnsupported || p.Digest != "" {
		t.Fatalf("preview: %+v", p)
	}
	result, err := f.pressWith(dest, keyA, digest, "estimate.pdf")
	if !errors.Is(err, ErrMoveUnsupported) || result.Preview == nil || result.Preview.State != documentsPreviewUnsupported {
		t.Fatalf("want unsupported, got %+v %v", result, err)
	}
	if !reflect.DeepEqual(f.tree(), before) {
		t.Fatal("something moved")
	}
	if state, _ := f.s.store.LoadScanState("ws-1"); len(state.Filings) != 0 || len(state.Actions) != 0 {
		t.Fatal("a refused press must not journal anything")
	}
}

func TestDocumentsFiling_RejectsMalformedRequestsBeforeAnything(t *testing.T) {
	f := newDocsFixture(t, "estimate.pdf")
	f.mkdir("Taxes")
	dest := DocumentsDestination{Folder: "Taxes"}
	digest := f.readyPreview(dest, "estimate.pdf").Digest
	for name, req := range map[string]DocumentsFileRequest{
		"short key":    {Destination: dest, Digest: digest, ClientKey: "short", DecisionID: decisionFor(keyA)},
		"short digest": {Destination: dest, Digest: "abc", ClientKey: keyA, DecisionID: decisionFor(keyA)},
		"no decision":  {Destination: dest, Digest: digest, ClientKey: keyA},
	} {
		if _, err := f.s.FileDocumentsSelection(t.Context(), "local", "ws-1", req, f.reselect("estimate.pdf")); !errors.Is(err, ErrInvalidAction) {
			t.Fatalf("%s: want invalid, got %v", name, err)
		}
	}
	if _, err := f.s.FileDocumentsSelection(t.Context(), "stranger", "ws-1", DocumentsFileRequest{Destination: dest, Digest: digest, ClientKey: keyA, DecisionID: decisionFor(keyA)}, f.reselect("estimate.pdf")); err == nil {
		t.Fatal("a stranger filed into this workspace")
	}
	if f.exists("Taxes/estimate.pdf") {
		t.Fatal("a refused press moved a file")
	}
}

func TestPlacementRecords_AreAdditiveAndAnOlderBuildNeverWidensThem(t *testing.T) {
	f := newDocsFixture(t, "estimate.pdf")
	f.mkdir("Taxes")
	f.mustPress(DocumentsDestination{Folder: "Taxes"}, keyA, "estimate.pdf")
	state, err := f.s.store.LoadScanState("ws-1")
	if err != nil || len(state.Actions) != 1 || len(state.Filings) != 1 {
		t.Fatalf("state: %v %+v", err, state)
	}
	action, filing := state.Actions[0], state.Filings[0]
	if action.Operation != OperationPlace || action.Place == nil || action.DestinationCategory != "" || action.DestinationRelative != "Taxes/estimate.pdf" || !action.Undoable() {
		t.Fatalf("action: %+v", action)
	}
	if action.Place.FolderID == "" || action.Place.RootDirID != f.rootID || action.AfterFingerprint.FileID != action.BeforeFingerprint.FileID {
		t.Fatalf("the verified identities must be journaled: %+v", action)
	}
	if err := action.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := filing.validate(); err != nil {
		t.Fatal(err)
	}

	// An older build drops the new keys when it rewrites the file.
	strip := func(v any, keys ...string) map[string]any {
		raw, _ := json.Marshal(v)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		for _, k := range keys {
			delete(m, k)
		}
		return m
	}
	var olderAction FileAction
	raw, _ := json.Marshal(strip(action, "place"))
	_ = json.Unmarshal(raw, &olderAction)
	if olderAction.Validate() == nil {
		t.Fatal("an older build's copy of a placement must fail validation, so it can never be repeated or undone")
	}
	var olderFiling FilingRecord
	raw, _ = json.Marshal(strip(filing, "destination", "decision_id"))
	_ = json.Unmarshal(raw, &olderFiling)
	if olderFiling.Destination != nil || olderFiling.validate() != nil {
		t.Fatal("a filing without the new keys is an ordinary, valid, legacy-shaped record")
	}
	var half FilingRecord
	raw, _ = json.Marshal(strip(filing, "decision_id"))
	_ = json.Unmarshal(raw, &half)
	if half.validate() == nil {
		t.Fatal("a destination without its decision is not a valid record")
	}
}

// Ori's own placements must never be learned later as the person's manual
// placements: the journal's identities are what the observer excludes.
func TestDocumentsFiling_PlacedMovesAreHiddenFromManualPlacementObservation(t *testing.T) {
	f := newDocsFixture(t, "estimate.pdf", "other.pdf")
	f.mkdir("Taxes")
	reviewed := f.inode("estimate.pdf")
	untouched := f.inode("other.pdf")
	since := time.Now().Add(-time.Hour)

	f.mustPress(DocumentsDestination{Folder: "Taxes"}, keyA, "estimate.pdf")
	hidden, err := f.s.ObservedActionFiles("ws-1", since)
	if err != nil {
		t.Fatal(err)
	}
	if !hidden[reviewed] {
		t.Fatal("a file Ori placed must be excluded from observation")
	}
	if hidden[untouched] {
		t.Fatal("a file Ori did not touch must stay observable")
	}
}

func TestLegacyRecordsStayByteIdentical(t *testing.T) {
	action := FileAction{ID: "a", WorkspaceID: "w", CandidateID: "c", Operation: OperationMove, SourceName: "x.pdf", DestinationCategory: CategoryDocuments}
	raw, _ := json.Marshal(action)
	if bytes.Contains(raw, []byte(`"place"`)) {
		t.Fatalf("a legacy action grew a key: %s", raw)
	}
	filing := FilingRecord{ID: "f", Owner: "o", BatchID: "b", RootID: "r", ClientKey: keyA}
	raw, _ = json.Marshal(filing)
	for _, key := range []string{`"destination"`, `"decision_id"`} {
		if bytes.Contains(raw, []byte(key)) {
			t.Fatalf("a legacy filing grew %s: %s", key, raw)
		}
	}
	if got := PayloadHash("ws", "batch", []PlanItem{{CandidateID: "c", Operation: OperationMove, Category: CategoryDocuments, FingerprintKey: "k"}}); got != legacyPayloadHashGolden {
		t.Fatalf("the legacy approval hash changed: %s", got)
	}
}

// legacyPayloadHashGolden is PayloadHash's output for the plan above. approval.go
// has no diff against origin/dev, so the value is the function's original output;
// it must never change, or every outstanding legacy approval would stop matching.
const legacyPayloadHashGolden = "b967a3c9c3ac6c8e2439aeb8bf6241277e4069d12c4c11e66149e22eb320cded"

func TestPlacementValidation(t *testing.T) {
	candidate := JanitorCandidate{ID: "c", WorkspaceID: "w", BatchID: "b", Name: "x.pdf", Fingerprint: Fingerprint{Name: "x.pdf", Size: 1, ModTime: time.Unix(1, 0), FileID: "1:2"}}
	approval := ApprovalRecord{UserID: "o", IdempotencyKey: "f", ConsumedAt: time.Unix(5, 0)}
	good, err := newPlacedAction("a", candidate, "Taxes/x.pdf", approval, "r", PlacedInto{RootDirID: "1:1", FolderID: "1:3"})
	if err != nil {
		t.Fatal(err)
	}
	if good.Undoable() {
		t.Fatal("a placement that has not been applied cannot be undone")
	}
	applied := good.MarkApplied(Fingerprint{Name: "x.pdf", Size: 1, ModTime: time.Unix(1, 0), FileID: "1:2"}, time.Unix(6, 0))
	if !applied.Undoable() {
		t.Fatal("an applied placement with its verified identities can be undone")
	}
	noIdentity := applied
	noIdentity.AfterFingerprint.FileID = ""
	if noIdentity.Undoable() {
		t.Fatal("without the file's identity after the move there is nothing safe to reverse")
	}
	for name, mutate := range map[string]func(*FileAction){
		"a category":        func(a *FileAction) { a.DestinationCategory = CategoryDocuments },
		"no place block":    func(a *FileAction) { a.Place = nil },
		"no folder id":      func(a *FileAction) { a.Place = &PlacedInto{RootDirID: "1:1"} },
		"no file identity":  func(a *FileAction) { a.BeforeFingerprint.FileID = "" },
		"a bare file name":  func(a *FileAction) { a.DestinationRelative = "x.pdf" },
		"an absolute path":  func(a *FileAction) { a.DestinationRelative = "/Taxes/x.pdf" },
		"a parent segment":  func(a *FileAction) { a.DestinationRelative = "Taxes/../x.pdf" },
		"a place on a move": func(a *FileAction) { a.Operation = OperationMove; a.DestinationCategory = CategoryDocuments },
	} {
		a := good
		mutate(&a)
		if a.Validate() == nil {
			t.Fatalf("%s must not validate", name)
		}
	}
	for _, op := range ValidOperations {
		if op == OperationPlace {
			t.Fatal("a client may never request a placement")
		}
	}
}
