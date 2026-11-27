package filejanitor

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestLookupFilingHistorySearchesRetainedJournalNotTheFolder(t *testing.T) {
	s, ws, root := curatorWelcomeFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	_, err := s.store.UpdateScanState(ws.ID, func(st *ScanState) error {
		for i, result := range []ActionResult{ResultApplied, ResultFailed, ResultStale, ResultApplying, ResultApplied} {
			a := FileAction{WorkspaceID: ws.ID, ApprovedBy: "local", SourceName: "hotel-receipt.pdf", Operation: OperationMove, DestinationRelative: "Filed/Documents/hotel-receipt.pdf", CompletedAt: now.Add(time.Duration(i) * time.Hour), Result: result, Undo: UndoDone}
			if i == 4 {
				a.SourceName = "hotel-receipt-may.pdf"
				a.Undo = UndoFailed
			}
			st.Actions = append(st.Actions, a)
		}
		for _, scope := range []struct{ workspace, owner string }{{"other", "local"}, {ws.ID, "former-owner"}} {
			st.Actions = append(st.Actions, FileAction{WorkspaceID: scope.workspace, ApprovedBy: scope.owner, CompletedAt: now, SourceName: "hotel-receipt-other.pdf", Operation: OperationMove, Result: ResultApplied, DestinationRelative: "Filed/other.pdf"})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(root, root+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	got, err := s.LookupFilingHistory(t.Context(), "local", ws.ID, "Hotel RECEIPT")
	if err != nil || got.Total != 4 || got.Matches[0].Name != "hotel-receipt-may.pdf" || !strings.Contains(got.Matches[0].Outcome, "Undo failed") || !strings.Contains(got.Matches[3].Outcome, "put back") || !got.Matches[3].When.Equal(now) {
		t.Fatalf("lookup: %+v %v", got, err)
	}
	for _, m := range got.Matches[1:3] {
		if strings.Contains(m.Outcome, "Filed to") {
			t.Fatal(m)
		}
	}
	got, err = s.LookupFilingHistory(t.Context(), "local", ws.ID, "missing")
	if err != nil || got.Total != 0 || !strings.Contains(got.Note, "retained history") {
		t.Fatal(got, err)
	}
	if _, err = s.LookupFilingHistory(t.Context(), "other", ws.ID, "receipt"); err == nil {
		t.Fatal("other owner read journal")
	}
}
