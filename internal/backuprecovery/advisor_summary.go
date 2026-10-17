package backuprecovery

import (
	"context"
	"time"
)

// AdvisorFacts is a deliberately separate DTO, not Status/Settings or a private
// persistence object. No root/path, volume/repository/snapshot ID, Vault reference,
// review/grant token, freeform report note or external label has a field here.
type AdvisorFacts struct {
	Connection         string     `json:"connection"`
	Destination        string     `json:"destination"`
	Monitoring         string     `json:"monitoring"`
	ReaderAvailable    bool       `json:"reader_available"`
	LastSuccessfulRead *time.Time `json:"last_successful_read,omitempty"`
	LastCompletedAt    *time.Time `json:"last_completed_at,omitempty"`
	SnapshotCount      *int       `json:"snapshot_count,omitempty"`
	LastAttemptOutcome string     `json:"last_attempt_outcome,omitempty"`
	Route              string     `json:"route"`
}

func (s *Service) ReadBackupAdvisor(ctx context.Context, owner, workspace string) (AdvisorFacts, error) {
	status, err := s.GetStatus(ctx, owner, workspace)
	if err != nil {
		return AdvisorFacts{}, err
	}
	facts := AdvisorFacts{Connection: status.Connection, Destination: status.Destination, Monitoring: status.Monitoring, ReaderAvailable: status.RuntimeAvailable, LastSuccessfulRead: status.LastSuccessfulRead, LastCompletedAt: status.LastCompletedAt, SnapshotCount: status.SnapshotCount, Route: "/workspaces/" + workspace + "?panel=backup-recovery"}
	if status.LastAttempt != nil && status.LastAttempt.Generation == status.Generation {
		facts.LastAttemptOutcome = status.LastAttempt.Outcome
	}
	return facts, nil
}
