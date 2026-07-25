package downloadsjanitor

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// This file joins the pieces into the two scan entry points the user has:
//
//   • TestScan — the harmless setup check. It reports what a real scan would
//     consider and creates nothing (FR-23).
//   • ScanNow  — a real scan that persists one reviewable batch (FR-42).
//
// Both run the identical eligibility, settling, classification, fingerprint,
// and deduplication path. That is the point of having one Scanner: a file the
// test scan calls ineligible cannot become actionable through a different
// entry point, and later automatic scans (group 5) join here too.

// CandidatePreview is one line of a test scan's report. It is deliberately not
// a candidate: it has no ID, is not persisted, and nothing can be approved from
// it.
type CandidatePreview struct {
	Name       string         `json:"name"`
	Extension  string         `json:"extension,omitempty"`
	MIMEType   string         `json:"mime_type,omitempty"`
	Size       int64          `json:"size"`
	ModifiedAt time.Time      `json:"modified_at"`
	Category   Category       `json:"category"`
	Reason     string         `json:"reason,omitempty"`
	Confidence ConfidenceBand `json:"confidence,omitempty"`
	// NeedsReview marks a file the deterministic pass could not place.
	NeedsReview bool `json:"needs_review,omitempty"`
}

// ScanReport is a test scan's outcome: what would be proposed, and what would
// be passed over and why.
type ScanReport struct {
	Eligible   []CandidatePreview      `json:"eligible"`
	Ineligible []IneligibleObservation `json:"ineligible,omitempty"`
	ScannedAt  time.Time               `json:"scanned_at"`
	// Counts mirror the batch summary so the setup card and the review surface
	// describe a scan the same way.
	EligibleCount    int `json:"eligible_count"`
	IneligibleCount  int `json:"ineligible_count"`
	NeedsReviewCount int `json:"needs_review_count"`
}

// SetScanner overrides the service's scanner. Tests use it to pin the clock;
// production wiring leaves the default in place.
func (s *Service) SetScanner(scanner *Scanner) {
	if s != nil && scanner != nil {
		s.scanner = scanner
	}
}

// scannerFor returns the service's scanner, building the default one lazily so
// a Service constructed without an explicit scanner still works.
func (s *Service) scannerFor() *Scanner {
	if s.scanner == nil {
		s.scanner = NewScanner(s.store, s.workspaces)
	}
	return s.scanner
}

// requireConfigured loads settings and refuses to scan a workspace whose setup
// is incomplete. Scanning is one of the things setup gates (FR-6).
func (s *Service) requireConfigured(workspaceID string) (JanitorSettings, error) {
	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil {
		return JanitorSettings{}, err
	}
	if !settings.IsSetUp() {
		return JanitorSettings{}, setupErr(CodeNotConfigured, "Choose a folder for this workspace before scanning.", RepairChooseFolder, nil)
	}
	return settings, nil
}

// TestScan reports what a scan would find without creating anything.
//
// It writes nothing at all — not even a settling observation. A check the user
// runs to see whether setup worked must not quietly change what a later real
// scan would do.
func (s *Service) TestScan(workspaceID string) (ScanReport, error) {
	settings, err := s.requireConfigured(workspaceID)
	if err != nil {
		return ScanReport{}, err
	}
	state, err := s.store.LoadScanState(workspaceID)
	if err != nil {
		return ScanReport{}, err
	}
	result, err := s.scannerFor().Scan(settings, state, ScanSourceTest)
	if err != nil {
		return ScanReport{}, err
	}

	report := ScanReport{
		ScannedAt:       result.ScannedAt,
		Ineligible:      result.Ineligible,
		IneligibleCount: len(result.Ineligible),
	}
	for _, candidate := range result.Eligible {
		classification := ClassifyMetadata(candidate)
		classification.Apply(&candidate)
		if candidate.NeedsReview {
			report.NeedsReviewCount++
		}
		report.Eligible = append(report.Eligible, CandidatePreview{
			Name:        candidate.Name,
			Extension:   candidate.Extension,
			MIMEType:    candidate.MIMEType,
			Size:        candidate.Size,
			ModifiedAt:  candidate.ModifiedAt,
			Category:    candidate.Category,
			Reason:      candidate.Reason,
			Confidence:  candidate.Confidence,
			NeedsReview: candidate.NeedsReview,
		})
	}
	report.EligibleCount = len(report.Eligible)
	return report, nil
}

// ScanNow runs a real scan and persists its result as one reviewable batch.
//
// A scan that finds nothing new still records nothing: an empty batch would be
// noise in history and, later, a notification about nothing (FR-105). The
// returned bool reports whether a batch was created.
func (s *Service) ScanNow(workspaceID string, source ScanSource) (JanitorBatch, bool, error) {
	if source == ScanSourceTest {
		return JanitorBatch{}, false, fmt.Errorf("%w: use TestScan for a test scan", ErrInvalidSettings)
	}
	settings, err := s.requireConfigured(workspaceID)
	if err != nil {
		return JanitorBatch{}, false, err
	}

	scanner := s.scannerFor()
	// Observing and scanning happen inside one state update so a file cannot be
	// observed by this run and then judged against a state another run wrote in
	// between.
	var created JanitorBatch
	var persisted bool
	_, err = s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
		// Judge first, then record. The scan must see the state this run
		// started with: recording a sighting first would make every file
		// "tracked as of now", which would disqualify a pre-existing backlog
		// from the never-observed-and-already-old path in settled().
		result, scanErr := scanner.Scan(settings, *state, source)
		if scanErr != nil {
			return scanErr
		}
		// Now record what is on disk, so this run contributes the evidence the
		// next run reads for anything that was still changing.
		if err := scanner.ObserveForSettling(settings, state); err != nil {
			return err
		}
		if len(result.Eligible) == 0 {
			// Still worth remembering what was passed over — but there is
			// nothing for the user to review, so no batch is created.
			return nil
		}

		batch := JanitorBatch{
			ID:          "batch-" + uuid.New().String(),
			WorkspaceID: workspaceID,
			Source:      source,
			StartedAt:   result.ScannedAt,
			CompletedAt: result.ScannedAt,
			Ineligible:  result.Ineligible,
		}
		candidates := make([]JanitorCandidate, 0, len(result.Eligible))
		for _, candidate := range result.Eligible {
			candidate.ID = "cand-" + uuid.New().String()
			candidate.WorkspaceID = workspaceID
			candidate.BatchID = batch.ID
			candidate.State = CandidatePending
			// Classification proposes; it never decides. Decision stays empty
			// until the user acts.
			ClassifyMetadata(candidate).Apply(&candidate)
			candidates = append(candidates, candidate)
			batch.CandidateIDs = append(batch.CandidateIDs, candidate.ID)
		}
		batch = SummarizeBatch(batch, candidates)

		state.Candidates = append(state.Candidates, candidates...)
		state.Batches = append(state.Batches, batch)
		created = batch
		persisted = true
		return nil
	})
	if err != nil {
		return JanitorBatch{}, false, err
	}
	return created, persisted, nil
}

// ListBatches returns the workspace's batches, newest first.
func (s *Service) ListBatches(workspaceID string) ([]JanitorBatch, error) {
	state, err := s.store.LoadScanState(workspaceID)
	if err != nil {
		return nil, err
	}
	out := make([]JanitorBatch, 0, len(state.Batches))
	for i := len(state.Batches) - 1; i >= 0; i-- {
		out = append(out, state.Batches[i])
	}
	return out, nil
}

// LatestPendingBatch returns the newest batch that still has candidates
// awaiting the user, which is what the review surface opens on.
func (s *Service) LatestPendingBatch(workspaceID string) (JanitorBatch, []JanitorCandidate, bool, error) {
	state, err := s.store.LoadScanState(workspaceID)
	if err != nil {
		return JanitorBatch{}, nil, false, err
	}
	for i := len(state.Batches) - 1; i >= 0; i-- {
		batch := state.Batches[i]
		if batch.State == BatchPending || batch.State == BatchPartiallyApplied {
			return batch, state.CandidatesFor(batch.ID), true, nil
		}
	}
	return JanitorBatch{}, nil, false, nil
}

// LatestPendingBatchCandidates returns the candidates of the newest batch still
// awaiting the user.
func (s *Service) LatestPendingBatchCandidates(workspaceID string) (JanitorBatch, []JanitorCandidate, error) {
	batch, candidates, _, err := s.LatestPendingBatch(workspaceID)
	return batch, candidates, err
}

// BatchDetail returns one batch and its candidates.
func (s *Service) BatchDetail(workspaceID, batchID string) (JanitorBatch, []JanitorCandidate, error) {
	state, err := s.store.LoadScanState(workspaceID)
	if err != nil {
		return JanitorBatch{}, nil, err
	}
	batch, ok := state.Batch(strings.TrimSpace(batchID))
	if !ok {
		return JanitorBatch{}, nil, fmt.Errorf("%w: %s", ErrBatchNotFound, batchID)
	}
	return batch, state.CandidatesFor(batch.ID), nil
}

// ErrBatchNotFound reports a batch ID that is not in this workspace — including
// one belonging to another workspace, which is indistinguishable from unknown.
var ErrBatchNotFound = fmt.Errorf("downloads janitor batch not found")

// DecisionUpdate is one user decision submitted for review. It carries IDs and
// an allowlisted category — never a path (FR-71).
type DecisionUpdate struct {
	CandidateID string
	// Decision is "move", "skip", or "" to clear a decision. Trash is added
	// with its own confirmation path in a later group.
	Decision Decision
	// Category applies to a move decision; empty keeps the proposed category.
	Category string
}

// ApplyDecisions records the user's review decisions. It changes no file: it
// records intent, which a separate, explicitly approved apply step acts on.
func (s *Service) ApplyDecisions(workspaceID string, updates []DecisionUpdate) ([]JanitorCandidate, error) {
	if len(updates) == 0 {
		return nil, nil
	}
	var changed []JanitorCandidate
	_, err := s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
		touchedBatches := map[string]struct{}{}
		for _, update := range updates {
			index := -1
			for i := range state.Candidates {
				if state.Candidates[i].ID == strings.TrimSpace(update.CandidateID) {
					index = i
					break
				}
			}
			if index < 0 {
				return fmt.Errorf("%w: %s", ErrCandidateNotFound, update.CandidateID)
			}
			candidate := &state.Candidates[index]
			if !candidate.Actionable() {
				return fmt.Errorf("%w: %s is %s and cannot be changed", ErrCandidateNotFound, candidate.ID, candidate.State)
			}

			switch update.Decision {
			case DecisionNone:
				candidate.Decision = DecisionNone
				candidate.DecisionCategory = ""
				candidate.DecidedAt = time.Time{}
				candidate.State = CandidatePending
			case DecisionMove:
				category := candidate.Category
				if requested := strings.TrimSpace(update.Category); requested != "" {
					definition, err := LookupCategory(requested)
					if err != nil {
						return err
					}
					category = definition.ID
				}
				if !ValidCategory(category) {
					return fmt.Errorf("%w: %q", ErrUnknownCategory, category)
				}
				candidate.Decision = DecisionMove
				candidate.DecisionCategory = category
				candidate.DecidedAt = s.clock()
				candidate.State = CandidatePending
			case DecisionSkip:
				candidate.Decision = DecisionSkip
				candidate.DecisionCategory = ""
				candidate.DecidedAt = s.clock()
				candidate.State = CandidateSkipped
				// Remember the exact file state so the same unchanged file is
				// not proposed again on the next scan.
				MarkSkipped(state, candidate.Fingerprint, s.clock())
			default:
				return fmt.Errorf("%w: unsupported decision %q", ErrInvalidCandidate, update.Decision)
			}
			changed = append(changed, *candidate)
			touchedBatches[candidate.BatchID] = struct{}{}
		}
		for batchID := range touchedBatches {
			resummarizeBatch(state, batchID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return changed, nil
}

// ResetSkipped forgets skip decisions so the files can be proposed again. An
// empty key resets every skipped item.
func (s *Service) ResetSkipped(workspaceID, key string) error {
	_, err := s.store.UpdateScanState(workspaceID, func(state *ScanState) error {
		ClearSkipped(state, key)
		return nil
	})
	return err
}
