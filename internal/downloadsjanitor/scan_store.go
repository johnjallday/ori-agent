package downloadsjanitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// scanStateFileName is the scan record inside a workspace's Janitor state
// directory. Batches, their candidates, settling observations, and skipped
// fingerprints share one file so a single atomic write always leaves them
// consistent with each other: a batch can never reference a candidate that a
// crash lost, and a summary can never disagree with the candidates it counts.
const scanStateFileName = "scan-state.json"

// ScanStateSchemaVersion is the on-disk revision of the scan record.
const ScanStateSchemaVersion = 1

// Retention bounds. Operational metadata accumulates on every scan, so it is
// capped rather than kept forever — but pruning never discards work the user
// still has to act on (see pruneScanState).
const (
	// MaxRetainedBatches caps stored batches. Batches with pending decisions
	// are never counted out by this limit.
	MaxRetainedBatches = 25
	// MaxRetainedObservations caps settling observations, which exist only to
	// answer "has this file stopped changing?".
	MaxRetainedObservations = 2000
	// MaxRetainedSkips caps remembered skip decisions.
	MaxRetainedSkips = 2000
	// MaxRetainedActions caps the journal. History is what the user undoes
	// from, so the bound is generous and the newest entries win.
	MaxRetainedActions = 1000
	// ObservationTTL bounds how long an unmatched settling observation is kept.
	// A file that stopped appearing does not need its observation retained.
	ObservationTTL = 7 * 24 * time.Hour
	// ResolvedBatchTTL bounds how long a fully resolved batch is retained for
	// history display.
	ResolvedBatchTTL = 90 * 24 * time.Hour
)

// SettledObservation is one sighting of a file, used to decide whether it has
// stopped changing. It holds no content — only the size and timestamp needed to
// compare two sightings (FR-32).
type SettledObservation struct {
	// Name is the top-level filename observed.
	Name string `json:"name"`
	// Size and ModTime as seen at ObservedAt.
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	// FirstSeenAt is when this unchanged size/mod-time pair was first observed;
	// ObservedAt is the most recent sighting. A file is settled once these are
	// far enough apart with no change in between.
	FirstSeenAt time.Time `json:"first_seen_at"`
	ObservedAt  time.Time `json:"observed_at"`
	// ChangeWitnessed records that Ori has actually seen this file change since
	// it started tracking it — the sighting was replaced by a different size or
	// modification time at least once.
	//
	// It is what separates "a file that was already sitting here" from "a file
	// I watched being written". The first can be trusted to its own timestamp;
	// the second must prove it stopped changing across Ori's own observations,
	// because a writer that stalls mid-file also leaves an ageing timestamp.
	ChangeWitnessed bool `json:"change_witnessed,omitempty"`
}

// Unchanged reports whether a new sighting matches this observation's size and
// modification time.
func (o SettledObservation) Unchanged(size int64, modTime time.Time) bool {
	return o.Size == size && o.ModTime.Equal(modTime)
}

// SkippedFingerprint remembers that the user dismissed one exact file state, so
// the same unchanged file is not proposed again on the next scan. It is keyed
// by fingerprint, so a file that changes becomes a fresh candidate (FR-41).
type SkippedFingerprint struct {
	// Key is Fingerprint.Key() — a hash, so a filename is not used as a map key
	// that later appears in logs.
	Key string `json:"key"`
	// Name is retained for the "skipped items" list the user can reset.
	Name      string    `json:"name"`
	SkippedAt time.Time `json:"skipped_at"`
}

// ScanState is everything one workspace's scans have produced.
type ScanState struct {
	SchemaVersion int    `json:"schema_version"`
	WorkspaceID   string `json:"workspace_id"`
	// Batches are ordered oldest to newest.
	Batches []JanitorBatch `json:"batches,omitempty"`
	// Candidates holds every batch's candidates, keyed by ID through the
	// batches' CandidateIDs.
	Candidates []JanitorCandidate `json:"candidates,omitempty"`
	// Observations back the settling check.
	Observations []SettledObservation `json:"observations,omitempty"`
	// Skipped remembers dismissed file states.
	Skipped []SkippedFingerprint `json:"skipped,omitempty"`
	// Approvals are issued, not-yet-consumed approval tokens. They live in the
	// same record as candidates so consuming one and mutating the candidate it
	// authorizes happen in a single atomic write — a token cannot be spent
	// without the state change it paid for, and vice versa.
	Approvals []ApprovalRecord `json:"approvals,omitempty"`
	// Actions is the durable journal of every attempted mutation.
	Actions   []FileAction `json:"actions,omitempty"`
	UpdatedAt time.Time    `json:"updated_at,omitempty"`
}

// Candidate returns the candidate with the given ID.
func (s ScanState) Candidate(id string) (JanitorCandidate, bool) {
	for _, candidate := range s.Candidates {
		if candidate.ID == id {
			return candidate, true
		}
	}
	return JanitorCandidate{}, false
}

// Action returns the journal entry with the given ID.
func (s ScanState) Action(id string) (FileAction, bool) {
	for _, action := range s.Actions {
		if action.ID == id {
			return action, true
		}
	}
	return FileAction{}, false
}

// ActionByIdempotencyKey finds a previously recorded action for an apply, which
// is how a retried confirm is recognized instead of re-executed.
func (s ScanState) ActionByIdempotencyKey(key string) (FileAction, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return FileAction{}, false
	}
	for _, action := range s.Actions {
		if action.IdempotencyKey == key {
			return action, true
		}
	}
	return FileAction{}, false
}

// Batch returns the batch with the given ID.
func (s ScanState) Batch(id string) (JanitorBatch, bool) {
	for _, batch := range s.Batches {
		if batch.ID == id {
			return batch, true
		}
	}
	return JanitorBatch{}, false
}

// CandidatesFor returns a batch's candidates in batch order.
func (s ScanState) CandidatesFor(batchID string) []JanitorCandidate {
	batch, ok := s.Batch(batchID)
	if !ok {
		return nil
	}
	byID := make(map[string]JanitorCandidate, len(s.Candidates))
	for _, candidate := range s.Candidates {
		byID[candidate.ID] = candidate
	}
	out := make([]JanitorCandidate, 0, len(batch.CandidateIDs))
	for _, id := range batch.CandidateIDs {
		if candidate, exists := byID[id]; exists {
			out = append(out, candidate)
		}
	}
	return out
}

// PendingCandidates returns every candidate still awaiting a decision, across
// batches. These are what must survive a restart intact.
func (s ScanState) PendingCandidates() []JanitorCandidate {
	var out []JanitorCandidate
	for _, candidate := range s.Candidates {
		if candidate.State == CandidatePending || candidate.State == CandidateApproved {
			out = append(out, candidate)
		}
	}
	return out
}

// IsSkipped reports whether this exact file state was dismissed by the user.
func (s ScanState) IsSkipped(fingerprint Fingerprint) bool {
	key := fingerprint.Key()
	if key == "" {
		return false
	}
	for _, skip := range s.Skipped {
		if skip.Key == key {
			return true
		}
	}
	return false
}

// Observation returns the stored sighting for a filename.
func (s ScanState) Observation(name string) (SettledObservation, bool) {
	for _, observation := range s.Observations {
		if observation.Name == name {
			return observation, true
		}
	}
	return SettledObservation{}, false
}

// ActiveFingerprints returns the fingerprints of candidates that are still live
// (pending, approved, or awaiting retry), so a rescan does not re-propose a file
// the user is already looking at (FR-40).
func (s ScanState) ActiveFingerprints() map[string]struct{} {
	out := map[string]struct{}{}
	for _, candidate := range s.Candidates {
		if candidate.State.Terminal() || candidate.State == CandidateStale {
			continue
		}
		if key := candidate.Fingerprint.Key(); key != "" {
			out[key] = struct{}{}
		}
	}
	return out
}

// --------------------------------------------------------------------- store

func (s *Store) scanStatePath(workspaceID string) (string, error) {
	dir, err := s.StateDir(workspaceID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, scanStateFileName), nil
}

// LoadScanState returns the workspace's scan record. A workspace that has never
// scanned — or whose record is missing or unreadable — loads as empty state, so
// a lost file costs the user a rescan rather than stranding the workspace.
//
// A record belonging to a different workspace is an error, never served.
func (s *Store) LoadScanState(workspaceID string) (ScanState, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	path, err := s.scanStatePath(workspaceID)
	if err != nil {
		return ScanState{}, err
	}
	// path is a resolved workspace folder joined with two package constants.
	data, err := os.ReadFile(path) // #nosec G304 -- resolved workspace folder + fixed constants
	if err != nil {
		if os.IsNotExist(err) {
			return newScanState(workspaceID), nil
		}
		return ScanState{}, fmt.Errorf("failed to read downloads janitor scan state: %w", err)
	}
	var stored ScanState
	if err := json.Unmarshal(data, &stored); err != nil {
		return newScanState(workspaceID), nil
	}
	if id := strings.TrimSpace(stored.WorkspaceID); id != "" && id != workspaceID {
		return ScanState{}, fmt.Errorf("%w: scan state is for %s", ErrWorkspaceMismatch, id)
	}
	stored.WorkspaceID = workspaceID
	stored.SchemaVersion = ScanStateSchemaVersion
	return stored, nil
}

func newScanState(workspaceID string) ScanState {
	return ScanState{SchemaVersion: ScanStateSchemaVersion, WorkspaceID: workspaceID}
}

// UpdateScanState applies mutate to the workspace's scan record and persists the
// result atomically, holding the workspace's lock across the whole
// read-modify-write. A mutate that returns an error leaves disk untouched.
//
// Every write is validated and pruned first, so a caller cannot persist a
// malformed candidate or grow the record without bound.
func (s *Store) UpdateScanState(workspaceID string, mutate func(*ScanState) error) (ScanState, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return ScanState{}, fmt.Errorf("%w: workspace id is required", ErrInvalidSettings)
	}
	if mutate == nil {
		return s.LoadScanState(workspaceID)
	}
	lock := s.lockFor(workspaceID)
	lock.Lock()
	defer lock.Unlock()

	current, err := s.LoadScanState(workspaceID)
	if err != nil {
		return ScanState{}, err
	}
	next := current
	if err := mutate(&next); err != nil {
		return ScanState{}, err
	}
	next.WorkspaceID = workspaceID
	next.SchemaVersion = ScanStateSchemaVersion
	if err := validateScanState(next); err != nil {
		return ScanState{}, err
	}
	next = pruneScanState(next, time.Now())
	next.UpdatedAt = time.Now()

	path, err := s.scanStatePath(workspaceID)
	if err != nil {
		return ScanState{}, err
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return ScanState{}, fmt.Errorf("failed to encode downloads janitor scan state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return ScanState{}, fmt.Errorf("failed to create downloads janitor state directory: %w", err)
	}
	if err := atomicWriteFile(path, append(data, '\n')); err != nil {
		return ScanState{}, err
	}
	return next, nil
}

// validateScanState rejects a record that could not be acted on safely: an
// invalid candidate, a candidate belonging to another workspace, or a batch
// referencing a candidate that is not stored.
func validateScanState(state ScanState) error {
	known := make(map[string]struct{}, len(state.Candidates))
	for _, candidate := range state.Candidates {
		if err := candidate.Validate(); err != nil {
			return err
		}
		if candidate.WorkspaceID != state.WorkspaceID {
			return fmt.Errorf("%w: candidate %s belongs to workspace %s", ErrInvalidCandidate, candidate.ID, candidate.WorkspaceID)
		}
		if _, dup := known[candidate.ID]; dup {
			return fmt.Errorf("%w: duplicate candidate id %s", ErrInvalidCandidate, candidate.ID)
		}
		known[candidate.ID] = struct{}{}
	}
	for _, batch := range state.Batches {
		if strings.TrimSpace(batch.ID) == "" {
			return fmt.Errorf("%w: batch id is required", ErrInvalidCandidate)
		}
		if batch.WorkspaceID != state.WorkspaceID {
			return fmt.Errorf("%w: batch %s belongs to workspace %s", ErrInvalidCandidate, batch.ID, batch.WorkspaceID)
		}
		for _, id := range batch.CandidateIDs {
			if _, exists := known[id]; !exists {
				return fmt.Errorf("%w: batch %s references unknown candidate %s", ErrInvalidCandidate, batch.ID, id)
			}
		}
	}
	return nil
}

// pruneScanState bounds retained operational metadata.
//
// The rule that matters: a batch is only ever dropped when it has nothing left
// for the user to act on. Pending and approved decisions are work the user has
// already done or is mid-way through, so retention limits never reclaim them —
// they bite on resolved history instead.
func pruneScanState(state ScanState, now time.Time) ScanState {
	keep := make([]JanitorBatch, 0, len(state.Batches))
	var resolved []JanitorBatch
	for _, batch := range state.Batches {
		if batchHasPendingWork(state, batch) {
			keep = append(keep, batch)
			continue
		}
		if !batch.CompletedAt.IsZero() && now.Sub(batch.CompletedAt) > ResolvedBatchTTL {
			continue
		}
		resolved = append(resolved, batch)
	}
	// Drop the oldest resolved batches first when over the cap.
	sort.SliceStable(resolved, func(i, j int) bool {
		return batchTime(resolved[i]).Before(batchTime(resolved[j]))
	})
	if allowed := MaxRetainedBatches - len(keep); allowed >= 0 && len(resolved) > allowed {
		resolved = resolved[len(resolved)-allowed:]
	}
	merged := append(keep, resolved...)
	sort.SliceStable(merged, func(i, j int) bool {
		return batchTime(merged[i]).Before(batchTime(merged[j]))
	})
	state.Batches = merged

	// Candidates follow their batch: anything no longer referenced goes.
	referenced := map[string]struct{}{}
	for _, batch := range state.Batches {
		for _, id := range batch.CandidateIDs {
			referenced[id] = struct{}{}
		}
	}
	candidates := make([]JanitorCandidate, 0, len(state.Candidates))
	for _, candidate := range state.Candidates {
		if _, ok := referenced[candidate.ID]; ok {
			candidates = append(candidates, candidate)
		}
	}
	state.Candidates = candidates

	// Observations are pure scratch: age them out, then cap newest-first.
	observations := make([]SettledObservation, 0, len(state.Observations))
	for _, observation := range state.Observations {
		if now.Sub(observation.ObservedAt) > ObservationTTL {
			continue
		}
		observations = append(observations, observation)
	}
	sort.SliceStable(observations, func(i, j int) bool {
		return observations[i].ObservedAt.Before(observations[j].ObservedAt)
	})
	if len(observations) > MaxRetainedObservations {
		observations = observations[len(observations)-MaxRetainedObservations:]
	}
	state.Observations = observations

	state.Approvals = pruneApprovals(state.Approvals, now)

	// Actions are the accountability record: they are capped, never aged out,
	// and the newest are kept.
	if len(state.Actions) > MaxRetainedActions {
		state.Actions = state.Actions[len(state.Actions)-MaxRetainedActions:]
	}

	// Skips are user decisions, so they are only ever dropped by the cap —
	// oldest first — never by age.
	skipped := append([]SkippedFingerprint(nil), state.Skipped...)
	sort.SliceStable(skipped, func(i, j int) bool {
		return skipped[i].SkippedAt.Before(skipped[j].SkippedAt)
	})
	if len(skipped) > MaxRetainedSkips {
		skipped = skipped[len(skipped)-MaxRetainedSkips:]
	}
	state.Skipped = skipped

	return state
}

func batchTime(batch JanitorBatch) time.Time {
	if !batch.CompletedAt.IsZero() {
		return batch.CompletedAt
	}
	return batch.StartedAt
}

// batchHasPendingWork reports whether any of a batch's candidates still awaits
// the user.
func batchHasPendingWork(state ScanState, batch JanitorBatch) bool {
	for _, candidate := range state.CandidatesFor(batch.ID) {
		switch candidate.State {
		case CandidatePending, CandidateApproved, CandidateApplying, CandidateFailed:
			return true
		}
	}
	return false
}

// ------------------------------------------------------------------ mutations

// AppendBatch stores a scan's batch and its candidates in one atomic write and
// returns the stored batch with its summary recomputed.
func (s *Store) AppendBatch(workspaceID string, batch JanitorBatch, candidates []JanitorCandidate) (JanitorBatch, error) {
	var stored JanitorBatch
	_, err := s.UpdateScanState(workspaceID, func(state *ScanState) error {
		batch.WorkspaceID = workspaceID
		batch.CandidateIDs = batch.CandidateIDs[:0:0]
		for i := range candidates {
			candidates[i].WorkspaceID = workspaceID
			candidates[i].BatchID = batch.ID
			if candidates[i].State == "" {
				candidates[i].State = CandidatePending
			}
			batch.CandidateIDs = append(batch.CandidateIDs, candidates[i].ID)
			state.Candidates = append(state.Candidates, candidates[i])
		}
		batch = SummarizeBatch(batch, candidates)
		state.Batches = append(state.Batches, batch)
		stored = batch
		return nil
	})
	if err != nil {
		return JanitorBatch{}, err
	}
	return stored, nil
}

// UpdateCandidate applies mutate to one candidate and re-summarizes its batch,
// so a decision and the counts shown above it are always written together.
func (s *Store) UpdateCandidate(workspaceID, candidateID string, mutate func(*JanitorCandidate) error) (JanitorCandidate, error) {
	var updated JanitorCandidate
	_, err := s.UpdateScanState(workspaceID, func(state *ScanState) error {
		index := -1
		for i := range state.Candidates {
			if state.Candidates[i].ID == candidateID {
				index = i
				break
			}
		}
		if index < 0 {
			return fmt.Errorf("%w: %s", ErrCandidateNotFound, candidateID)
		}
		if err := mutate(&state.Candidates[index]); err != nil {
			return err
		}
		updated = state.Candidates[index]
		resummarizeBatch(state, updated.BatchID)
		return nil
	})
	if err != nil {
		return JanitorCandidate{}, err
	}
	return updated, nil
}

// ErrCandidateNotFound reports a candidate ID that is not in this workspace's
// scan state — including one that belongs to another workspace, which is
// indistinguishable from unknown by design.
var ErrCandidateNotFound = errors.New("downloads janitor candidate not found")

func resummarizeBatch(state *ScanState, batchID string) {
	for i := range state.Batches {
		if state.Batches[i].ID == batchID {
			state.Batches[i] = SummarizeBatch(state.Batches[i], state.CandidatesFor(batchID))
			return
		}
	}
}

// RecordObservation stores or refreshes a file sighting for the settling check.
// An unchanged sighting keeps its original FirstSeenAt — that span is what
// "settled" is measured over; a changed one restarts the clock.
func RecordObservation(state *ScanState, name string, size int64, modTime, now time.Time) SettledObservation {
	for i := range state.Observations {
		if state.Observations[i].Name != name {
			continue
		}
		if state.Observations[i].Unchanged(size, modTime) {
			state.Observations[i].ObservedAt = now
			return state.Observations[i]
		}
		state.Observations[i] = SettledObservation{
			Name: name, Size: size, ModTime: modTime, FirstSeenAt: now, ObservedAt: now,
			ChangeWitnessed: true,
		}
		return state.Observations[i]
	}
	observation := SettledObservation{
		Name: name, Size: size, ModTime: modTime, FirstSeenAt: now, ObservedAt: now,
	}
	state.Observations = append(state.Observations, observation)
	return observation
}

// MarkSkipped remembers that the user dismissed this exact file state.
func MarkSkipped(state *ScanState, fingerprint Fingerprint, now time.Time) {
	key := fingerprint.Key()
	if key == "" {
		return
	}
	for i := range state.Skipped {
		if state.Skipped[i].Key == key {
			state.Skipped[i].SkippedAt = now
			return
		}
	}
	state.Skipped = append(state.Skipped, SkippedFingerprint{Key: key, Name: fingerprint.Name, SkippedAt: now})
}

// ClearSkipped forgets one skip decision (the user resetting a skipped item).
// An empty key clears every skip, which is the "reset skipped items" setting.
func ClearSkipped(state *ScanState, key string) {
	if strings.TrimSpace(key) == "" {
		state.Skipped = nil
		return
	}
	out := state.Skipped[:0:0]
	for _, skip := range state.Skipped {
		if skip.Key != key {
			out = append(out, skip)
		}
	}
	state.Skipped = out
}
