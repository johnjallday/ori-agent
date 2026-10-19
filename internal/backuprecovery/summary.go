package backuprecovery

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

const maxSummaryWorkspaces = 32

// SummaryFacts is the only owning-domain payload intended for HQ/Brief. It has
// no source/repository paths, external labels, snapshot/storage IDs, credential
// references, tokens, notes or executable action. Titles are compiled by callers.
type SummaryFacts struct {
	WorkspaceID        string         `json:"workspace_id"`
	Provider           string         `json:"provider,omitempty"` // fixed id, never a provider label
	Generation         string         `json:"generation,omitempty"`
	Sequence           uint64         `json:"sequence"`
	Route              string         `json:"route"`
	AsOf               time.Time      `json:"as_of"`
	Connection         string         `json:"connection"`
	Destination        string         `json:"destination"`
	Monitoring         string         `json:"monitoring"`
	ReaderAvailable    bool           `json:"reader_available"`
	EvidenceCurrent    bool           `json:"evidence_current_generation"`
	Health             EvidenceHealth `json:"health"`
	LastSuccessfulRead *time.Time     `json:"last_successful_read,omitempty"`
	LastCompletedAt    *time.Time     `json:"last_completed_at,omitempty"`
	SnapshotCount      *int           `json:"snapshot_count,omitempty"`
	LastAttemptOutcome string         `json:"last_attempt_outcome,omitempty"`
	RecoveryState      string         `json:"recovery_state"`
	RecoveryTestedAt   *time.Time     `json:"recovery_tested_at,omitempty"`
	// SourceCoverage is each configured source's stored Time Machine
	// inclusion (included / source_excluded / unknown), in order, never a path.
	SourceCoverage []string `json:"source_coverage,omitempty"`
}
type SummaryFailure struct {
	WorkspaceID string `json:"workspace_id"`
	Code        string `json:"code"`
}
type SummaryRead struct {
	Sources   []SummaryFacts   `json:"sources"`
	Failures  []SummaryFailure `json:"failures"`
	Complete  bool             `json:"complete"`
	Truncated bool             `json:"truncated"`
}

func summaryFromRecord(s *Service, record localRecord) SummaryFacts {
	status := s.storedStatus(record)
	facts := SummaryFacts{WorkspaceID: record.authority.Workspace, Provider: status.Provider, Generation: status.Generation, Sequence: status.Sequence, AsOf: status.ServerTime, Connection: status.Connection, Destination: status.Destination, Monitoring: status.Monitoring, ReaderAvailable: status.RuntimeAvailable, EvidenceCurrent: status.EvidenceCurrent, Health: status.Health, LastSuccessfulRead: status.LastSuccessfulRead, LastCompletedAt: status.LastCompletedAt, SnapshotCount: status.SnapshotCount, RecoveryState: "not_tested"}
	// Future dates remain unavailable to every summary consumer, not just the
	// owning console. Historical read times retain their evidence-current label.
	if facts.LastSuccessfulRead != nil && facts.LastSuccessfulRead.After(status.ServerTime.Add(5*time.Minute)) {
		facts.LastSuccessfulRead = nil
	}
	if status.Health.Recency == "clock_suspect" {
		facts.LastCompletedAt = nil
	}
	if status.LastAttempt != nil && status.LastAttempt.Generation == status.Generation {
		facts.LastAttemptOutcome = status.LastAttempt.Outcome
	}
	if status.EvidenceCurrent && record.state != nil && record.state.Observation != nil && record.state.Observation.TimeMachine != nil {
		facts.SourceCoverage = append([]string(nil), record.state.Observation.TimeMachine.Sources...)
	}
	if record.state != nil {
		superseded := map[string]bool{}
		for _, report := range record.state.Reports {
			if report.Supersedes != "" {
				superseded[report.Supersedes] = true
			}
		}
		for _, report := range record.state.Reports {
			if report.Generation != status.Generation || superseded[report.ID] {
				continue
			}
			if !status.EvidenceCurrent || report.TestedAt.After(status.ServerTime.Add(5*time.Minute)) || report.ReportedAt.After(status.ServerTime.Add(5*time.Minute)) {
				facts.RecoveryState = "review_required"
				break
			}
			facts.RecoveryState = report.Result
			if !report.TestedAt.After(status.ServerTime.Add(5 * time.Minute)) {
				at := report.TestedAt
				facts.RecoveryTestedAt = &at
			}
			break
		}
	}
	return facts
}

func backupSummaryInstall(raw string) (bool, error) {
	value, err := decodeMetadata([]byte(raw), nil, 0, maxSettingsBytes)
	if err != nil {
		return false, ErrUnavailable
	}
	rows, ok := value.([]any)
	if !ok || len(rows) > 128 {
		return false, ErrUnavailable
	}
	for _, value := range rows {
		row, ok := value.(map[string]any)
		if !ok {
			return false, ErrUnavailable
		}
		id, ok := row["id"].(string)
		if !ok {
			return false, ErrUnavailable
		}
		if strings.ToLower(strings.TrimSpace(id)) == "backup-recovery" {
			return true, nil
		}
	}
	return false, nil
}

// scopeIdentities validates, deduplicates and orders caller-supplied scope
// candidates. It never adds an identity the caller did not name.
func scopeIdentities(workspaceIDs []string) ([]string, error) {
	if len(workspaceIDs) > 1024 {
		return nil, ErrInvalidRequest
	}
	seen := map[string]bool{}
	ids := []string{}
	for _, id := range workspaceIDs {
		if !localID(id) {
			return nil, ErrInvalidRequest
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// scopedBackupRecord is one installed Backup & Recovery workspace that passed
// the exact live SQL ownership/activity/installation check. Failure is a fixed
// code when its capability row or stored state could not be read.
type scopedBackupRecord struct {
	id, slug string
	record   localRecord
	failure  string
}

// scanScopedBackupRecords visits, within tx, every candidate that is an active
// local workspace with Backup & Recovery installed, in identity order, stopping
// after limit visits. It reports truncation; it never enumerates beyond ids.
func (s *Service) scanScopedBackupRecords(ctx context.Context, tx *sql.Tx, owner string, ids []string, limit int, visit func(scopedBackupRecord)) (bool, error) {
	visited := 0
	for _, id := range ids {
		var installs, slug string
		err := tx.QueryRowContext(ctx, `SELECT CASE WHEN typeof(folder_slug)='text' AND length(CAST(folder_slug AS BLOB))<=80 THEN folder_slug ELSE '' END,CASE WHEN length(CAST(installed_capabilities_json AS BLOB))<=131072 THEN installed_capabilities_json ELSE '' END FROM workspaces WHERE id=? AND owner_user_id=? AND owner_user_id='local' AND kind='workspace' AND status='active' AND deleted_at IS NULL`, id, owner).Scan(&slug, &installs)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, err
		}
		if !workspace.IsCanonicalWorkspaceSlug(slug) {
			slug = ""
		}
		installed, err := backupSummaryInstall(installs)
		if err == nil && !installed {
			continue
		}
		if visited >= limit {
			return true, nil
		}
		visited++
		if err != nil {
			visit(scopedBackupRecord{id: id, slug: slug, failure: "capability_state_unavailable"})
			continue
		}
		record, err := s.store.read(ctx, tx, owner, id)
		if err != nil {
			visit(scopedBackupRecord{id: id, slug: slug, failure: "stored_state_unavailable"})
			continue
		}
		visit(scopedBackupRecord{id: id, slug: slug, record: record})
	}
	return false, nil
}

// ReadBackupSummaries accepts only candidate identities from the caller's
// canonical selected/all-workspace scope. It cannot enumerate or widen that
// scope. Every identity is rechecked against exact live SQL ownership, activity,
// installation and local authority in a single consistent read transaction.
func (s *Service) ReadBackupSummaries(ctx context.Context, owner string, workspaceIDs []string) (SummaryRead, error) {
	result := SummaryRead{Sources: []SummaryFacts{}, Failures: []SummaryFailure{}, Complete: true}
	if owner != "local" {
		return result, ErrWorkspaceUnavailable
	}
	release, err := s.enter()
	if err != nil {
		return result, err
	}
	defer release()
	ids, err := scopeIdentities(workspaceIDs)
	if err != nil {
		return result, err
	}
	err = s.store.transaction(ctx, func(tx *sql.Tx) error {
		truncated, err := s.scanScopedBackupRecords(ctx, tx, owner, ids, maxSummaryWorkspaces, func(row scopedBackupRecord) {
			if row.failure != "" {
				result.Complete = false
				result.Failures = append(result.Failures, SummaryFailure{WorkspaceID: row.id, Code: row.failure})
				return
			}
			facts := summaryFromRecord(s, row.record)
			if row.slug != "" {
				facts.Route = backupConsoleRoute(row.slug)
			}
			result.Sources = append(result.Sources, facts)
		})
		if truncated {
			result.Truncated = true
			result.Complete = false
		}
		return err
	})
	if err != nil {
		return SummaryRead{Sources: []SummaryFacts{}, Failures: []SummaryFailure{}, Complete: false}, err
	}
	return result, nil
}

func backupConsoleRoute(slug string) string {
	return "/workspaces/" + url.PathEscape(slug) + "?panel=backup-recovery&tab=overview"
}
