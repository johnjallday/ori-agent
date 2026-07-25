package downloadsjanitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realMover performs the move the way a working filesystem MCP server would.
type realMover struct {
	calls int
	// beforeMove runs just before the rename, to stage races.
	beforeMove func(source, destination string)
}

func (m *realMover) Move(_ context.Context, _, source, destination string) error {
	m.calls++
	if m.beforeMove != nil {
		m.beforeMove(source, destination)
	}
	return os.Rename(source, destination)
}

// lyingMover reports success without moving anything — the failure mode that
// makes "trust the tool's word" unsafe.
type lyingMover struct{ calls int }

func (m *lyingMover) Move(context.Context, string, string, string) error {
	m.calls++
	return nil
}

// ambiguousMover performs the move but reports an error anyway, as a tool can
// when a response is lost or malformed.
type ambiguousMover struct{}

func (m *ambiguousMover) Move(_ context.Context, _, source, destination string) error {
	_ = os.Rename(source, destination)
	return errors.New("connection reset")
}

// failingMover refuses the move and changes nothing.
type failingMover struct{}

func (m *failingMover) Move(context.Context, string, string, string) error {
	return errors.New("permission denied")
}

// approveAndConfirm previews the given candidates and confirms the plan.
func approveAndConfirm(t *testing.T, service *Service, candidates []JanitorCandidate, category string) ApplyResult {
	t.Helper()
	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, category),
	})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}
	result, err := service.ConfirmMoves(context.Background(), ConfirmRequest{
		WorkspaceID: "ws-1", UserID: "user-1", BatchID: preview.BatchID,
		Token: preview.Token, Items: moveItems(candidates, category),
	})
	if err != nil {
		t.Fatalf("ConfirmMoves: %v", err)
	}
	return result
}

func TestConfirmMoves_MovesTheFileAndJournalsTheApproval(t *testing.T) {
	service, root, candidates := reviewFixture(t, "report.pdf")
	mover := &realMover{}
	service.SetMover(mover)

	result := approveAndConfirm(t, service, candidates, "")
	if result.Applied != 1 || result.Failed != 0 || result.Stale != 0 {
		t.Fatalf("result = %+v", result)
	}
	outcome := result.Outcomes[0]
	if outcome.Destination != "Filed/Documents/report.pdf" || !outcome.Undoable {
		t.Fatalf("outcome = %+v", outcome)
	}

	// The file really moved.
	if _, err := os.Stat(filepath.Join(root, "report.pdf")); !os.IsNotExist(err) {
		t.Fatalf("the source should be gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Filed", "Documents", "report.pdf")); err != nil {
		t.Fatalf("the file should be filed: %v", err)
	}

	// The journal records who approved it, when, and against which file state.
	actions, err := service.ListActions("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 {
		t.Fatalf("expected one journal entry, got %d", len(actions))
	}
	action := actions[0]
	if action.ApprovedBy != "user-1" || action.ApprovedAt.IsZero() {
		t.Fatalf("journal must record the approval: %+v", action)
	}
	if action.Result != ResultApplied || action.AfterFingerprint.Zero() {
		t.Fatalf("journal must record the verified outcome: %+v", action)
	}
	if !action.BeforeFingerprint.Matches(candidates[0].Fingerprint) {
		t.Fatal("journal must record the file state that was approved")
	}
}

// A mover that claims success without moving the file must not produce an
// "applied" result. Believing it would tell the user their folder was tidied
// when nothing happened.
func TestConfirmMoves_DoesNotTrustAToolThatLiesAboutSuccess(t *testing.T) {
	service, root, candidates := reviewFixture(t, "report.pdf")
	mover := &lyingMover{}
	service.SetMover(mover)

	result := approveAndConfirm(t, service, candidates, "")
	if mover.calls != 1 {
		t.Fatalf("the mover should have been called once, got %d", mover.calls)
	}
	if result.Applied != 0 || result.Failed != 1 {
		t.Fatalf("an unverified move must not count as applied: %+v", result)
	}
	if !strings.Contains(strings.ToLower(result.Outcomes[0].Message), "could not confirm") {
		t.Fatalf("the message should say the move was not confirmed: %q", result.Outcomes[0].Message)
	}
	// The source is still where it was, which is the truth being reported.
	if _, err := os.Stat(filepath.Join(root, "report.pdf")); err != nil {
		t.Fatalf("the source should be untouched: %v", err)
	}
	actions, _ := service.ListActions("ws-1")
	if actions[0].Result != ResultFailed {
		t.Fatalf("the journal must record the failure: %+v", actions[0])
	}
}

// The mirror image: a tool that errors after the move succeeded. The filesystem
// is the authority, so this is reported as applied.
func TestConfirmMoves_ReconcilesAnAmbiguousToolResult(t *testing.T) {
	service, root, candidates := reviewFixture(t, "report.pdf")
	service.SetMover(&ambiguousMover{})

	result := approveAndConfirm(t, service, candidates, "")
	if result.Applied != 1 {
		t.Fatalf("a move that demonstrably happened must be reported as applied: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "Filed", "Documents", "report.pdf")); err != nil {
		t.Fatalf("the file should be filed: %v", err)
	}
}

func TestConfirmMoves_AFailedMoveLeavesTheSourceAlone(t *testing.T) {
	service, root, candidates := reviewFixture(t, "report.pdf")
	service.SetMover(&failingMover{})

	result := approveAndConfirm(t, service, candidates, "")
	if result.Failed != 1 || result.Applied != 0 {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "report.pdf")); err != nil {
		t.Fatalf("a failed move must leave the source in place: %v", err)
	}
	if result.Outcomes[0].Undoable {
		t.Fatal("a failed action is not undoable")
	}
}

// One failure must not roll back or block the others.
func TestConfirmMoves_AppliesPerItemSoOneFailureDoesNotSinkTheBatch(t *testing.T) {
	service, root, candidates := reviewFixture(t, "a.pdf", "b.pdf", "c.pdf")

	// Fail only the middle file.
	service.SetMover(&selectiveMover{failFor: "b.pdf"})
	result := approveAndConfirm(t, service, candidates, "")

	if result.Applied != 2 || result.Failed != 1 {
		t.Fatalf("expected a mixed result, got %+v", result)
	}
	if len(result.Outcomes) != 3 {
		t.Fatalf("every item needs its own outcome: %+v", result.Outcomes)
	}
	for _, outcome := range result.Outcomes {
		if outcome.Name == "b.pdf" && outcome.Result != ResultFailed {
			t.Fatalf("b.pdf should have failed: %+v", outcome)
		}
		if outcome.Name != "b.pdf" && outcome.Result != ResultApplied {
			t.Fatalf("%s should have been applied: %+v", outcome.Name, outcome)
		}
	}
	// The successes stand; the failure is still in the folder.
	if _, err := os.Stat(filepath.Join(root, "Filed", "Documents", "a.pdf")); err != nil {
		t.Fatalf("a.pdf should be filed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "b.pdf")); err != nil {
		t.Fatalf("b.pdf should still be in the folder: %v", err)
	}
}

type selectiveMover struct{ failFor string }

func (m *selectiveMover) Move(_ context.Context, _, source, destination string) error {
	if filepath.Base(source) == m.failFor {
		return errors.New("permission denied")
	}
	return os.Rename(source, destination)
}

// A file that changes between approval and execution must be refused, not moved
// on the strength of a stale plan.
func TestConfirmMoves_RefusesASourceThatChangedAfterApproval(t *testing.T) {
	service, root, candidates := reviewFixture(t, "report.pdf")
	mover := &realMover{}
	service.SetMover(mover)

	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, ""),
	})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}
	// The file is rewritten after the plan was approved.
	if err := os.WriteFile(filepath.Join(root, "report.pdf"), []byte("different contents entirely"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := service.ConfirmMoves(context.Background(), ConfirmRequest{
		WorkspaceID: "ws-1", UserID: "user-1", BatchID: preview.BatchID,
		Token: preview.Token, Items: moveItems(candidates, ""),
	})
	if err != nil {
		t.Fatalf("ConfirmMoves: %v", err)
	}
	if result.Stale != 1 || result.Applied != 0 {
		t.Fatalf("a changed file must be marked stale: %+v", result)
	}
	if mover.calls != 0 {
		t.Fatal("a stale item must never reach the mover")
	}
	if _, err := os.Stat(filepath.Join(root, "report.pdf")); err != nil {
		t.Fatalf("the file must be left where it is: %v", err)
	}
	if !strings.Contains(strings.ToLower(result.Outcomes[0].Message), "rescan") {
		t.Fatalf("the user should be told to rescan: %q", result.Outcomes[0].Message)
	}
}

// A file swapped for a different one with the same name, size, and timestamp is
// caught by the platform file identity.
func TestConfirmMoves_RefusesAReplacedSource(t *testing.T) {
	service, root, candidates := reviewFixture(t, "report.pdf")
	mover := &realMover{}
	service.SetMover(mover)

	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, ""),
	})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}

	// Replace the file in place, preserving name, size, and modification time.
	path := filepath.Join(root, "report.pdf")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, info.Size()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}

	after, err := currentFingerprint(root, "report.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if after.FileID == "" {
		t.Skip("platform exposes no file identity, so a same-size same-time swap is undetectable")
	}

	result, err := service.ConfirmMoves(context.Background(), ConfirmRequest{
		WorkspaceID: "ws-1", UserID: "user-1", BatchID: preview.BatchID,
		Token: preview.Token, Items: moveItems(candidates, ""),
	})
	if err != nil {
		t.Fatalf("ConfirmMoves: %v", err)
	}
	if result.Stale != 1 || mover.calls != 0 {
		t.Fatalf("a replaced file must be refused: %+v (mover calls %d)", result, mover.calls)
	}
}

// A destination that becomes occupied between preview and apply must be
// re-resolved, never overwritten.
func TestConfirmMoves_ReResolvesADestinationTakenAfterPreview(t *testing.T) {
	service, root, candidates := reviewFixture(t, "report.pdf")
	service.SetMover(&realMover{})

	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, ""),
	})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}
	if preview.Items[0].Destination != "Filed/Documents/report.pdf" {
		t.Fatalf("preview destination = %q", preview.Items[0].Destination)
	}

	// Something else takes the previewed name in the meantime.
	destination := filepath.Join(root, "Filed", "Documents")
	if err := os.MkdirAll(destination, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "report.pdf"), []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := service.ConfirmMoves(context.Background(), ConfirmRequest{
		WorkspaceID: "ws-1", UserID: "user-1", BatchID: preview.BatchID,
		Token: preview.Token, Items: moveItems(candidates, ""),
	})
	if err != nil {
		t.Fatalf("ConfirmMoves: %v", err)
	}
	if result.Applied != 1 {
		t.Fatalf("result = %+v", result)
	}
	if result.Outcomes[0].Destination != "Filed/Documents/report (2).pdf" {
		t.Fatalf("destination should have been re-resolved, got %q", result.Outcomes[0].Destination)
	}
	// The occupying file is untouched — Ori never overwrites.
	data, err := os.ReadFile(filepath.Join(destination, "report.pdf"))
	if err != nil || string(data) != "occupied" {
		t.Fatalf("the occupying file must survive: %q %v", string(data), err)
	}
}

// Retrying the same confirm must not move anything twice.
func TestConfirmMoves_ARetryReportsTheOriginalOutcomeWithoutRepeatingIt(t *testing.T) {
	service, root, candidates := reviewFixture(t, "report.pdf")
	mover := &realMover{}
	service.SetMover(mover)

	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, ""),
	})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}
	request := ConfirmRequest{
		WorkspaceID: "ws-1", UserID: "user-1", BatchID: preview.BatchID,
		Token: preview.Token, Items: moveItems(candidates, ""),
	}

	first, err := service.ConfirmMoves(context.Background(), request)
	if err != nil {
		t.Fatalf("ConfirmMoves: %v", err)
	}
	second, err := service.ConfirmMoves(context.Background(), request)
	if err != nil {
		t.Fatalf("retried ConfirmMoves: %v", err)
	}

	if !second.Replayed {
		t.Fatalf("a retry should be reported as a replay: %+v", second)
	}
	if second.Applied != first.Applied || len(second.Outcomes) != len(first.Outcomes) {
		t.Fatalf("a replay should report the original outcome: %+v vs %+v", second, first)
	}
	if mover.calls != 1 {
		t.Fatalf("the move must happen exactly once, got %d calls", mover.calls)
	}
	// Exactly one journal entry, and one file in the destination.
	actions, _ := service.ListActions("ws-1")
	if len(actions) != 1 {
		t.Fatalf("a retry must not journal a second action: %d", len(actions))
	}
	entries, err := os.ReadDir(filepath.Join(root, "Filed", "Documents"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("the file must exist exactly once in the destination: %v", entries)
	}
}

func TestConfirmMoves_RequiresAValidApproval(t *testing.T) {
	service, _, candidates := reviewFixture(t, "report.pdf")
	mover := &realMover{}
	service.SetMover(mover)

	// No token at all.
	if _, err := service.ConfirmMoves(context.Background(), ConfirmRequest{
		WorkspaceID: "ws-1", UserID: "user-1", BatchID: candidates[0].BatchID, Items: moveItems(candidates, ""),
	}); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("expected ErrApprovalRequired, got %v", err)
	}
	// A made-up token.
	if _, err := service.ConfirmMoves(context.Background(), ConfirmRequest{
		WorkspaceID: "ws-1", UserID: "user-1", BatchID: candidates[0].BatchID,
		Token: "forged", Items: moveItems(candidates, ""),
	}); !errors.Is(err, ErrApprovalInvalid) {
		t.Fatalf("expected ErrApprovalInvalid, got %v", err)
	}
	if mover.calls != 0 {
		t.Fatal("nothing may be moved without a valid approval")
	}
}

// Changing the plan after approval must invalidate it, even though the
// candidate IDs are unchanged.
func TestConfirmMoves_RejectsACategorySwappedAfterApproval(t *testing.T) {
	service, root, candidates := reviewFixture(t, "report.pdf")
	mover := &realMover{}
	service.SetMover(mover)

	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, "documents"),
	})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}
	// Confirm with a different category than was approved.
	_, err = service.ConfirmMoves(context.Background(), ConfirmRequest{
		WorkspaceID: "ws-1", UserID: "user-1", BatchID: preview.BatchID,
		Token: preview.Token, Items: moveItems(candidates, "installers"),
	})
	if !errors.Is(err, ErrApprovalInvalid) {
		t.Fatalf("expected the approval to be invalidated, got %v", err)
	}
	if mover.calls != 0 {
		t.Fatal("nothing may move under a mismatched approval")
	}
	if _, statErr := os.Stat(filepath.Join(root, "report.pdf")); statErr != nil {
		t.Fatalf("the file must be untouched: %v", statErr)
	}
}

// Every mutation is journaled before it is attempted, so a crash mid-apply
// leaves evidence rather than a silent gap.
func TestConfirmMoves_JournalsBeforeMutating(t *testing.T) {
	service, _, candidates := reviewFixture(t, "report.pdf")
	var journaledBeforeMove bool
	service.SetMover(&realMover{beforeMove: func(string, string) {
		state, err := service.store.LoadScanState("ws-1")
		if err == nil && len(state.Actions) == 1 && state.Actions[0].Result == ResultApplying {
			journaledBeforeMove = true
		}
	}})

	approveAndConfirm(t, service, candidates, "")
	if !journaledBeforeMove {
		t.Fatal("the action must be journaled, in the applying state, before the move is issued")
	}
}

func TestConfirmMoves_RequiresAConfiguredMover(t *testing.T) {
	service, _, candidates := reviewFixture(t, "report.pdf")
	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1", UserID: "user-1", Items: moveItems(candidates, ""),
	})
	if err != nil {
		t.Fatalf("PreviewMoves: %v", err)
	}
	if _, err := service.ConfirmMoves(context.Background(), ConfirmRequest{
		WorkspaceID: "ws-1", UserID: "user-1", BatchID: preview.BatchID,
		Token: preview.Token, Items: moveItems(candidates, ""),
	}); err == nil {
		t.Fatal("without a move mechanism the apply must fail rather than pretend")
	}
}

// The candidate's state follows the action, so the review surface shows what
// actually happened.
func TestConfirmMoves_UpdatesCandidateStateAndBatchSummary(t *testing.T) {
	service, _, candidates := reviewFixture(t, "a.pdf", "b.pdf")
	service.SetMover(&selectiveMover{failFor: "b.pdf"})

	approveAndConfirm(t, service, candidates, "")

	batch, stored, err := service.BatchDetail("ws-1", candidates[0].BatchID)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range stored {
		switch candidate.Name {
		case "a.pdf":
			if candidate.State != CandidateApplied {
				t.Fatalf("a.pdf state = %q", candidate.State)
			}
		case "b.pdf":
			if candidate.State != CandidateFailed || candidate.StateReason == "" {
				t.Fatalf("b.pdf should record its failure: %+v", candidate)
			}
		}
	}
	if batch.Summary.Applied != 1 || batch.Summary.Failed != 1 {
		t.Fatalf("batch summary should reflect the mixed result: %+v", batch.Summary)
	}
}
