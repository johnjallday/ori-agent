package backuprecovery

import (
	"context"
	"time"
)

// Notes are closed choices, not a free-text secret sink. Reports are assertions
// by the local user; neither a note nor a pass is provider/Ori verification.
type RecoveryNote struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

func recoveryNotes() []RecoveryNote {
	return []RecoveryNote{
		{"", "No additional note"},
		{"sample_compared", "Sample opened and compared outside Ori."},
		{"restore_failed", "External sample restore did not complete."},
		{"sample_mismatch", "Recovered sample did not match the expected content."},
		{"key_unavailable", "Required recovery information was unavailable outside Ori."},
	}
}
func recoveryNote(code string) (string, bool) {
	for _, note := range recoveryNotes() {
		if note.Code == code {
			if code == "" {
				return "", true
			}
			return note.Label, true
		}
	}
	return "", false
}
func recoveryNoteCode(label string) string {
	for _, note := range recoveryNotes() {
		if note.Label == label {
			return note.Code
		}
	}
	return ""
}

type RecoveryReportRequest struct {
	Generation  string    `json:"generation"`
	Revision    uint64    `json:"revision"`
	OperationID string    `json:"operation_id"`
	EditID      string    `json:"edit_id,omitempty"`
	SnapshotID  string    `json:"snapshot_id,omitempty"`
	TestedAt    time.Time `json:"tested_at"`
	Result      string    `json:"result"`
	NoteCode    string    `json:"note_code"`
}
type RecoveryReport struct {
	ID         string    `json:"id"`
	Generation string    `json:"generation"`
	SnapshotID string    `json:"snapshot_id,omitempty"`
	ReportedAt time.Time `json:"reported_at"`
	TestedAt   time.Time `json:"tested_at"`
	Result     string    `json:"result"`
	NoteCode   string    `json:"note_code"`
	Supersedes string    `json:"supersedes,omitempty"`
	Historical bool      `json:"historical"`
	Superseded bool      `json:"superseded"`
}

func storedRecoveryReports(record localRecord) []RecoveryReport {
	reports := []RecoveryReport{}
	if record.state == nil {
		return reports
	}
	superseded := map[string]bool{}
	for _, report := range record.state.Reports {
		if report.Supersedes != "" {
			superseded[report.Supersedes] = true
		}
	}
	for _, report := range record.state.Reports {
		reports = append(reports, RecoveryReport{ID: report.ID, Generation: report.Generation, SnapshotID: report.SnapshotID, ReportedAt: report.ReportedAt, TestedAt: report.TestedAt, Result: report.Result, NoteCode: recoveryNoteCode(report.Note), Supersedes: report.Supersedes, Historical: report.Generation != record.state.Generation || record.state.Configuration == nil, Superseded: superseded[report.ID]})
	}
	return reports
}
func reportMatchesSnapshot(state *localState, id string) bool {
	if id == "" {
		return true
	}
	if state.Observation == nil || state.Observation.Generation != state.Generation {
		return false
	}
	for _, snapshot := range state.Observation.Snapshots {
		if snapshot.ID == id {
			return true
		}
	}
	return false
}
func reportEditable(state *localState, id string) bool {
	if id == "" {
		return true
	}
	found := false
	for _, report := range state.Reports {
		if report.Supersedes == id {
			return false
		}
		if report.ID == id && report.Generation == state.Generation {
			found = true
		}
	}
	return found
}

// RecordRecoveryReport writes one bounded immutable assertion/version and its
// replay outcome atomically. It does not read files, decrypt a backup password,
// validate a restore, create a receipt, or alter periodic consent.
func (s *Service) RecordRecoveryReport(ctx context.Context, owner, workspace string, request RecoveryReportRequest) (OperationResult, error) {
	release, err := s.enter()
	if err != nil {
		return OperationResult{}, err
	}
	defer release()
	note, validNote := recoveryNote(request.NoteCode)
	now := s.clock().UTC()
	if !validOperationID(request.OperationID) || request.EditID != "" && !validOperationID(request.EditID) || request.SnapshotID != "" && !metadataID(request.SnapshotID) || !localTime(request.TestedAt) || request.TestedAt.After(now.Add(5*time.Minute)) || !validNote || request.Result != "user_reported_passed" && request.Result != "user_reported_failed" || request.Result == "user_reported_passed" && request.NoteCode != "" && request.NoteCode != "sample_compared" {
		return OperationResult{}, ErrInvalidRequest
	}
	if _, err := s.record(ctx, owner, workspace); err != nil {
		return OperationResult{}, err
	}
	slot := s.slot(workspace)
	unlock, err := lockSlot(ctx, slot)
	if err != nil {
		return OperationResult{}, err
	}
	defer unlock()
	record, err := s.record(ctx, owner, workspace)
	if err != nil {
		return OperationResult{}, err
	}
	if err := metadataAdmission(record); err != nil {
		return OperationResult{}, err
	}
	digest := localDigest([]any{owner, workspace, "recovery_report", request})
	if result, replayed, err := s.replay(ctx, record, slot, request.OperationID, "recovery_report", digest); replayed || err != nil {
		return result, err
	}
	if !expectedRequest(record, request.Generation, request.Revision) || record.state == nil || record.state.Configuration == nil {
		return OperationResult{}, ErrConfigurationChanged
	}
	for _, report := range record.state.Reports {
		if report.ID == request.OperationID {
			return OperationResult{}, ErrReviewExpired
		}
	}
	if !reportMatchesSnapshot(record.state, request.SnapshotID) || !reportEditable(record.state, request.EditID) {
		return OperationResult{}, ErrConfigurationChanged
	}
	updated, err := s.store.update(ctx, owner, workspace, record.expected(), changeUser, func(current *localRecord) error {
		if current.state.Configuration == nil || !reportMatchesSnapshot(current.state, request.SnapshotID) || !reportEditable(current.state, request.EditID) {
			return ErrConfigurationChanged
		}
		report := localRestoreReport{ID: request.OperationID, Generation: current.state.Generation, SnapshotID: request.SnapshotID, TestedAt: request.TestedAt, ReportedAt: now, Result: request.Result, Note: note, Supersedes: request.EditID}
		current.state.Reports = append([]localRestoreReport{report}, current.state.Reports...)
		if len(current.state.Reports) > maxRestoreReports {
			current.state.Reports = current.state.Reports[:maxRestoreReports]
		}
		operation := pendingOperation(current.state, request.OperationID, "recovery_report", digest, now)
		operation.Status = "complete"
		operation.Outcome = "applied"
		appendOperation(current.state, operation, now)
		return nil
	})
	if err != nil {
		return OperationResult{}, err
	}
	s.clearReviews(workspace)
	operation, _ := findOperation(updated, request.OperationID)
	return operationResult(operation), nil
}
