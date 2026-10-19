package backuprecovery

import (
	"context"
	"time"
)

// AdvisorFacts is a deliberately separate DTO, not Status/Settings or a private
// persistence object. No root/path, volume/repository/snapshot ID, Vault reference,
// review/grant token, freeform report note or external label has a field here.
type AdvisorFacts struct {
	Provider           string         `json:"provider,omitempty"`
	SourceCoverage     []string       `json:"source_coverage,omitempty"`
	Connection         string         `json:"connection"`
	Destination        string         `json:"destination"`
	Monitoring         string         `json:"monitoring"`
	ReaderAvailable    bool           `json:"reader_available"`
	LastSuccessfulRead *time.Time     `json:"last_successful_read,omitempty"`
	LastCompletedAt    *time.Time     `json:"last_completed_at,omitempty"`
	SnapshotCount      *int           `json:"snapshot_count,omitempty"`
	LastAttemptOutcome string         `json:"last_attempt_outcome,omitempty"`
	Route              string         `json:"route"`
	AsOf               time.Time      `json:"as_of"`
	EvidenceCurrent    bool           `json:"evidence_current_generation"`
	Health             EvidenceHealth `json:"health"`
	RecoveryState      string         `json:"recovery_state"`
	RecoveryTestedAt   *time.Time     `json:"recovery_tested_at,omitempty"`
}

func (s *Service) ReadBackupAdvisor(ctx context.Context, owner, workspace string) (AdvisorFacts, error) {
	read, err := s.ReadBackupSummaries(ctx, owner, []string{workspace})
	if err != nil {
		return AdvisorFacts{}, err
	}
	if len(read.Failures) > 0 || !read.Complete {
		return AdvisorFacts{}, ErrUnavailable
	}
	if len(read.Sources) != 1 {
		return AdvisorFacts{}, ErrWorkspaceUnavailable
	}
	facts := read.Sources[0]
	return AdvisorFacts{Provider: facts.Provider, SourceCoverage: facts.SourceCoverage, Connection: facts.Connection, Destination: facts.Destination, Monitoring: facts.Monitoring, ReaderAvailable: facts.ReaderAvailable, LastSuccessfulRead: facts.LastSuccessfulRead, LastCompletedAt: facts.LastCompletedAt, SnapshotCount: facts.SnapshotCount, LastAttemptOutcome: facts.LastAttemptOutcome, Route: facts.Route, AsOf: facts.AsOf, EvidenceCurrent: facts.EvidenceCurrent, Health: facts.Health, RecoveryState: facts.RecoveryState, RecoveryTestedAt: facts.RecoveryTestedAt}, nil
}
