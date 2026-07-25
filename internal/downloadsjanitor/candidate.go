package downloadsjanitor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// This file defines what a scan produces: candidates (one eligible file each)
// and the batch that groups them for review.
//
// Three properties are structural rather than conventional, because the safety
// of every later step depends on them:
//
//   - A candidate's Name is a single top-level filename. It can never be a
//     path, absolute or relative, so a candidate can never address a file
//     outside the configured root.
//   - A candidate carries a Fingerprint of what the scanner saw. Nothing is
//     ever mutated without re-reading the file and matching that fingerprint,
//     which is what makes "the file changed after you approved it" detectable.
//   - A candidate's Decision is the user's, recorded separately from the
//     classifier's proposal. The two are never the same field, so an agent
//     proposal can never be mistaken for an approval.

// ScanSource records what triggered a scan. It is shown in the batch summary so
// the user can tell a scan they asked for from one that ran on its own.
type ScanSource string

const (
	// ScanSourceManual is the user pressing "Scan now".
	ScanSourceManual ScanSource = "manual"
	// ScanSourceTest is the harmless setup test scan: it reports what it would
	// consider without creating any reviewable action.
	ScanSourceTest ScanSource = "test"
	// ScanSourceWatcher is a coalesced run triggered by folder activity.
	ScanSourceWatcher ScanSource = "watcher"
	// ScanSourceDaily is the daily catch-up run.
	ScanSourceDaily ScanSource = "daily"
)

// ValidScanSources lists every recognized scan source.
var ValidScanSources = []ScanSource{ScanSourceManual, ScanSourceTest, ScanSourceWatcher, ScanSourceDaily}

// Category is one of version 1's fixed filing categories. The full registry and
// destination derivation live in categories.go; the type is declared here
// because the candidate model is expressed in terms of it.
type Category string

// CandidateState is where a candidate stands in the review lifecycle.
type CandidateState string

const (
	// CandidatePending is proposed and awaiting the user's decision.
	CandidatePending CandidateState = "pending"
	// CandidateSkipped was dismissed by the user. It stays dismissed for this
	// fingerprint until the file changes or the user resets it.
	CandidateSkipped CandidateState = "skipped"
	// CandidateApproved has an approved decision that has not been applied yet.
	CandidateApproved CandidateState = "approved"
	// CandidateApplying is mid-mutation.
	CandidateApplying CandidateState = "applying"
	// CandidateApplied completed successfully.
	CandidateApplied CandidateState = "applied"
	// CandidateStale means the file changed, moved, or vanished after it was
	// proposed. A stale candidate is never acted on; it needs a fresh scan.
	CandidateStale CandidateState = "stale"
	// CandidateFailed means the attempted action did not succeed. The source
	// file is left where it was whenever the operating system allows.
	CandidateFailed CandidateState = "failed"
)

// TerminalStates are the states a candidate no longer acts from.
func (s CandidateState) Terminal() bool {
	return s == CandidateApplied || s == CandidateSkipped
}

// Decision is what the user chose for a candidate. It is deliberately distinct
// from the classifier's proposed category: only a decision authorizes anything,
// and only the user sets one.
type Decision string

const (
	// DecisionNone is the initial value: the user has not chosen. Every
	// candidate starts here, so opening the review surface can never mutate a
	// file (FR-62).
	DecisionNone Decision = ""
	// DecisionMove files the candidate into its decided category.
	DecisionMove Decision = "move"
	// DecisionTrash sends the candidate to the recoverable system Trash. It is
	// only ever set by an explicit user action — never by the classifier, never
	// inherited from a bulk selection (FR-66).
	DecisionTrash Decision = "trash"
	// DecisionSkip dismisses the candidate without touching the file.
	DecisionSkip Decision = "skip"
)

// Mutates reports whether the decision would change the filesystem.
func (d Decision) Mutates() bool {
	return d == DecisionMove || d == DecisionTrash
}

// ConfidenceBand is the coarse confidence of a classification. A band rather
// than a bare number is what the review surface shows, so "low" is legible
// without the user interpreting a score.
type ConfidenceBand string

const (
	ConfidenceHigh   ConfidenceBand = "high"
	ConfidenceMedium ConfidenceBand = "medium"
	ConfidenceLow    ConfidenceBand = "low"
)

// ClassifierKind records how a proposal was reached, so the UI can state
// plainly whether a model was involved (FR-115).
type ClassifierKind string

const (
	// ClassifierMetadata is the deterministic name/extension/type/size/date
	// classifier. It never opens a file.
	ClassifierMetadata ClassifierKind = "metadata"
	// ClassifierModel means a configured model resolved an ambiguous case from
	// metadata.
	ClassifierModel ClassifierKind = "model"
	// ClassifierContent means bounded, opted-in content inspection was used.
	ClassifierContent ClassifierKind = "content"
	// ClassifierFallback means nothing conclusive was available and the
	// candidate fell back to Other / Needs review.
	ClassifierFallback ClassifierKind = "fallback"
)

// IneligibleReason explains why an observed file did not become a reviewable
// candidate. These are reported by a test scan and counted in a batch summary,
// but they never create an action.
type IneligibleReason string

const (
	IneligibleNotRegularFile IneligibleReason = "not_regular_file"
	IneligibleSymlink        IneligibleReason = "symlink"
	IneligibleHidden         IneligibleReason = "hidden"
	IneligibleTemporary      IneligibleReason = "temporary"
	IneligiblePartial        IneligibleReason = "partial_download"
	IneligibleInFiledFolder  IneligibleReason = "inside_filing_folder"
	IneligibleUnsettled      IneligibleReason = "still_changing"
	IneligibleAlreadyKnown   IneligibleReason = "already_proposed"
	IneligibleSkippedByUser  IneligibleReason = "skipped_by_user"
	IneligibleUnreadable     IneligibleReason = "unreadable"
)

// Fingerprint identifies the exact file state a proposal was made against.
//
// It exists for mutation safety, not for finding duplicates: two identical
// copies of one file have different fingerprints because their names differ,
// and version 1 deliberately does no duplicate detection.
type Fingerprint struct {
	// Name is the top-level filename the fingerprint was taken for.
	Name string `json:"name"`
	// Size in bytes at observation time.
	Size int64 `json:"size"`
	// ModTime at observation time.
	ModTime time.Time `json:"mod_time"`
	// FileID is the platform's file identity (device+inode where available).
	// Empty on platforms that do not expose one; matching then relies on name,
	// size, and modification time alone.
	FileID string `json:"file_id,omitempty"`
}

// Matches reports whether two fingerprints describe the same unchanged file.
// When both sides carry a file identity it must agree — that is what catches a
// file replaced in place with identical size and timestamp.
func (f Fingerprint) Matches(other Fingerprint) bool {
	if f.Name != other.Name || f.Size != other.Size || !f.ModTime.Equal(other.ModTime) {
		return false
	}
	if f.FileID != "" && other.FileID != "" && f.FileID != other.FileID {
		return false
	}
	return true
}

// Zero reports whether the fingerprint was never populated.
func (f Fingerprint) Zero() bool {
	return f.Name == "" && f.Size == 0 && f.ModTime.IsZero() && f.FileID == ""
}

// Key is the stable identity used to remember decisions across scans (a skipped
// file must stay skipped until it changes). It is hashed so a filename never
// becomes a map key that is logged verbatim.
func (f Fingerprint) Key() string {
	if f.Zero() {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		f.Name,
		fmt.Sprintf("%d", f.Size),
		f.ModTime.UTC().Format(time.RFC3339Nano),
		f.FileID,
	}, "\x00")))
	return hex.EncodeToString(sum[:16])
}

// JanitorCandidate is one eligible top-level file, its proposed filing, and the
// user's decision about it.
type JanitorCandidate struct {
	// ID is stable for the life of the candidate and is what every API call
	// refers to. Clients submit IDs, never paths.
	ID string `json:"id"`
	// WorkspaceID and BatchID scope the candidate.
	WorkspaceID string `json:"workspace_id"`
	BatchID     string `json:"batch_id,omitempty"`

	// Name is the file's name, relative to the configured root and by
	// construction a single top-level element. It is sanitized for display: see
	// SanitizeFileName.
	Name string `json:"name"`
	// Extension is the lower-cased extension including the dot, or empty.
	Extension string `json:"extension,omitempty"`
	// MIMEType is the type detected from the name/extension. Detecting it never
	// requires opening the file.
	MIMEType string `json:"mime_type,omitempty"`
	// Size in bytes and ModifiedAt as observed.
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
	// DiscoveredAt is when this candidate was first proposed.
	DiscoveredAt time.Time `json:"discovered_at"`
	// Fingerprint is the file state this proposal was made against.
	Fingerprint Fingerprint `json:"fingerprint"`

	// ScanSource records which kind of scan produced the candidate.
	ScanSource ScanSource `json:"scan_source,omitempty"`

	// Category is the classifier's proposal — not an authorization.
	Category Category `json:"category,omitempty"`
	// Reason is a short, user-facing explanation of the proposal.
	Reason string `json:"reason,omitempty"`
	// Confidence is the band shown in review; ConfidenceScore is the optional
	// underlying value.
	Confidence      ConfidenceBand `json:"confidence,omitempty"`
	ConfidenceScore float64        `json:"confidence_score,omitempty"`
	// Classifier records how the proposal was reached.
	Classifier ClassifierKind `json:"classifier,omitempty"`
	// NeedsReview marks a proposal the user should look at: unrecognized type
	// or low confidence (FR-51).
	NeedsReview bool `json:"needs_review,omitempty"`

	// State is the lifecycle position.
	State CandidateState `json:"state"`
	// StateReason explains a stale or failed state in user-facing words.
	StateReason string `json:"state_reason,omitempty"`

	// Decision is the user's choice, and DecisionCategory the category they
	// chose (which may differ from the proposed Category). DecidedAt records
	// when. These are the only fields that authorize anything.
	Decision         Decision  `json:"decision,omitempty"`
	DecisionCategory Category  `json:"decision_category,omitempty"`
	DecidedAt        time.Time `json:"decided_at,omitempty"`
}

// EffectiveCategory returns the category an approved move would use: the user's
// choice when they made one, otherwise the proposal.
func (c JanitorCandidate) EffectiveCategory() Category {
	if c.DecisionCategory != "" {
		return c.DecisionCategory
	}
	return c.Category
}

// Actionable reports whether the candidate may still be submitted for approval.
func (c JanitorCandidate) Actionable() bool {
	switch c.State {
	case CandidatePending, CandidateApproved, CandidateFailed:
		return true
	default:
		return false
	}
}

// IneligibleObservation is a file a scan looked at and deliberately did not
// propose. It carries no action and no fingerprint-backed authorization — only
// enough to explain a count in the summary or a test scan's listing.
type IneligibleObservation struct {
	Name   string           `json:"name"`
	Reason IneligibleReason `json:"reason"`
}

// BatchState is the review status of a batch as a whole.
type BatchState string

const (
	// BatchPending has candidates awaiting decisions.
	BatchPending BatchState = "pending"
	// BatchPartiallyApplied has had some decisions applied, with others still
	// pending — a batch is never all-or-nothing (FR-88).
	BatchPartiallyApplied BatchState = "partially_applied"
	// BatchResolved has no candidates left awaiting a decision.
	BatchResolved BatchState = "resolved"
)

// BatchSummary is the count line shown above a review batch (FR-60).
type BatchSummary struct {
	// Proposed is the number of candidates awaiting a decision.
	Proposed int `json:"proposed"`
	// NeedsReview counts proposals flagged for the user's attention.
	NeedsReview int `json:"needs_review"`
	// Skipped counts candidates the user dismissed.
	Skipped int `json:"skipped"`
	// Ineligible counts files the scan looked at and did not propose.
	Ineligible int `json:"ineligible"`
	// Stale counts candidates whose files changed after they were proposed.
	Stale int `json:"stale"`
	// Applied and Failed count completed outcomes.
	Applied int `json:"applied"`
	Failed  int `json:"failed"`
	// Total is the number of candidates in the batch.
	Total int `json:"total"`
}

// JanitorBatch is one scan's reviewable output.
type JanitorBatch struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspace_id"`
	Source      ScanSource `json:"source"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt time.Time  `json:"completed_at,omitempty"`
	// CandidateIDs is the batch's contents, in scan order.
	CandidateIDs []string `json:"candidate_ids,omitempty"`
	// Ineligible records what the scan skipped and why. Names only — no paths,
	// no contents.
	Ineligible []IneligibleObservation `json:"ineligible,omitempty"`
	State      BatchState              `json:"state"`
	Summary    BatchSummary            `json:"summary"`
	// NotificationID links the single Action Center entry created for this
	// batch, so repeat scans update one item instead of flooding (FR-103).
	NotificationID string `json:"notification_id,omitempty"`
}

// SummarizeBatch recomputes a batch's summary and state from its candidates.
// The batch record never carries counts that disagree with the candidates it
// points at.
func SummarizeBatch(batch JanitorBatch, candidates []JanitorCandidate) JanitorBatch {
	summary := BatchSummary{Ineligible: len(batch.Ineligible), Total: len(candidates)}
	for _, candidate := range candidates {
		switch candidate.State {
		case CandidatePending, CandidateApproved:
			summary.Proposed++
		case CandidateSkipped:
			summary.Skipped++
		case CandidateStale:
			summary.Stale++
		case CandidateApplied:
			summary.Applied++
		case CandidateFailed:
			summary.Failed++
		}
		if candidate.NeedsReview && !candidate.State.Terminal() {
			summary.NeedsReview++
		}
	}
	batch.Summary = summary
	switch {
	case summary.Proposed > 0 && (summary.Applied > 0 || summary.Failed > 0):
		batch.State = BatchPartiallyApplied
	case summary.Proposed > 0:
		batch.State = BatchPending
	default:
		batch.State = BatchResolved
	}
	return batch
}

// maxDisplayNameRunes bounds a stored filename. Long names are truncated for
// display and logging; the fingerprint keeps the untruncated name so file
// identity is unaffected.
const maxDisplayNameRunes = 180

// SanitizeFileName makes a filename safe to store, render, and log without
// letting it impersonate anything.
//
// Filenames are untrusted input: they can carry control characters, newlines
// that fake extra log lines, bidirectional overrides that disguise an
// extension, or text shaped like instructions. This strips the characters that
// enable those tricks and bounds the length. It never interprets the name, and
// callers must never treat the result as an instruction (FR-53).
//
// It returns an error when the name could not be a top-level file at all: empty,
// "."/"..", or containing a path separator. That is a containment check, not
// cosmetics — a candidate whose name is a path could address a file outside the
// configured root.
func SanitizeFileName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("%w: file name is empty", ErrInvalidCandidate)
	}
	if trimmed == "." || trimmed == ".." {
		return "", fmt.Errorf("%w: %q is not a file name", ErrInvalidCandidate, trimmed)
	}
	if strings.ContainsAny(trimmed, `/\`) || strings.ContainsRune(trimmed, 0) {
		return "", fmt.Errorf("%w: file name must be a single top-level name", ErrInvalidCandidate)
	}
	if trimmed != filepath.Base(trimmed) {
		return "", fmt.Errorf("%w: file name must be a single top-level name", ErrInvalidCandidate)
	}

	var b strings.Builder
	for _, r := range trimmed {
		switch {
		case r == utf8.RuneError:
			b.WriteRune('�')
		case unicode.IsControl(r), isBidiControl(r):
			// Control and bidi-override characters are dropped: they are what
			// makes "invoice‮gpj.exe" read as a JPEG.
			continue
		default:
			b.WriteRune(r)
		}
	}
	cleaned := strings.TrimSpace(b.String())
	if cleaned == "" {
		return "", fmt.Errorf("%w: file name has no displayable characters", ErrInvalidCandidate)
	}
	if utf8.RuneCountInString(cleaned) > maxDisplayNameRunes {
		runes := []rune(cleaned)
		cleaned = string(runes[:maxDisplayNameRunes]) + "…"
	}
	return cleaned, nil
}

// isBidiControl reports whether r is a bidirectional formatting character.
// These reorder rendered text and are a standard way to disguise a file's
// apparent extension.
func isBidiControl(r rune) bool {
	switch r {
	case '‎', '‏', '‪', '‫', '‬', '‭', '‮',
		'⁦', '⁧', '⁨', '⁩':
		return true
	}
	return false
}

// ErrInvalidCandidate reports candidate data that cannot be stored: an unusable
// name, a missing workspace, or an empty fingerprint.
var ErrInvalidCandidate = fmt.Errorf("invalid downloads janitor candidate")

// Validate enforces the candidate invariants every later step relies on.
func (c JanitorCandidate) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidCandidate)
	}
	if strings.TrimSpace(c.WorkspaceID) == "" {
		return fmt.Errorf("%w: workspace id is required", ErrInvalidCandidate)
	}
	if _, err := SanitizeFileName(c.Name); err != nil {
		return err
	}
	if c.Fingerprint.Zero() {
		return fmt.Errorf("%w: fingerprint is required", ErrInvalidCandidate)
	}
	if c.Fingerprint.Name != c.Name {
		return fmt.Errorf("%w: fingerprint does not describe this file", ErrInvalidCandidate)
	}
	if c.Decision != DecisionNone && c.Decision != DecisionMove && c.Decision != DecisionTrash && c.Decision != DecisionSkip {
		return fmt.Errorf("%w: unknown decision %q", ErrInvalidCandidate, c.Decision)
	}
	return nil
}
