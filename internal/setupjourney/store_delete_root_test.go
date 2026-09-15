package setupjourney

import (
	"context"
	"errors"
	"testing"
	"time"
)

func countRowsFor(t *testing.T, store *SQLiteStore, table string, runIDs ...string) int {
	t.Helper()
	total := 0
	for _, runID := range runIDs {
		var count int
		column := "run_id"
		if table == "setup_journey_run" {
			column = "id"
		}
		// #nosec G202 -- test-only closed table and column names.
		if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table+` WHERE `+column+` = ?`, runID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		total += count
	}
	return total
}

// seedRootWithReceipts gives a root one child and review, operation and
// declaration-migration receipts on both runs.
func seedRootWithReceipts(t *testing.T, store *SQLiteStore, spec RootSpec) (*Run, *Run) {
	t.Helper()
	ctx := context.Background()
	root, _, err := store.CreateOrGetRoot(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	child, _, err := store.CreateOrGetChild(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range []*Run{root, child} {
		current, err := store.GetRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		review, _, err := store.CreateOrGetReviewReceipt(ctx, ReviewReceiptSpec{
			RunKind: current.Kind, RunID: current.ID, IdempotencyKey: "review-" + string(current.Kind),
			StepID: current.StepStates[0].StepID, ActionID: "install", InputDigest: Digest([]byte("input")),
			RunRevision: current.StateRevision, OwnerRevisionDigest: Digest([]byte("owner")),
			DisclosureDigest: Digest([]byte("disclosure")), TTL: 15 * time.Minute,
		})
		if err != nil {
			t.Fatal(err)
		}
		_, claimed, _, err := store.ClaimOperation(ctx, OperationClaim{
			RunKind: current.Kind, RunID: current.ID, IfRevision: current.StateRevision,
			IdempotencyKey: "operation-" + string(current.Kind), StepID: current.StepStates[0].StepID, ActionID: "install",
			InputDigest: Digest([]byte("input")), ReviewToken: review.Token, ReviewDigest: Digest([]byte("disclosure")),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := store.FinalizeOperation(ctx, claimed, "operation-"+string(current.Kind), OperationCompletion{
			Status: OperationSucceeded, ResultCode: ResultApplied,
		}); err != nil {
			t.Fatal(err)
		}
		settled, err := store.GetRun(ctx, current.ID)
		if err != nil {
			t.Fatal(err)
		}
		stepIDs := make([]string, len(settled.StepStates))
		for index, state := range settled.StepStates {
			stepIDs[index] = state.StepID
		}
		if _, _, err := store.ApplyDeclarationMigration(ctx, settled.Kind, settled.ID, settled.StateRevision,
			1, 2, stepIDs, Digest([]byte("migration-"+string(settled.Kind)))); err != nil {
			t.Fatal(err)
		}
	}
	return root, child
}

// FR 35: one transaction deletes the root, its children, and every receipt of
// those runs. Other roots are untouched.
func TestSQLiteStoreDeleteRootCascadesOnlyThatRootsProgress(t *testing.T) {
	ctx := context.Background()
	_, store := openTestStore(t)
	root, child := seedRootWithReceipts(t, store, testRootSpec())
	otherSpec := testRootSpec()
	otherSpec.JourneyID = "other-journey"
	otherRoot, otherChild := seedRootWithReceipts(t, store, otherSpec)

	tables := []string{
		"setup_journey_review_receipt", "setup_journey_operation_receipt",
		"setup_journey_declaration_migration_receipt", "setup_journey_run",
	}
	for _, table := range tables {
		if countRowsFor(t, store, table, root.ID, child.ID) != 2 {
			t.Fatalf("seed %s rows missing", table)
		}
	}
	if err := store.DeleteRoot(ctx, root.OwnerUserID, root.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if remaining := countRowsFor(t, store, table, root.ID, child.ID); remaining != 0 {
			t.Fatalf("%s kept %d rows of the deleted root", table, remaining)
		}
		if kept := countRowsFor(t, store, table, otherRoot.ID, otherChild.ID); kept != 2 {
			t.Fatalf("%s lost rows of another root: %d", table, kept)
		}
	}
	if _, err := store.GetRun(ctx, root.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted root still reads: %v", err)
	}
	if err := store.DeleteRoot(ctx, root.OwnerUserID, root.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete error = %v", err)
	}
	// A fresh root can be created under the same identity.
	fresh, created, err := store.CreateOrGetRoot(ctx, testRootSpec())
	if err != nil || !created || fresh.ID == root.ID {
		t.Fatalf("fresh root = %+v created=%v err=%v", fresh, created, err)
	}
}

// FR 35: a root the user does not own, a child ID, or a run with an unsettled
// operation is refused and nothing is deleted.
func TestSQLiteStoreDeleteRootRefusesForeignChildAndBusyRuns(t *testing.T) {
	ctx := context.Background()
	_, store := openTestStore(t)
	root := createTestRoot(t, store)
	child, _, err := store.CreateOrGetChild(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteRoot(ctx, "someone-else", root.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign delete error = %v", err)
	}
	if err := store.DeleteRoot(ctx, root.OwnerUserID, child.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("child delete error = %v", err)
	}
	for name, input := range map[string][2]string{"blank owner": {"", root.ID}, "blank root": {root.OwnerUserID, ""}} {
		if err := store.DeleteRoot(ctx, input[0], input[1]); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s error = %v", name, err)
		}
	}
	if _, _, _, err := store.ClaimOperation(ctx, OperationClaim{
		RunKind: RunKindChild, RunID: child.ID, IfRevision: child.StateRevision,
		IdempotencyKey: "busy-operation", StepID: child.StepStates[0].StepID, ActionID: "connect_another_project",
		InputDigest: Digest([]byte("busy")),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteRoot(ctx, root.OwnerUserID, root.ID); !errors.Is(err, ErrOperationBusy) {
		t.Fatalf("busy delete error = %v", err)
	}
	if countRowsFor(t, store, "setup_journey_run", root.ID, child.ID) != 2 {
		t.Fatal("a refused delete removed runs")
	}
}
