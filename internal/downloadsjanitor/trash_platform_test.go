package downloadsjanitor

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Trash is the only removal Ori performs, and it is recoverable by definition.
// These tests cover the platforms the developer's machine is not, using fakes,
// because the alternative — exercising a real Trash — is destructive and
// platform-dependent. They also assert the absence of a permanent-delete path,
// which is a property of the whole package rather than of any one function.

// A platform with no Trash gets no removal. The interesting part is not that it
// fails, but that the file survives: there is no fallback to os.Remove.
func TestTrash_UnsupportedPlatformRemovesNothing(t *testing.T) {
	service, root := configuredService(t)
	service.SetMover(&realMover{})
	trash := newFakeTrash(t)
	trash.supported = false
	service.SetTrash(trash)

	agedFile(t, root, "junk.bin", 40)
	batch, _, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil {
		t.Fatal(err)
	}
	_, candidates, _ := service.BatchDetail("ws-1", batch.ID)

	outcomes := approveAndConfirmTrash(t, service, candidates)
	if len(outcomes) != 1 || outcomes[0].Result != ResultFailed {
		t.Fatalf("removal must fail on a platform with no Trash: %+v", outcomes)
	}
	if !strings.Contains(outcomes[0].Message, "no recoverable Trash") {
		t.Fatalf("the message must say why: %q", outcomes[0].Message)
	}
	if trash.moves != 0 {
		t.Fatal("an unsupported Trash must not be asked to move anything")
	}
	// The whole point: the file is still there.
	if _, err := os.Lstat(filepath.Join(root, "junk.bin")); err != nil {
		t.Fatalf("the file must survive a failed removal: %v", err)
	}
	// And history does not claim it was removed.
	actions, _ := service.ListActions("ws-1")
	for _, action := range actions {
		if action.Operation == OperationTrash && action.Result == ResultApplied {
			t.Fatalf("nothing may be recorded as removed: %+v", action)
		}
	}
}

// Windows' Recycle Bin restores by original path and issues no token. An empty
// token must therefore not be mistaken for "this cannot be undone".
func TestTrash_RestoreWorksWithoutAToken(t *testing.T) {
	service, root := configuredService(t)
	service.SetMover(&realMover{})
	trash := newFakeTrash(t)
	trash.emptyToken = true
	service.SetTrash(trash)

	agedFile(t, root, "recycled.bin", 25)
	batch, _, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil {
		t.Fatal(err)
	}
	_, candidates, _ := service.BatchDetail("ws-1", batch.ID)
	if outcomes := approveAndConfirmTrash(t, service, candidates); outcomes[0].Result != ResultApplied {
		t.Fatalf("removal should succeed: %+v", outcomes)
	}

	actions, _ := service.ListActions("ws-1")
	var trashed FileAction
	for _, action := range actions {
		if action.Operation == OperationTrash {
			trashed = action
		}
	}
	if !trashed.Undoable() {
		t.Fatal("a platform that restores by path still offers undo; an absent token is not an absent Trash")
	}
	result, err := service.Undo(context.Background(), "ws-1", trashed.ID, "user-1")
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if result.Result != string(UndoDone) {
		t.Fatalf("undo result = %q: %s", result.Result, result.Message)
	}
	if _, err := os.Lstat(filepath.Join(root, "recycled.bin")); err != nil {
		t.Fatalf("the file must come back: %v", err)
	}
}

// A Trash that reports success without removing anything must not be believed.
// Ori decides from the filesystem, not from the tool's word.
func TestTrash_SilentNoopIsNotReportedAsRemoved(t *testing.T) {
	service, root := configuredService(t)
	service.SetMover(&realMover{})
	trash := newFakeTrash(t)
	trash.silentNoop = true
	service.SetTrash(trash)

	agedFile(t, root, "still-here.bin", 30)
	batch, _, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil {
		t.Fatal(err)
	}
	_, candidates, _ := service.BatchDetail("ws-1", batch.ID)

	outcomes := approveAndConfirmTrash(t, service, candidates)
	if outcomes[0].Result == ResultApplied {
		t.Fatalf("the file is still on disk; this must not be reported as removed: %+v", outcomes[0])
	}
	if _, err := os.Lstat(filepath.Join(root, "still-here.bin")); err != nil {
		t.Fatalf("the file is present and must stay present: %v", err)
	}
}

// A failing Trash inside a mixed batch must not take the moves down with it.
func TestTrash_FailureDoesNotAffectMovesInTheSameBatch(t *testing.T) {
	service, root := configuredService(t)
	service.SetMover(&realMover{})
	trash := newFakeTrash(t)
	trash.failMove = true
	service.SetTrash(trash)

	agedFile(t, root, "keep.pdf", 60)
	agedFile(t, root, "drop.bin", 20)
	batch, _, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil {
		t.Fatal(err)
	}
	_, candidates, _ := service.BatchDetail("ws-1", batch.ID)

	var items []PreviewRequestItem
	for _, candidate := range candidates {
		if strings.HasSuffix(candidate.Name, ".pdf") {
			items = append(items, PreviewRequestItem{CandidateID: candidate.ID, Operation: OperationMove, Category: string(CategoryDocuments)})
			continue
		}
		items = append(items, PreviewRequestItem{CandidateID: candidate.ID, Operation: OperationTrash})
	}
	preview, err := service.PreviewMoves(PreviewRequest{WorkspaceID: "ws-1", UserID: "user-1", Items: items})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ConfirmMoves(context.Background(), ConfirmRequest{
		WorkspaceID: "ws-1", UserID: "user-1", BatchID: preview.BatchID, Token: preview.Token, Items: items,
	})
	if err != nil {
		t.Fatalf("ConfirmMoves: %v", err)
	}

	var moved, failed int
	for _, outcome := range result.Outcomes {
		switch outcome.Result {
		case ResultApplied:
			moved++
		case ResultFailed:
			failed++
		}
	}
	if moved != 1 || failed != 1 {
		t.Fatalf("one move should succeed and one removal fail: %+v", result.Outcomes)
	}
	if _, err := os.Lstat(filepath.Join(root, "Filed", "Documents", "keep.pdf")); err != nil {
		t.Fatalf("the move must still land: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "drop.bin")); err != nil {
		t.Fatalf("the file whose removal failed must remain: %v", err)
	}
}

// There is no permanent delete. This is asserted against the package source
// rather than through behavior, because the claim is about what does not exist:
// no future change should be able to add one without this test objecting.
func TestNoPermanentDeletePathExists(t *testing.T) {
	// Calls that unlink irrecoverably. os.Remove is legitimate for Ori's own
	// scratch files (temp files during an atomic write, a writability probe),
	// so each occurrence is allowlisted by the file it lives in and justified
	// below rather than banned outright.
	permittedRemovals := map[string]string{
		"store.go":   "removes its own temp file when an atomic write fails",
		"service.go": "removes its own writability probe file",
	}

	fileSet := token.NewFileSet()
	packages, err := parser.ParseDir(fileSet, ".", func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}

	for _, pkg := range packages {
		for path, file := range pkg.Files {
			name := filepath.Base(path)
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := selector.X.(*ast.Ident)
				if !ok || ident.Name != "os" {
					return true
				}
				switch selector.Sel.Name {
				case "RemoveAll":
					t.Errorf("%s:%d calls os.RemoveAll; the Janitor never deletes recursively",
						name, fileSet.Position(call.Pos()).Line)
				case "Remove", "Truncate":
					if _, permitted := permittedRemovals[name]; !permitted {
						t.Errorf("%s:%d calls os.%s. The Janitor's only removal is the recoverable "+
							"Trash. If this is Ori's own scratch file, add it to permittedRemovals "+
							"with a reason; if it touches a user's file, it must not exist.",
							name, fileSet.Position(call.Pos()).Line, selector.Sel.Name)
					}
				}
				return true
			})
		}
	}

	// The agent's tools are read-only: no delete, and no mutation of any kind.
	for _, tool := range JanitorReadTools {
		if strings.Contains(tool, "delete") || strings.Contains(tool, "move") ||
			strings.Contains(tool, "write") || strings.Contains(tool, "create") {
			t.Errorf("the Downloads Curator must hold no mutation tool, found %q", tool)
		}
	}

	// And the only removal the service performs goes through the recoverable
	// interface, which cannot express a permanent delete.
	var _ interface {
		Supported() bool
		MoveToTrash(string) (string, error)
		RestoreFromTrash(string, string) error
	} = TrashRemover(nil)
}

// approveAndConfirmTrash approves every candidate for removal and applies it.
func approveAndConfirmTrash(t *testing.T, service *Service, candidates []JanitorCandidate) []ItemOutcome {
	t.Helper()
	items := make([]PreviewRequestItem, 0, len(candidates))
	for _, candidate := range candidates {
		items = append(items, PreviewRequestItem{CandidateID: candidate.ID, Operation: OperationTrash})
	}
	return approveAndConfirmItems(t, service, items).Outcomes
}
