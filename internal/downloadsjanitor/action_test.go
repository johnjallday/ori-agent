package downloadsjanitor

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func approvedCandidate() JanitorCandidate {
	candidate := testCandidate("report.pdf")
	candidate.Decision = DecisionMove
	candidate.DecisionCategory = CategoryDocuments
	return candidate
}

func newTestAction(t *testing.T, mutate ...func(*FileAction)) FileAction {
	t.Helper()
	action, err := NewApprovedAction(
		"action-1", "ws-1", approvedCandidate(), OperationMove,
		"Filed/Documents/report.pdf", "user-1", time.Now(), "idem-1",
	)
	if err != nil {
		t.Fatalf("NewApprovedAction: %v", err)
	}
	for _, m := range mutate {
		m(&action)
	}
	return action
}

func TestNewApprovedAction_RecordsWhoApprovedWhatAndAgainstWhichFileState(t *testing.T) {
	candidate := approvedCandidate()
	approvedAt := time.Now()

	action, err := NewApprovedAction("action-1", "ws-1", candidate, OperationMove, "Filed/Documents/report.pdf", "user-1", approvedAt, "idem-1")
	if err != nil {
		t.Fatalf("NewApprovedAction: %v", err)
	}
	if action.ApprovedBy != "user-1" || !action.ApprovedAt.Equal(approvedAt) {
		t.Fatalf("approval not recorded: %+v", action)
	}
	if !action.BeforeFingerprint.Matches(candidate.Fingerprint) {
		t.Fatal("an action must record the file state its approval was given for")
	}
	if action.Result != ResultPending || action.Undo != UndoUnavailable {
		t.Fatalf("a fresh action must be pending and not undoable: %+v", action)
	}
	if action.DestinationCategory != CategoryDocuments {
		t.Fatalf("destination category = %q", action.DestinationCategory)
	}
	// The source is a name, never a path — an action cannot address anything
	// outside the configured folder.
	if strings.ContainsAny(action.SourceName, `/\`) {
		t.Fatalf("source name must be a top-level name: %q", action.SourceName)
	}
}

// An action cannot exist without an approver, an approval time, and a
// fingerprint. Those are what let the journal prove, later, that a mutation was
// authorized rather than invented.
func TestFileAction_ValidateRequiresProofOfApproval(t *testing.T) {
	cases := map[string]func(*FileAction){
		"no approver":      func(a *FileAction) { a.ApprovedBy = "  " },
		"no approval time": func(a *FileAction) { a.ApprovedAt = time.Time{} },
		"no idempotency":   func(a *FileAction) { a.IdempotencyKey = "" },
		"no fingerprint":   func(a *FileAction) { a.BeforeFingerprint = Fingerprint{} },
		"wrong file":       func(a *FileAction) { a.BeforeFingerprint.Name = "other.pdf" },
		"no candidate":     func(a *FileAction) { a.CandidateID = "" },
		"no workspace":     func(a *FileAction) { a.WorkspaceID = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			action := newTestAction(t, mutate)
			if err := action.Validate(); !errors.Is(err, ErrInvalidAction) {
				t.Fatalf("expected ErrInvalidAction, got %v", err)
			}
		})
	}
}

func TestFileAction_ValidateRejectsUnusableOperationsAndDestinations(t *testing.T) {
	cases := map[string]func(*FileAction){
		"unknown operation":   func(a *FileAction) { a.Operation = "delete" },
		"permanent delete":    func(a *FileAction) { a.Operation = "permanent_delete" },
		"path as source":      func(a *FileAction) { a.SourceName = "sub/report.pdf"; a.BeforeFingerprint.Name = "sub/report.pdf" },
		"unknown category":    func(a *FileAction) { a.DestinationCategory = "receipts" },
		"absolute dest":       func(a *FileAction) { a.DestinationRelative = "/etc/passwd" },
		"traversing dest":     func(a *FileAction) { a.DestinationRelative = "Filed/../../etc/passwd" },
		"windows sep in dest": func(a *FileAction) { a.DestinationRelative = `Filed\Documents\report.pdf` },
		"bare dest":           func(a *FileAction) { a.DestinationRelative = "report.pdf" },
		"empty dest":          func(a *FileAction) { a.DestinationRelative = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			action := newTestAction(t, mutate)
			if err := action.Validate(); !errors.Is(err, ErrInvalidAction) {
				t.Fatalf("expected ErrInvalidAction, got %v", err)
			}
		})
	}
}

// Trash has no destination inside the folder; recording one would misrepresent
// what happened to the file.
func TestFileAction_TrashCarriesNoDestination(t *testing.T) {
	candidate := approvedCandidate()
	action, err := NewApprovedAction("action-1", "ws-1", candidate, OperationTrash, "", "user-1", time.Now(), "idem-1")
	if err != nil {
		t.Fatalf("NewApprovedAction: %v", err)
	}
	if action.DestinationRelative != "" || action.DestinationCategory != "" {
		t.Fatalf("a Trash action must have no destination: %+v", action)
	}

	action.DestinationRelative = "Filed/Documents/report.pdf"
	if err := action.Validate(); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("expected a Trash action with a destination to be rejected, got %v", err)
	}
}

func TestValidOperations_ExcludePermanentDeletion(t *testing.T) {
	if len(ValidOperations) != 2 {
		t.Fatalf("operations = %v; version 1 has exactly move and trash", ValidOperations)
	}
	for _, operation := range ValidOperations {
		if strings.Contains(string(operation), "delete") || strings.Contains(string(operation), "remove") {
			t.Fatalf("no destructive operation may exist in version 1: %q", operation)
		}
	}
}

func TestDestinationRelativeFor_BuildsAContainedReference(t *testing.T) {
	got, err := DestinationRelativeFor("Filed", CategoryDocuments, "report (2).pdf")
	if err != nil {
		t.Fatalf("DestinationRelativeFor: %v", err)
	}
	if got != "Filed/Documents/report (2).pdf" {
		t.Fatalf("destination = %q", got)
	}

	// Nothing a hostile name or category could do produces an escaping
	// reference.
	if _, err := DestinationRelativeFor("Filed", "receipts", "report.pdf"); err == nil {
		t.Fatal("an unknown category must be rejected")
	}
	for _, name := range []string{"../escape.pdf", "sub/report.pdf", "", ".."} {
		if _, err := DestinationRelativeFor("Filed", CategoryDocuments, name); err == nil {
			t.Fatalf("destination name %q should have been rejected", name)
		}
	}
}

func TestFileAction_LifecycleTransitions(t *testing.T) {
	action := newTestAction(t)
	now := time.Now()

	applying := action.MarkApplying(now)
	if applying.Result != ResultApplying || applying.StartedAt.IsZero() {
		t.Fatalf("applying = %+v", applying)
	}
	// An interrupted run must be distinguishable from one that never started.
	if applying.CompletedAt.IsZero() != true {
		t.Fatal("an in-flight action has no completion time")
	}

	after := Fingerprint{Name: "report.pdf", Size: 1024, ModTime: now}
	applied := applying.MarkApplied(after, now)
	if applied.Result != ResultApplied || !applied.AfterFingerprint.Matches(after) {
		t.Fatalf("applied = %+v", applied)
	}
	if !applied.Undoable() {
		t.Fatal("a completed move should be offered for undo")
	}

	stale := applying.MarkStale("The file changed after you approved it.", now)
	if stale.Result != ResultStale || stale.Undoable() {
		t.Fatalf("stale = %+v", stale)
	}
	failed := applying.MarkFailed("The destination was unavailable.", now)
	if failed.Result != ResultFailed || failed.Undoable() || failed.ErrorSummary == "" {
		t.Fatalf("failed = %+v", failed)
	}

	// Only an applied action is ever undoable.
	for _, result := range []ActionResult{ResultPending, ResultApplying, ResultStale, ResultFailed} {
		candidate := action
		candidate.Result = result
		candidate.Undo = UndoAvailable
		if candidate.Undoable() {
			t.Fatalf("%q must not be undoable", result)
		}
	}
	// Nor is one that has already been undone.
	undone := applied
	undone.Undo = UndoDone
	if undone.Undoable() {
		t.Fatal("an undone action must not be undoable again")
	}
}

func TestActionResult_TerminalStates(t *testing.T) {
	for _, result := range []ActionResult{ResultApplied, ResultStale, ResultFailed} {
		if !result.Terminal() {
			t.Errorf("%q should be terminal", result)
		}
	}
	for _, result := range []ActionResult{ResultPending, ResultApplying} {
		if result.Terminal() {
			t.Errorf("%q is not terminal", result)
		}
	}
}

func TestFileAction_JournalCarriesNoContentOrAbsolutePaths(t *testing.T) {
	action := newTestAction(t, func(a *FileAction) {
		*a = a.MarkApplied(Fingerprint{Name: "report.pdf", Size: 10, ModTime: time.Now()}, time.Now())
	})
	data, err := json.Marshal(action)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for key := range raw {
		for _, banned := range []string{"content", "text", "excerpt", "prompt", "absolute"} {
			if strings.Contains(key, banned) {
				t.Fatalf("a journal entry must not carry %q", key)
			}
		}
	}
	// The destination reference is relative, so the journal never records where
	// the user's home folder is.
	if strings.HasPrefix(action.DestinationRelative, "/") {
		t.Fatalf("destination must be relative: %q", action.DestinationRelative)
	}

	var back FileAction
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.ID != action.ID || back.Result != ResultApplied || !back.AfterFingerprint.Matches(action.AfterFingerprint) {
		t.Fatalf("action did not round-trip: %+v", back)
	}
}

func TestSummarizeOutcomes_KeepsMixedResultsHonest(t *testing.T) {
	applied, failed, stale := SummarizeOutcomes([]ItemOutcome{
		{Result: ResultApplied},
		{Result: ResultApplied},
		{Result: ResultFailed},
		{Result: ResultStale},
	})
	if applied != 2 || failed != 1 || stale != 1 {
		t.Fatalf("applied=%d failed=%d stale=%d", applied, failed, stale)
	}
}
