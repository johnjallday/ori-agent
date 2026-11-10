package filejanitor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	ReviewDraftSchemaVersion     = 1
	MaxReviewChoicesPerBatch     = 1000
	MaxReviewChoicesPerWorkspace = 10000
	MaxReviewEditBytes           = 256 * 1024
)

var (
	ErrReviewDraftInvalid    = errors.New("saved review choices are unavailable")
	ErrReviewConflict        = errors.New("saved review choices changed")
	ErrReviewBounds          = errors.New("saved review choice limit reached")
	ErrUnsupportedScanSchema = errors.New("unsupported File Janitor scan schema")
)

// ReviewChoice records an actual explicit selection, never an approval. The
// destination remains exclusively in the canonical candidate decision.
type ReviewChoice struct {
	CandidateID    string    `json:"candidate_id"`
	FingerprintKey string    `json:"fingerprint_key"`
	Operation      Operation `json:"operation"`
	SourceGroupID  string    `json:"source_group_id,omitempty"`
}

// ReviewDraft shares the canonical scan-state atomic transaction. An invalid
// future/malformed record retains its opaque JSON for unrelated state writes
// but supplies no choices. No token, acknowledgement or filesystem path belongs
// here. ScopeDigest binds the saved reference/binding without duplicating them.
type ReviewDraft struct {
	SchemaVersion     int            `json:"schema_version"`
	BatchID           string         `json:"batch_id"`
	Owner             string         `json:"owner"`
	RootID            string         `json:"root_id"`
	InstalledAt       time.Time      `json:"installed_at"`
	CapabilityVersion int            `json:"capability_version"`
	ScopeDigest       string         `json:"scope_digest"`
	Generation        uint64         `json:"generation"`
	Choices           []ReviewChoice `json:"choices"`
	RemovedStale      int            `json:"removed_stale,omitempty"`
	invalid           json.RawMessage
}

func (d *ReviewDraft) UnmarshalJSON(data []byte) error {
	type record ReviewDraft
	var decoded record
	if err := json.Unmarshal(data, &decoded); err != nil {
		// Keep a readable batch ID only to allow an explicit, batch-targeted
		// clear. Malformed records can never be adopted by group label.
		var identity struct {
			BatchID string `json:"batch_id"`
		}
		if err := json.Unmarshal(data, &identity); err != nil {
			identity.BatchID = ""
		}
		*d = ReviewDraft{BatchID: identity.BatchID, invalid: append(json.RawMessage(nil), data...)}
		return nil
	}
	*d = ReviewDraft(decoded)
	if d.validate() != nil {
		d.invalid = append(json.RawMessage(nil), data...)
	}
	return nil
}

func (d ReviewDraft) MarshalJSON() ([]byte, error) {
	if len(d.invalid) > 0 {
		return append([]byte(nil), d.invalid...), nil
	}
	type record ReviewDraft
	return json.Marshal(record(d))
}

func (d ReviewDraft) validate() error {
	if d.SchemaVersion != ReviewDraftSchemaVersion || !managedRootGeneration.MatchString(d.BatchID) ||
		strings.TrimSpace(d.Owner) == "" || !managedRootGeneration.MatchString(d.RootID) ||
		d.InstalledAt.IsZero() || d.CapabilityVersion < 1 || len(d.ScopeDigest) != 64 || d.Generation == 0 || d.RemovedStale < 0 {
		return ErrReviewDraftInvalid
	}
	if len(d.Choices) > MaxReviewChoicesPerBatch {
		return ErrReviewBounds
	}
	seen := make(map[string]bool, len(d.Choices))
	for _, choice := range d.Choices {
		if !managedRootGeneration.MatchString(choice.CandidateID) || len(choice.FingerprintKey) != 32 || seen[choice.CandidateID] ||
			(choice.Operation != OperationMove && choice.Operation != OperationTrash) ||
			(choice.SourceGroupID != "" && !managedRootGeneration.MatchString(choice.SourceGroupID)) {
			return ErrReviewDraftInvalid
		}
		seen[choice.CandidateID] = true
	}
	return nil
}

// ReviewChoiceView is the path-free browser shape. Binding fingerprints stay
// server-side; current canonical categories are a read projection, not another
// authoritative destination store.
type ReviewChoiceView struct {
	// GroupID is a current read projection for accurate off-page card counts;
	// SourceGroupID remains the distinct provenance used by selection Undo.
	GroupID       string    `json:"group_id,omitempty"`
	CandidateID   string    `json:"candidate_id"`
	Operation     Operation `json:"operation"`
	Category      Category  `json:"category,omitempty"`
	SourceGroupID string    `json:"source_group_id,omitempty"`
}

type ReviewDraftGroupView struct {
	ID                string   `json:"id"`
	Eligible          int      `json:"eligible"`
	Selected          int      `json:"selected"`
	Trash             int      `json:"trash"`
	GroupMarked       int      `json:"group_marked"`
	Category          Category `json:"category,omitempty"`
	MixedDestinations bool     `json:"mixed_destinations"`
}

type ReviewDraftView struct {
	BatchID       string                 `json:"batch_id"`
	Revision      string                 `json:"revision"`
	GroupRevision string                 `json:"group_revision"`
	State         string                 `json:"state"`
	Choices       []ReviewChoiceView     `json:"choices"`
	RemovedStale  int                    `json:"removed_stale"`
	Groups        []ReviewDraftGroupView `json:"groups"`
}

// projectReviewDraft never persists reconciliation. Every caller receives a
// revision of the actual evidence, so a second tab cannot overwrite an edit by
// submitting a timestamp or a stale label. The same projection is revalidated
// under the canonical write/preview lock before any authority is issued.
type reviewDraftBinding struct {
	Owner             string
	RootID            string
	InstalledAt       time.Time
	CapabilityVersion int
	Digest            string
}

func projectReviewDraft(state ScanState, batchID string, binding reviewDraftBinding, groupRevision string) ReviewDraftView {
	view := ReviewDraftView{BatchID: batchID, GroupRevision: groupRevision, State: "empty", Choices: []ReviewChoiceView{}}
	var stored *ReviewDraft
	duplicate := false
	for i := range state.ReviewDrafts {
		if state.ReviewDrafts[i].BatchID == batchID {
			if stored != nil {
				duplicate = true
			}
			stored = &state.ReviewDrafts[i]
		}
	}
	generation := uint64(0)
	if stored != nil {
		generation = stored.Generation
		view.RemovedStale = stored.RemovedStale
		switch {
		case duplicate || len(stored.invalid) > 0 || stored.validate() != nil:
			view.State = "invalid"
			view.RemovedStale += len(stored.Choices)
		case stored.Owner != binding.Owner || stored.ScopeDigest != binding.Digest || stored.RootID != binding.RootID ||
			!stored.InstalledAt.Equal(binding.InstalledAt) || stored.CapabilityVersion != binding.CapabilityVersion:
			view.State = "stale"
			view.RemovedStale += len(stored.Choices)
		default:
			view.State = "saved"
			byID := make(map[string]JanitorCandidate, len(state.Candidates))
			for _, candidate := range state.Candidates {
				byID[candidate.ID] = candidate
			}
			for _, choice := range stored.Choices {
				candidate, found := byID[choice.CandidateID]
				definition, categoryErr := LookupCategory(string(candidate.EffectiveCategory()))
				if !found || candidate.BatchID != batchID || candidate.WorkspaceID != state.WorkspaceID ||
					!proposalPending(candidate) || candidate.Fingerprint.Key() != choice.FingerprintKey ||
					(choice.Operation == OperationMove && (categoryErr != nil || (candidate.NeedsReview && (candidate.DecisionCategory == "" || candidate.DecisionGroupID != "")))) {
					if !found || (candidate.State != CandidateApplied && candidate.State != CandidateApplying) {
						view.RemovedStale++
					}
					continue
				}
				item := ReviewChoiceView{CandidateID: choice.CandidateID, Operation: choice.Operation, SourceGroupID: choice.SourceGroupID}
				if choice.Operation == OperationMove {
					item.Category = definition.ID
				}
				view.Choices = append(view.Choices, item)
			}
		}
	}
	sort.Slice(view.Choices, func(i, j int) bool { return view.Choices[i].CandidateID < view.Choices[j].CandidateID })
	// All fields here have a bounded JSON representation. Include raw invalid
	// evidence in the digest so replacing one malformed record conflicts too.
	// Category-only edits are not selections, but two tabs must not silently
	// overwrite those destinations either. Bind their canonical values without
	// timestamp/approval-token churn or a shadow destination map.
	var destinations []any
	for _, candidate := range state.Candidates {
		if candidate.BatchID == batchID {
			destinations = append(destinations, []any{candidate.ID, candidate.Category, candidate.Decision, candidate.DecisionCategory, candidate.DecisionGroupID})
		}
	}
	data, err := json.Marshal([]any{ReviewDraftSchemaVersion, state.WorkspaceID, batchID, binding, groupRevision, generation, view.State, view.Choices, view.RemovedStale, stored, destinations})
	if err != nil {
		view.State, view.Choices = "invalid", []ReviewChoiceView{}
		data = []byte(fmt.Sprintf("invalid:%s:%s", batchID, binding.Digest))
	}
	sum := sha256.Sum256(data)
	view.Revision = hex.EncodeToString(sum[:])
	return view
}

// reconcileLegacyDraftDecision keeps the old category/skip endpoint compatible
// without treating its category-only DecisionMove as a selection. Explicit
// clears/skips cancel only that member's actual choice; a category change on a
// selected member becomes a per-file exception, preserving unrelated choices.
func reconcileLegacyDraftDecision(state *ScanState, previous, candidate JanitorCandidate, decision Decision) error {
	for i := range state.ReviewDrafts {
		draft := &state.ReviewDrafts[i]
		if draft.BatchID != candidate.BatchID || len(draft.invalid) > 0 {
			continue
		}
		choices := make([]ReviewChoice, 0, len(draft.Choices))
		touched := false
		for _, choice := range draft.Choices {
			if choice.CandidateID != candidate.ID {
				choices = append(choices, choice)
				continue
			}
			touched = true
			if decision == DecisionNone || decision == DecisionSkip {
				continue
			}
			// Reconcile canonical evidence before the category edit: answering a
			// category cannot resurrect invalid classification/file evidence.
			// Current disk state is revalidated by draft reads and preview; this
			// legacy metadata-only edit never probes a revoked/unavailable root.
			valid := proposalPending(previous) && choice.FingerprintKey == previous.Fingerprint.Key() &&
				!(choice.Operation == OperationMove && previous.NeedsReview && (previous.DecisionCategory == "" || previous.DecisionGroupID != ""))
			if !valid {
				draft.RemovedStale++
				continue
			}
			choice.SourceGroupID = ""
			choices = append(choices, choice)
		}
		if touched {
			if draft.Generation == math.MaxUint64 {
				return ErrReviewBounds
			}
			draft.Choices = choices
			draft.Generation++
		}
	}
	return nil
}

func pruneReviewDrafts(state *ScanState) {
	batches := make(map[string]bool, len(state.Batches))
	for _, batch := range state.Batches {
		batches[batch.ID] = true
	}
	candidates := make(map[string]JanitorCandidate, len(state.Candidates))
	for _, candidate := range state.Candidates {
		candidates[candidate.ID] = candidate
	}
	kept := make([]ReviewDraft, 0, len(state.ReviewDrafts))
	for _, draft := range state.ReviewDrafts {
		if len(draft.invalid) > 0 {
			// Do not silently down-write unknown schema data on unrelated scans.
			// A known batch follows canonical retention; an unscoped invalid
			// record stays inert until explicitly repaired, never adopted.
			if draft.BatchID == "" || batches[draft.BatchID] {
				kept = append(kept, draft)
			}
			continue
		}
		if !batches[draft.BatchID] {
			continue
		}
		choices := make([]ReviewChoice, 0, len(draft.Choices))
		for _, choice := range draft.Choices {
			candidate, found := candidates[choice.CandidateID]
			if found && candidate.BatchID == draft.BatchID && proposalPending(candidate) && candidate.Fingerprint.Key() == choice.FingerprintKey {
				choices = append(choices, choice)
			} else if !found || (candidate.State != CandidateApplied && candidate.State != CandidateApplying) {
				draft.RemovedStale++
			}
		}
		draft.Choices = choices
		kept = append(kept, draft)
	}
	state.ReviewDrafts = kept
}
