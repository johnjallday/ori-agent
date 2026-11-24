package settingsreset

import (
	"os"
	"testing"
)

// The receipt-level half of the cancellation decision is a pure function of the
// strictly decoded journal. These are the receipts it must refuse, each one a
// look-alike of the incident shape that cannot be proven to have changed nothing.

func incidentReceipt(t *testing.T) *journal {
	t.Helper()
	fresh, err := Selection(IntentStartFresh, nil)
	mustPreview(t, err)
	j := compatRecoveryScopeChangedJournal(fresh, 3)
	if _, err := encodeJournal(j); err != nil {
		t.Fatalf("the incident receipt must stay a valid canonical journal: %v", err)
	}
	return j
}

func TestReceiptProvesPreApplyOnlyForTheIncidentShape(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*journal)
		want   CancelReason
	}{
		{"incident shape: blocked rev 3, every category pending", func(*journal) {}, CancelEligible},
		{"blocked rev 4 after an interrupted apply looks identical but is not provable", func(j *journal) { j.Operation.Revision = 4 }, CancelNotPreApply},
		{"blocked rev 5", func(j *journal) { j.Operation.Revision = 5 }, CancelNotPreApply},
		{"blocked rev 2 is a different (admission) lineage", func(j *journal) { j.Operation.Revision = 2 }, CancelNotPreApply},
		{"staged awaiting_restart is not blocked", func(j *journal) {
			j.Operation.State, j.Operation.Revision, j.Operation.Blockers = StateAwaitingRestart, 2, []Blocker{}
		}, CancelNotBlocked},
		{"applying is not blocked", func(j *journal) {
			j.Operation.State, j.Operation.Revision, j.Operation.Blockers = StateApplying, 3, []Blocker{}
		}, CancelNotBlocked},
		{"partial failure is not blocked", func(j *journal) { j.Operation.State = StatePartialFailure }, CancelNotBlocked},
		{"completed is not blocked", func(j *journal) { j.Operation.State = StateCompleted }, CancelNotBlocked},
		{"recovery_unavailable is another blocker", func(j *journal) { j.Operation.Blockers[0].Code = "recovery_unavailable" }, CancelOtherBlocker},
		{"drain_failed is another blocker", func(j *journal) { j.Operation.Blockers[0].Code = "drain_failed" }, CancelOtherBlocker},
		{"hq_archive_recovery is another blocker", func(j *journal) { j.Operation.Blockers[0].Code = "hq_archive_recovery" }, CancelOtherBlocker},
		{"no blocker", func(j *journal) { j.Operation.Blockers = nil }, CancelOtherBlocker},
		{"two blockers", func(j *journal) { j.Operation.Blockers = append(j.Operation.Blockers, j.Operation.Blockers[0]) }, CancelOtherBlocker},
		{"selected-data reset is not Start Fresh", func(j *journal) { j.Operation.Intent, j.Plan.Preview.Intent = IntentSelectedData, IntentSelectedData }, CancelNotStartFresh},
		{"HQ archive journal is excluded this increment", func(j *journal) { j.Archive = &archiveJournal{} }, CancelArchiveOperation},
		{"HQ archive evidence is excluded", func(j *journal) { j.Plan.Evidence.HQArchive = &hqArchiveEvidence{} }, CancelArchiveOperation},
		{"public HQ archive result is excluded", func(j *journal) { j.Operation.HQArchive = &HQArchiveResult{Outcome: OutcomeFailed} }, CancelArchiveOperation},
		{"one category not pending", func(j *journal) { j.Operation.Results[0].Outcome = OutcomeCompleted }, CancelResultsNotPending},
		{"one category failed", func(j *journal) { j.Operation.Results[0].Outcome = OutcomeFailed }, CancelResultsNotPending},
		{"one check not pending", func(j *journal) { j.Operation.Results[0].Checks[0].Outcome = OutcomeCompleted }, CancelResultsNotPending},
		{"no results", func(j *journal) { j.Operation.Results = nil }, CancelResultsNotPending},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := incidentReceipt(t)
			tc.mutate(j)
			if got := receiptProvesPreApply(j); got != tc.want {
				t.Fatalf("receiptProvesPreApply = %q, want %q", got, tc.want)
			}
		})
	}
	t.Run("nil receipt", func(t *testing.T) {
		if got := receiptProvesPreApply(nil); got != CancelNotBlocked {
			t.Fatalf("receiptProvesPreApply(nil) = %q", got)
		}
	})
}

// The frozen golden bytes of the incident must decode through the strict
// decoder and be eligible at the receipt level, so a future change to the
// journal rules cannot silently strand receipts already on disk.
func TestIncidentGoldenReceiptIsReceiptEligible(t *testing.T) {
	golden, err := os.ReadFile(compatGoldenPath("start-fresh-blocked-recovery-scope-changed")) // #nosec G304 -- checked-in test fixture
	mustPreview(t, err)
	j, err := decodeJournal(golden)
	mustPreview(t, err)
	if got := receiptProvesPreApply(j); got != CancelEligible {
		t.Fatalf("incident golden = %q", got)
	}
	if j.Operation.Revision != 3 || j.Operation.State != StateBlocked || len(j.Operation.Results) != 9 {
		t.Fatalf("incident golden drifted: rev %d, %s, %d categories", j.Operation.Revision, j.Operation.State, len(j.Operation.Results))
	}
}
