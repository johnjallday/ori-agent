package setupjourney

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/database"
)

var testStepIDs = []string{"integration", "project", "workspace", "staffing", "summary"}

func openTestStore(t *testing.T) (*database.DB, *SQLiteStore) {
	t.Helper()
	db, err := database.Open(context.Background(), &database.Config{InMemory: true, WALMode: false})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, NewSQLiteStore(db)
}

func testRootSpec() RootSpec {
	return RootSpec{
		OwnerUserID:              "local",
		RelationshipID:           "assistant-1",
		SpecialistSlug:           "music-production",
		JourneyID:                "reaper-setup",
		DeclarationSchemaVersion: 1,
		DeclarationVersion:       1,
		StepIDs:                  append([]string(nil), testStepIDs...),
	}
}

func createTestRoot(t *testing.T, store *SQLiteStore) *Run {
	t.Helper()
	run, created, err := store.CreateOrGetRoot(context.Background(), testRootSpec())
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	if !created {
		t.Fatal("first root create was reported as replay")
	}
	return run
}

func TestSQLiteStoreRootAndChildRunsStayIndependent(t *testing.T) {
	_, store := openTestStore(t)
	ctx := context.Background()
	root := createTestRoot(t, store)
	if root.Kind != RunKindRoot || root.StateRevision != 1 || root.Lifecycle != LifecycleNotStarted {
		t.Fatalf("unexpected initial root: %#v", root)
	}
	if root.ID == "" || len(root.StepStates) != len(testStepIDs) || root.CurrentStepID != testStepIDs[0] {
		t.Fatalf("root did not receive server identity and bounded steps: %#v", root)
	}
	if root.FirstOpenedAt != nil || root.FirstCompletedAt != nil || root.Dismissed {
		t.Fatalf("inert root gained presentation timestamps: %#v", root)
	}

	replayedRoot, created, err := store.CreateOrGetRoot(ctx, testRootSpec())
	if err != nil || created || replayedRoot.ID != root.ID {
		t.Fatalf("root create-or-get = %#v, created=%v, err=%v", replayedRoot, created, err)
	}

	child, childCreated, err := store.CreateOrGetChild(ctx, root.ID)
	if err != nil || !childCreated {
		t.Fatalf("create child: created=%v err=%v", childCreated, err)
	}
	if child.Kind != RunKindChild || child.RootRunID != root.ID || child.ID == root.ID || child.StateRevision != 1 {
		t.Fatalf("unexpected child identity: %#v", child)
	}
	if child.OwnerUserID != "" || child.RelationshipID != "" || child.SpecialistSlug != "" ||
		child.IntegrationPluginID != "" || child.HomeWorkspaceID != "" {
		t.Fatalf("child copied root authority/receipts: %#v", child)
	}

	openedAt := time.Now().UTC().Truncate(time.Microsecond)
	root.FirstOpenedAt = &openedAt
	root.Lifecycle = LifecycleInProgress
	root.StepStates[0].Status = StepComplete
	root.CurrentStepID = root.StepStates[1].StepID
	root.IntegrationPluginID = "com.ori.reaper"
	root.IntegrationVersion = "0.5.0"
	updatedRoot, err := store.CompareAndSwapRun(ctx, root, 1)
	if err != nil {
		t.Fatalf("update root: %v", err)
	}
	if updatedRoot.StateRevision != 2 || updatedRoot.IntegrationPluginID != "com.ori.reaper" {
		t.Fatalf("root update was not revisioned: %#v", updatedRoot)
	}
	unchangedChild, err := store.GetRun(ctx, child.ID)
	if err != nil {
		t.Fatalf("reload child: %v", err)
	}
	if unchangedChild.StateRevision != 1 || unchangedChild.StepStates[0].Status != StepPending {
		t.Fatalf("root progress leaked into child: %#v", unchangedChild)
	}

	child.ProjectWorkspaceID = "workspace-project-1"
	child.Lifecycle = LifecycleInProgress
	boundChild, err := store.CompareAndSwapRun(ctx, child, 1)
	if err != nil || boundChild.ProjectWorkspaceID != "workspace-project-1" {
		t.Fatalf("bind child workspace: %#v, %v", boundChild, err)
	}
	secondChild, secondCreated, err := store.CreateOrGetChild(ctx, root.ID)
	if err != nil || !secondCreated || secondChild.ID == child.ID {
		t.Fatalf("create second unbound child: %#v created=%v err=%v", secondChild, secondCreated, err)
	}
	resumed, resumedCreated, err := store.CreateOrGetChild(ctx, root.ID)
	if err != nil || resumedCreated || resumed.ID != secondChild.ID {
		t.Fatalf("resume unbound child: %#v created=%v err=%v", resumed, resumedCreated, err)
	}
	children, err := store.ListChildRuns(ctx, root.ID)
	if err != nil || len(children) != 2 {
		t.Fatalf("list children: len=%d err=%v", len(children), err)
	}
}

func TestSQLiteStoreFirstCompletionTimestampSurvivesRegression(t *testing.T) {
	_, store := openTestStore(t)
	ctx := context.Background()
	run := createTestRoot(t, store)
	now := time.Now().UTC().Truncate(time.Microsecond)
	run.FirstOpenedAt = &now
	run.FirstCompletedAt = &now
	run.Lifecycle = LifecycleReady
	run.CurrentStepID = ""
	for index := range run.StepStates {
		run.StepStates[index].Status = StepComplete
	}
	ready, err := store.CompareAndSwapRun(ctx, run, run.StateRevision)
	if err != nil {
		t.Fatalf("mark ready: %v", err)
	}
	ready.Lifecycle = LifecycleNeedsAttention
	ready.CurrentStepID = ready.StepStates[0].StepID
	ready.StepStates[0] = StepState{StepID: ready.StepStates[0].StepID, Status: StepBlocked, ReasonCode: ReasonIntegrationDisabled}
	regressed, err := store.CompareAndSwapRun(ctx, ready, ready.StateRevision)
	if err != nil {
		t.Fatalf("record regression: %v", err)
	}
	if regressed.FirstCompletedAt == nil || !regressed.FirstCompletedAt.Equal(now) {
		t.Fatalf("regression cleared historical completion: %#v", regressed.FirstCompletedAt)
	}
	regressed.FirstCompletedAt = nil
	if _, err := store.CompareAndSwapRun(ctx, regressed, regressed.StateRevision); !errors.Is(err, ErrInvalid) {
		t.Fatalf("clearing historical completion error = %v; want ErrInvalid", err)
	}
}

func TestSQLiteStoreDeclarationMigrationIsExplicitAndPreservesReceipts(t *testing.T) {
	_, store := openTestStore(t)
	ctx := context.Background()
	run := createTestRoot(t, store)
	run.IntegrationPluginID = "com.ori.reaper"
	run.IntegrationVersion = "0.5.0"
	run.HomeWorkspaceID = "workspace-home"
	run.ProjectWorkspaceID = "workspace-project"
	run.Lifecycle = LifecycleInProgress
	run.StepStates[0].Status = StepComplete
	run.CurrentStepID = run.StepStates[1].StepID
	run, err := store.CompareAndSwapRun(ctx, run, run.StateRevision)
	if err != nil {
		t.Fatalf("seed canonical receipts: %v", err)
	}
	migrated, receipt, err := store.ApplyDeclarationMigration(
		ctx, run.Kind, run.ID, run.StateRevision, 1, 2,
		[]string{"integration", "project", "workspace", "team", "summary"},
		Digest([]byte("v1-to-v2-step-map")),
	)
	if err != nil {
		t.Fatalf("apply declaration migration: %v", err)
	}
	if migrated.DeclarationVersion != 2 || migrated.StateRevision != run.StateRevision+1 ||
		migrated.Lifecycle != LifecycleNeedsAttention || migrated.CurrentStepID != "integration" {
		t.Fatalf("unexpected migrated run: %#v", migrated)
	}
	if migrated.IntegrationPluginID != run.IntegrationPluginID ||
		migrated.HomeWorkspaceID != run.HomeWorkspaceID ||
		migrated.ProjectWorkspaceID != run.ProjectWorkspaceID {
		t.Fatalf("migration rewrote canonical receipts: before=%#v after=%#v", run, migrated)
	}
	if receipt.FromDeclarationVersion != 1 || receipt.ToDeclarationVersion != 2 ||
		receipt.RunRevisionBefore != run.StateRevision || receipt.RunRevisionAfter != migrated.StateRevision {
		t.Fatalf("unexpected migration receipt: %#v", receipt)
	}
	if _, _, err := store.ApplyDeclarationMigration(
		ctx, run.Kind, run.ID, run.StateRevision, 1, 2, testStepIDs,
		Digest([]byte("v1-to-v2-step-map")),
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale migration error = %v; want ErrConflict", err)
	}
}

func TestSQLiteStoreCompareAndSwapRejectsStaleAndDuplicateProjectRuns(t *testing.T) {
	_, store := openTestStore(t)
	ctx := context.Background()
	root := createTestRoot(t, store)
	stale := root.Clone()
	root.Dismissed = true
	dismissedAt := time.Now().UTC()
	root.LastDismissedAt = &dismissedAt
	if _, err := store.CompareAndSwapRun(ctx, root, 1); err != nil {
		t.Fatalf("first CAS: %v", err)
	}
	stale.Lifecycle = LifecycleInProgress
	if _, err := store.CompareAndSwapRun(ctx, stale, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale CAS error = %v; want ErrConflict", err)
	}

	first, _, err := store.CreateOrGetChild(ctx, root.ID)
	if err != nil {
		t.Fatalf("create first child: %v", err)
	}
	first.ProjectWorkspaceID = "workspace-shared"
	first.Lifecycle = LifecycleInProgress
	if _, err := store.CompareAndSwapRun(ctx, first, 1); err != nil {
		t.Fatalf("bind first child: %v", err)
	}
	second, _, err := store.CreateOrGetChild(ctx, root.ID)
	if err != nil {
		t.Fatalf("create second child: %v", err)
	}
	second.ProjectWorkspaceID = "workspace-shared"
	second.Lifecycle = LifecycleInProgress
	if _, err := store.CompareAndSwapRun(ctx, second, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate child project error = %v; want ErrConflict", err)
	}
}

func TestSQLiteStoreReviewReceiptReplaysAndIsConsumedWithOperationClaim(t *testing.T) {
	_, store := openTestStore(t)
	ctx := context.Background()
	root := createTestRoot(t, store)
	spec := ReviewReceiptSpec{
		RunKind: RunKindRoot, RunID: root.ID, IdempotencyKey: "review-request-1",
		StepID: "integration", ActionID: "install", InputDigest: Digest([]byte("input")),
		RunRevision: root.StateRevision, OwnerRevisionDigest: Digest([]byte("owner-v1")),
		DisclosureDigest: Digest([]byte("disclosure-v1")), TTL: 15 * time.Minute,
	}
	review, created, err := store.CreateOrGetReviewReceipt(ctx, spec)
	if err != nil || !created {
		t.Fatalf("create review: %#v created=%v err=%v", review, created, err)
	}
	replayed, created, err := store.CreateOrGetReviewReceipt(ctx, spec)
	if err != nil || created || replayed.Token != review.Token {
		t.Fatalf("replay review: %#v created=%v err=%v", replayed, created, err)
	}
	changed := spec
	changed.DisclosureDigest = Digest([]byte("changed"))
	if _, _, err := store.CreateOrGetReviewReceipt(ctx, changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed review replay error = %v", err)
	}

	claim := OperationClaim{
		RunKind: RunKindRoot, RunID: root.ID, IfRevision: root.StateRevision,
		IdempotencyKey: "install-request-reviewed", StepID: "integration", ActionID: "install",
		InputDigest: spec.InputDigest, ReviewToken: review.Token, ReviewDigest: spec.DisclosureDigest,
	}
	if _, _, _, err := store.ClaimOperation(ctx, claim); err != nil {
		t.Fatalf("claim reviewed operation: %v", err)
	}
	consumed, err := store.GetReviewReceipt(ctx, review.Token)
	if err != nil || consumed.ConsumedAt == nil || consumed.ConsumedByKey != claim.IdempotencyKey {
		t.Fatalf("review was not atomically consumed: %#v err=%v", consumed, err)
	}
}

func TestSQLiteStoreRejectsStaleOrExpiredReviewAtClaim(t *testing.T) {
	_, store := openTestStore(t)
	ctx := context.Background()
	root := createTestRoot(t, store)
	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return clock }
	spec := ReviewReceiptSpec{
		RunKind: RunKindRoot, RunID: root.ID, IdempotencyKey: "review-expiring",
		StepID: "integration", ActionID: "install", InputDigest: Digest([]byte("input")),
		RunRevision: 1, OwnerRevisionDigest: Digest([]byte("owner")),
		DisclosureDigest: Digest([]byte("disclosure")), TTL: time.Minute,
	}
	review, _, err := store.CreateOrGetReviewReceipt(ctx, spec)
	if err != nil {
		t.Fatalf("create review: %v", err)
	}
	clock = clock.Add(2 * time.Minute)
	claim := OperationClaim{
		RunKind: RunKindRoot, RunID: root.ID, IfRevision: 1,
		IdempotencyKey: "install-after-expiry", StepID: "integration", ActionID: "install",
		InputDigest: spec.InputDigest, ReviewToken: review.Token, ReviewDigest: spec.DisclosureDigest,
	}
	if _, _, _, err := store.ClaimOperation(ctx, claim); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired review claim error = %v", err)
	}
	claim.IdempotencyKey = "install-wrong-disclosure"
	claim.ReviewDigest = Digest([]byte("different"))
	if _, _, _, err := store.ClaimOperation(ctx, claim); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed disclosure claim error = %v", err)
	}
	persisted, err := store.GetReviewReceipt(ctx, review.Token)
	if err != nil || persisted.ConsumedAt != nil {
		t.Fatalf("rejected claim consumed review: %#v err=%v", persisted, err)
	}
}

func TestSQLiteStoreOperationClaimReplayFinalizeAndBusyGuard(t *testing.T) {
	_, store := openTestStore(t)
	ctx := context.Background()
	root := createTestRoot(t, store)
	claim := OperationClaim{
		RunKind: RunKindRoot, RunID: root.ID, IfRevision: 1,
		IdempotencyKey: "install-request-1", StepID: "integration",
		ActionID: "install", InputDigest: Digest([]byte(`{"candidate":"reviewed"}`)),
	}
	receipt, claimedRun, replayed, err := store.ClaimOperation(ctx, claim)
	if err != nil || replayed {
		t.Fatalf("claim operation: replayed=%v err=%v", replayed, err)
	}
	if receipt.Status != OperationClaimed || receipt.RunRevisionBefore != 1 ||
		receipt.RunRevisionAfter != 2 || claimedRun.StateRevision != 2 {
		t.Fatalf("unexpected claim receipt/run: %#v / %#v", receipt, claimedRun)
	}

	replayedReceipt, replayedRun, replayed, err := store.ClaimOperation(ctx, claim)
	if err != nil || !replayed || replayedReceipt.RunRevisionAfter != 2 || replayedRun.StateRevision != 2 {
		t.Fatalf("claim replay: receipt=%#v run=%#v replayed=%v err=%v", replayedReceipt, replayedRun, replayed, err)
	}
	conflicting := claim
	conflicting.ActionID = "enable"
	if _, _, _, err := store.ClaimOperation(ctx, conflicting); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed idempotent request error = %v", err)
	}
	other := claim
	other.IfRevision = 2
	other.IdempotencyKey = "install-request-2"
	if _, _, _, err := store.ClaimOperation(ctx, other); !errors.Is(err, ErrOperationBusy) {
		t.Fatalf("parallel operation error = %v; want ErrOperationBusy", err)
	}

	uncertain, err := store.MarkOperationReconcileRequired(ctx, root.Kind, root.ID, claim.IdempotencyKey)
	if err != nil || uncertain.Status != OperationReconcileRequired {
		t.Fatalf("mark reconcile required: %#v, %v", uncertain, err)
	}
	claimedRun.Lifecycle = LifecycleInProgress
	claimedRun.IntegrationPluginID = "com.ori.reaper"
	claimedRun.IntegrationVersion = "0.5.0"
	claimedRun.StepStates[0].Status = StepComplete
	claimedRun.CurrentStepID = claimedRun.StepStates[1].StepID
	completion := OperationCompletion{
		Status: OperationSucceeded, ResultCode: "applied",
		Result: CanonicalResult{
			IntegrationPluginID: "com.ori.reaper", IntegrationVersion: "0.5.0",
			OwnerRevisions: []OwnerRevision{{Owner: OwnerPlugin, Revision: 7}},
		},
	}
	finished, finalRun, replayed, err := store.FinalizeOperation(ctx, claimedRun, claim.IdempotencyKey, completion)
	if err != nil || replayed {
		t.Fatalf("finalize operation: replayed=%v err=%v", replayed, err)
	}
	if finished.Status != OperationSucceeded || finished.CompletedAt == nil ||
		finalRun.StateRevision != 2 || finalRun.IntegrationPluginID != "com.ori.reaper" {
		t.Fatalf("unexpected final receipt/run: %#v / %#v", finished, finalRun)
	}
	if finished.Result.OwnerRevisions[0].Revision != 7 {
		t.Fatalf("canonical owner revision was not retained: %#v", finished.Result)
	}

	replayedFinished, replayedFinalRun, replayed, err := store.FinalizeOperation(ctx, claimedRun, claim.IdempotencyKey, completion)
	if err != nil || !replayed || replayedFinished.CompletedAt == nil || replayedFinalRun.StateRevision != 2 {
		t.Fatalf("finalization replay: receipt=%#v run=%#v replayed=%v err=%v", replayedFinished, replayedFinalRun, replayed, err)
	}
}

func TestSQLiteStoreFailedOperationReplaysDefinitiveReceipt(t *testing.T) {
	_, store := openTestStore(t)
	ctx := context.Background()
	root := createTestRoot(t, store)
	claim := OperationClaim{
		RunKind: RunKindRoot, RunID: root.ID, IfRevision: root.StateRevision,
		IdempotencyKey: "failed-request-1", StepID: "integration", ActionID: "install",
		InputDigest: Digest([]byte("input")),
	}
	_, claimed, _, err := store.ClaimOperation(ctx, claim)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	claimed.Lifecycle = LifecycleNeedsAttention
	claimed.StepStates[0] = StepState{StepID: "integration", Status: StepBlocked, ReasonCode: ReasonOperationFailed}
	completion := OperationCompletion{Status: OperationFailed, ResultCode: "not_applied", ReasonCode: ReasonOperationFailed}
	first, _, replayed, err := store.FinalizeOperation(ctx, claimed, claim.IdempotencyKey, completion)
	if err != nil || replayed || first.Status != OperationFailed {
		t.Fatalf("finalize failure: %#v replayed=%v err=%v", first, replayed, err)
	}
	second, _, replayed, err := store.FinalizeOperation(ctx, claimed, claim.IdempotencyKey, completion)
	if err != nil || !replayed || second.ReasonCode != ReasonOperationFailed {
		t.Fatalf("replay failure: %#v replayed=%v err=%v", second, replayed, err)
	}
}

func TestSQLiteStoreMalformedStructuralStateBecomesBoundedNeedsAttention(t *testing.T) {
	db, store := openTestStore(t)
	ctx := context.Background()
	root := createTestRoot(t, store)
	if _, err := db.ExecContext(ctx, `
		UPDATE setup_journey_run
		SET step_states_json = ?, project_workspace_id = ?
		WHERE id = ?
	`, `[{"step_id":"integration","status":"invented","leak":"/private/song.rpp"}]`, "/private/song.rpp", root.ID); err != nil {
		t.Fatalf("seed malformed legacy progress: %v", err)
	}
	got, err := store.GetRun(ctx, root.ID)
	if err != nil {
		t.Fatalf("read malformed structural progress: %v", err)
	}
	if !got.NeedsNormalization || got.Lifecycle != LifecycleNeedsAttention || len(got.StepStates) != 0 {
		t.Fatalf("malformed progress did not fail closed: %#v", got)
	}
	if got.ProjectWorkspaceID != "" || strings.Contains(got.ProjectWorkspaceID, "private") {
		t.Fatalf("malformed path escaped bounded projection: %#v", got)
	}

	got.StepStates, _ = normalizeStepIDs(testStepIDs)
	got.CurrentStepID = testStepIDs[0]
	got.Lifecycle = LifecycleInProgress
	repaired, err := store.CompareAndSwapRun(ctx, got, got.StateRevision)
	if err != nil {
		t.Fatalf("repair malformed structural progress: %v", err)
	}
	if repaired.NeedsNormalization || len(repaired.StepStates) != len(testStepIDs) {
		t.Fatalf("repaired progress stayed malformed: %#v", repaired)
	}
}

func TestSQLiteStoreRejectsSensitiveOrUnboundedReceiptData(t *testing.T) {
	_, store := openTestStore(t)
	ctx := context.Background()
	root := createTestRoot(t, store)
	root.ProjectWorkspaceID = "/Users/person/Music/song.rpp"
	if _, err := store.CompareAndSwapRun(ctx, root, 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("path-like project receipt error = %v", err)
	}
	root.ProjectWorkspaceID = ""

	badClaim := OperationClaim{
		RunKind: RunKindRoot, RunID: root.ID, IfRevision: 1,
		IdempotencyKey: "sk-secretcredential", StepID: "integration",
		ActionID: "install", InputDigest: Digest([]byte("safe normalized input")),
	}
	if _, _, _, err := store.ClaimOperation(ctx, badClaim); !errors.Is(err, ErrInvalid) {
		t.Fatalf("secret-like idempotency key error = %v", err)
	}

	goodClaim := badClaim
	goodClaim.IdempotencyKey = "safe-request"
	_, claimed, _, err := store.ClaimOperation(ctx, goodClaim)
	if err != nil {
		t.Fatalf("claim safe operation: %v", err)
	}
	badCompletion := OperationCompletion{
		Status: OperationSucceeded, ResultCode: "applied",
		Result: CanonicalResult{CanonicalReceiptID: "../../manifest.json"},
	}
	if _, _, _, err := store.FinalizeOperation(ctx, claimed, goodClaim.IdempotencyKey, badCompletion); !errors.Is(err, ErrInvalid) {
		t.Fatalf("path-like result receipt error = %v", err)
	}
}

func TestSQLiteStoreRestartRecoversRunAndUncertainClaim(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "journeys.db")
	db, err := database.Open(ctx, &database.Config{Path: dbPath, WALMode: false})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	store := NewSQLiteStore(db)
	root := createTestRoot(t, store)
	claim := OperationClaim{
		RunKind: RunKindRoot, RunID: root.ID, IfRevision: 1,
		IdempotencyKey: "restart-request", StepID: "integration",
		ActionID: "install", InputDigest: Digest([]byte("input")),
	}
	if _, _, _, err := store.ClaimOperation(ctx, claim); err != nil {
		t.Fatalf("claim before restart: %v", err)
	}
	if _, err := store.MarkOperationReconcileRequired(ctx, root.Kind, root.ID, claim.IdempotencyKey); err != nil {
		t.Fatalf("mark uncertain before restart: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	reopened, err := database.Open(ctx, &database.Config{Path: dbPath, WALMode: false})
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	restartedStore := NewSQLiteStore(reopened)
	got, err := restartedStore.GetRun(ctx, root.ID)
	if err != nil || got.StateRevision != 2 {
		t.Fatalf("run after restart: %#v, %v", got, err)
	}
	receipt, err := restartedStore.GetOperationReceipt(ctx, root.Kind, root.ID, claim.IdempotencyKey)
	if err != nil || receipt.Status != OperationReconcileRequired {
		t.Fatalf("uncertain receipt after restart: %#v, %v", receipt, err)
	}
	other := claim
	other.IfRevision = got.StateRevision
	other.IdempotencyKey = "other-request"
	if _, _, _, err := restartedStore.ClaimOperation(ctx, other); !errors.Is(err, ErrOperationBusy) {
		t.Fatalf("restart lost busy guard: %v", err)
	}
}
