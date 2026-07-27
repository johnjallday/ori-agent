package downloadsjanitor

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Approval is the gate between "the user decided something" and "Ori changed a
// file". It exists because a decision recorded in a review surface is not, by
// itself, authorization to mutate: the user must see the final resolved plan
// and confirm that exact plan.
//
// The handshake is two steps:
//
//  1. Preview — the server re-derives every destination from its own state,
//     resolves collisions, and hands back a token bound to that exact plan.
//  2. Confirm — the token is consumed atomically before anything is touched,
//     and every binding is rechecked. A replayed, expired, tampered, or
//     cross-user token buys nothing.
//
// The token is not a capability the client can widen. It authorizes precisely
// the payload it was issued for: change any decision, any category, or any
// file's fingerprint, and the hash no longer matches.

// ApprovalTTL is how long a preview's approval stays valid. Long enough to read
// the plan and confirm; short enough that a token left in a closed tab is not a
// standing permission.
const ApprovalTTL = 10 * time.Minute

// MaxRetainedApprovals bounds stored approvals. Consumed and expired entries
// are pruned; the cap is a backstop.
const MaxRetainedApprovals = 50

var (
	// ErrApprovalRequired reports a confirm with no usable approval token.
	ErrApprovalRequired = errors.New("downloads janitor approval is required")
	// ErrApprovalInvalid reports a token that does not match this user,
	// workspace, batch, or plan — including a tampered payload.
	ErrApprovalInvalid = errors.New("downloads janitor approval is not valid for this request")
	// ErrApprovalExpired reports a token past its expiry.
	ErrApprovalExpired = errors.New("downloads janitor approval has expired")
	// ErrApprovalConsumed reports a token that was already used. Approvals are
	// single-use so a replayed confirm cannot move the same file twice.
	ErrApprovalConsumed = errors.New("downloads janitor approval was already used")
	// ErrCandidateNotActionable reports a candidate that cannot be approved in
	// its current state (already applied, skipped, or stale).
	ErrCandidateNotActionable = errors.New("downloads janitor candidate cannot be approved in its current state")
)

// ApprovalRecord is the stored half of an issued approval. The token itself is
// never stored: only its hash, so the state file is not a file full of usable
// bearer credentials.
type ApprovalRecord struct {
	ID string `json:"id"`
	// TokenHash is sha256 of the token handed to the client.
	TokenHash string `json:"token_hash"`
	// UserID, WorkspaceID, and BatchID scope what this approval can authorize.
	UserID      string `json:"user_id"`
	WorkspaceID string `json:"workspace_id"`
	BatchID     string `json:"batch_id"`
	// PayloadHash binds the approval to the exact normalized plan the user saw,
	// including each candidate's fingerprint at preview time.
	PayloadHash string `json:"payload_hash"`
	// IdempotencyKey identifies the apply this approval authorizes, so a retry
	// of the same confirm is recognized rather than re-executed.
	IdempotencyKey string    `json:"idempotency_key"`
	IssuedAt       time.Time `json:"issued_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	ConsumedAt     time.Time `json:"consumed_at,omitzero"`
}

// Consumed reports whether this approval has already been used.
func (a ApprovalRecord) Consumed() bool { return !a.ConsumedAt.IsZero() }

// Expired reports whether the approval is past its expiry.
func (a ApprovalRecord) Expired(now time.Time) bool { return now.After(a.ExpiresAt) }

// PlanItem is one normalized decision inside an approval payload.
type PlanItem struct {
	CandidateID string
	Operation   Operation
	Category    Category
	// FingerprintKey binds the item to the file state the user was shown. If
	// the file changes between preview and confirm, the plan no longer matches.
	FingerprintKey string
}

// PayloadHash computes the stable hash of a plan. Items are sorted so the same
// plan submitted in a different order hashes identically — the order the client
// happened to send is not part of what the user approved.
func PayloadHash(workspaceID, batchID string, items []PlanItem) string {
	normalized := make([]string, 0, len(items))
	for _, item := range items {
		normalized = append(normalized, strings.Join([]string{
			item.CandidateID,
			string(item.Operation),
			string(item.Category),
			item.FingerprintKey,
		}, "|"))
	}
	sort.Strings(normalized)
	sum := sha256.Sum256([]byte(workspaceID + "\x00" + batchID + "\x00" + strings.Join(normalized, "\n")))
	return hex.EncodeToString(sum[:])
}

// newApprovalToken returns a fresh opaque token and its hash.
func newApprovalToken() (token, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("failed to generate an approval token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

// findApproval locates a stored approval by token hash using a constant-time
// comparison, so a caller cannot learn a valid hash by timing this lookup.
func findApproval(state ScanState, tokenHash string) (int, bool) {
	for i := range state.Approvals {
		if subtle.ConstantTimeCompare([]byte(state.Approvals[i].TokenHash), []byte(tokenHash)) == 1 {
			return i, true
		}
	}
	return -1, false
}

// pruneApprovals drops expired approvals, then caps the rest.
//
// A consumed approval is deliberately retained until it expires. Dropping it at
// consumption would make a replayed confirm — a double-click, a retried request
// after a dropped connection — indistinguishable from a forged token: the
// caller would be told the approval is invalid rather than that it was already
// used, and could not tell a completed apply from a rejected one. Keeping the
// spent record for its remaining lifetime lets the honest retry get the honest
// answer.
func pruneApprovals(approvals []ApprovalRecord, now time.Time) []ApprovalRecord {
	kept := approvals[:0:0]
	for _, approval := range approvals {
		if approval.Expired(now) {
			continue
		}
		kept = append(kept, approval)
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].IssuedAt.Before(kept[j].IssuedAt) })
	if len(kept) > MaxRetainedApprovals {
		kept = kept[len(kept)-MaxRetainedApprovals:]
	}
	return kept
}

// ---------------------------------------------------------------- collisions

// resolveAvailableName returns a name that does not already exist in dir,
// following the Finder convention: "report.pdf", then "report (2).pdf", and so
// on (FR-84).
//
// It never returns an existing name, so a caller acting on the result cannot
// overwrite anything. Availability is rechecked at apply time as well — this
// resolution can go stale between preview and confirm, which is a normal race
// rather than an error.
func resolveAvailableName(dir, name string) (string, error) {
	if err := ValidateFileName(name); err != nil {
		return "", err
	}
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)

	for attempt := 1; attempt <= maxCollisionAttempts; attempt++ {
		candidate := name
		if attempt > 1 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, attempt, extension)
		}
		_, err := os.Lstat(filepath.Join(dir, candidate))
		if os.IsNotExist(err) {
			return candidate, nil
		}
		if err != nil && !os.IsNotExist(err) {
			// An entry we cannot inspect is treated as occupied: assuming it is
			// free is how an overwrite happens.
			continue
		}
	}
	return "", fmt.Errorf("%w: too many files named like %q already exist", ErrDestinationUnavailable, name)
}

// maxCollisionAttempts bounds the "(2)", "(3)", … search.
const maxCollisionAttempts = 200

// ErrDestinationUnavailable reports that no free destination name could be
// resolved.
var ErrDestinationUnavailable = errors.New("downloads janitor destination is unavailable")
