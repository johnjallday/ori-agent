package downloadsjanitor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// flaggedFixture sets up a workspace against an isolated inbox, scans it, and
// returns the first low-confidence candidate. A .bin file is the classifier's
// canonical "could hold anything" case.
func flaggedFixture(t *testing.T) (*Service, JanitorCandidate) {
	t.Helper()
	service, _ := newTestService(t)

	root := filepath.Join(tempDirCanonical(t), "Inbox")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmSetup(SetupRequest{WorkspaceID: "ws-1", Path: root}); err != nil {
		t.Fatalf("ConfirmSetup: %v", err)
	}
	writeAged(t, root, "payload.bin", "some bytes")

	batch, _, err := service.ScanNow("ws-1", ScanSourceManual)
	if err != nil {
		t.Fatalf("ScanNow: %v", err)
	}
	_, candidates, err := service.BatchDetail("ws-1", batch.ID)
	if err != nil {
		t.Fatalf("BatchDetail: %v", err)
	}
	for _, candidate := range candidates {
		if candidate.NeedsReview {
			return service, candidate
		}
	}
	t.Fatal("fixture produced no low-confidence candidate; the gate would be untested")
	return nil, JanitorCandidate{}
}

// A file Ori could not place confidently must not be filed on that guess.
//
// "Needs review" is a statement that the proposal is not trustworthy. Treating
// an empty category as "keep what Ori proposed" — correct for a confident
// classification — would file exactly the case the flag exists to reject, and
// a bulk approval is where that happens without anyone reading the row.
func TestPreviewMoves_RefusesAnUnresolvedNeedsReviewFile(t *testing.T) {
	service, flagged := flaggedFixture(t)

	_, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1",
		UserID:      "user-1",
		Items: []PreviewRequestItem{
			{CandidateID: flagged.ID, Operation: OperationMove},
		},
	})
	if err == nil {
		t.Fatal("a flagged file must not be filed on the guess that was flagged")
	}
	if !errors.Is(err, ErrCandidateNotActionable) {
		t.Fatalf("error = %v, want ErrCandidateNotActionable", err)
	}
	if !strings.Contains(err.Error(), "category you choose") {
		t.Errorf("error should say what the user must do: %v", err)
	}
}

// The user's own choice resolves it. This is the other half: the gate must not
// become a wall that makes a flagged file unfilable.
func TestPreviewMoves_AcceptsAFlaggedFileTheUserCategorized(t *testing.T) {
	service, flagged := flaggedFixture(t)

	preview, err := service.PreviewMoves(PreviewRequest{
		WorkspaceID: "ws-1",
		UserID:      "user-1",
		Items: []PreviewRequestItem{
			{CandidateID: flagged.ID, Operation: OperationMove, Category: "documents"},
		},
	})
	if err != nil {
		t.Fatalf("an explicitly categorized file must be filable: %v", err)
	}
	if len(preview.Items) != 1 {
		t.Fatalf("preview items = %d, want 1", len(preview.Items))
	}
}
