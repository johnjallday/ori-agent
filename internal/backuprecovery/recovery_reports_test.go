//go:build darwin || linux

package backuprecovery

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func reportRequestFixture(t *testing.T, s *Service) RecoveryReportRequest {
	t.Helper()
	status, err := s.GetStatus(t.Context(), "local", localFixtureWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	return RecoveryReportRequest{Generation: status.Generation, Revision: status.Revision, OperationID: uuid.NewString(), Result: "user_reported_passed", TestedAt: status.ServerTime.Add(-time.Hour), NoteCode: "sample_compared"}
}
func TestRecoveryReportsAreDatedUserAssertionsWithReplayAndImmutableEdits(t *testing.T) {
	s, provider, credentials := recoveryServiceFixture(t, false)
	before, _ := s.store.load(t.Context(), "local", localFixtureWorkspace)
	request := reportRequestFixture(t, s)
	request.SnapshotID = before.state.Observation.Snapshots[0].ID
	result, err := s.RecordRecoveryReport(t.Context(), "local", localFixtureWorkspace, request)
	if err != nil || result.Outcome != "applied" {
		t.Fatal(result, err)
	}
	repeated, err := s.RecordRecoveryReport(t.Context(), "local", localFixtureWorkspace, request)
	if err != nil || repeated != result {
		t.Fatal("identical report replay changed its outcome", repeated, err)
	}
	altered := request
	altered.Result = "user_reported_failed"
	if _, err := s.RecordRecoveryReport(t.Context(), "local", localFixtureWorkspace, altered); !errors.Is(err, ErrConfigurationChanged) {
		t.Fatal("UUID accepted changed report", err)
	}
	edit := reportRequestFixture(t, s)
	edit.EditID = request.OperationID
	edit.Result = "user_reported_failed"
	edit.NoteCode = "sample_mismatch"
	edit.SnapshotID = request.SnapshotID
	if _, err := s.RecordRecoveryReport(t.Context(), "local", localFixtureWorkspace, edit); err != nil {
		t.Fatal(err)
	}
	view, err := s.GetRecovery(t.Context(), "local", localFixtureWorkspace)
	if err != nil || len(view.Reports) != 2 || view.Reports[0].Result != "user_reported_failed" || view.Reports[0].Supersedes != request.OperationID || !view.Reports[1].Superseded {
		t.Fatal("edit lost its prior user report", view, err)
	}
	stale := reportRequestFixture(t, s)
	stale.EditID = request.OperationID
	if _, err := s.RecordRecoveryReport(t.Context(), "local", localFixtureWorkspace, stale); !errors.Is(err, ErrConfigurationChanged) {
		t.Fatal("edited superseded version", err)
	}
	after, _ := s.store.load(t.Context(), "local", localFixtureWorkspace)
	if len(after.state.Reports) != 2 || localDigest(before.receipt) != localDigest(after.receipt) || localDigest(before.state.Observation) != localDigest(after.state.Observation) || provider.calls.Load() != 1 || credentials.uses.Load() != 1 {
		t.Fatal("report changed access, observation or performed IO")
	}
	control := ControlRequest{after.state.Generation, after.state.Revision, uuid.NewString()}
	if _, err := s.Revoke(t.Context(), "local", localFixtureWorkspace, control, false); err != nil {
		t.Fatal(err)
	}
	view, err = s.GetRecovery(t.Context(), "local", localFixtureWorkspace)
	if err != nil || !view.Reports[0].Historical || !view.Reports[1].Historical {
		t.Fatal("reconfiguration promoted old user reports", err)
	}
}
func TestRecoveryReportRejectsForgedProofSecretsFutureAndStaleScope(t *testing.T) {
	s, provider, credentials := recoveryServiceFixture(t, false)
	request := reportRequestFixture(t, s)
	cases := map[string]func(*RecoveryReportRequest){
		"forged proof":     func(r *RecoveryReportRequest) { r.Result = "ori_verified" },
		"secret note":      func(r *RecoveryReportRequest) { r.NoteCode = "password=invented-do-not-store" },
		"future":           func(r *RecoveryReportRequest) { r.TestedAt = r.TestedAt.Add(48 * time.Hour) },
		"zero date":        func(r *RecoveryReportRequest) { r.TestedAt = time.Time{} },
		"conflicting note": func(r *RecoveryReportRequest) { r.NoteCode = "key_unavailable" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			changed := request
			change(&changed)
			if _, err := s.RecordRecoveryReport(t.Context(), "local", localFixtureWorkspace, changed); !errors.Is(err, ErrInvalidRequest) {
				t.Fatal(err)
			}
		})
	}
	wrongSnapshot := request
	wrongSnapshot.SnapshotID = localDigest("unknown snapshot")
	if _, err := s.RecordRecoveryReport(t.Context(), "local", localFixtureWorkspace, wrongSnapshot); !errors.Is(err, ErrConfigurationChanged) {
		t.Fatal("invented snapshot association", err)
	}
	if _, err := s.RecordRecoveryReport(t.Context(), "other", localFixtureWorkspace, request); !errors.Is(err, ErrWorkspaceUnavailable) {
		t.Fatal("foreign report", err)
	}
	if _, err := s.RecordRecoveryReport(t.Context(), "local", localFixtureWorkspace, request); err != nil {
		t.Fatal(err)
	}
	request.OperationID = uuid.NewString()
	if _, err := s.RecordRecoveryReport(t.Context(), "local", localFixtureWorkspace, request); !errors.Is(err, ErrConfigurationChanged) {
		t.Fatal("stale edit accepted", err)
	}
	view, _ := s.GetRecovery(t.Context(), "local", localFixtureWorkspace)
	if len(view.Reports) != 1 || provider.calls.Load() != 1 || credentials.uses.Load() != 1 {
		t.Fatal("invalid report created data or IO")
	}
}
func TestRecoveryReportWriteFailureIsAtomicAndHistoryIsBounded(t *testing.T) {
	s, provider, _ := recoveryServiceFixture(t, false)
	request := reportRequestFixture(t, s)
	if _, err := s.store.db.ExecContext(t.Context(), `CREATE TRIGGER refuse_report_fixture BEFORE UPDATE ON backup_recovery_local BEGIN SELECT RAISE(ABORT,'invented private failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordRecoveryReport(t.Context(), "local", localFixtureWorkspace, request); err != ErrUnavailable {
		t.Fatal("write failure escaped or succeeded", err)
	}
	state, _ := s.store.load(t.Context(), "local", localFixtureWorkspace)
	if len(state.state.Reports) != 0 || state.state.Revision != request.Revision {
		t.Fatal("failed write left a report/outcome")
	}
	if _, err := s.store.db.ExecContext(t.Context(), `DROP TRIGGER refuse_report_fixture`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxRestoreReports+1; i++ {
		r := reportRequestFixture(t, s)
		if _, err := s.RecordRecoveryReport(t.Context(), "local", localFixtureWorkspace, r); err != nil {
			t.Fatal(err)
		}
	}
	view, err := s.GetRecovery(t.Context(), "local", localFixtureWorkspace)
	if err != nil || len(view.Reports) != maxRestoreReports || provider.calls.Load() != 1 {
		t.Fatal("report history became unbounded or probed", err)
	}
}
