package calendarhttp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// confirmationTTL bounds how long a mutation preview stays confirmable.
// Short-lived by design (FR31): a preview is a snapshot of intent, not a
// durable draft, so a stale confirmation must not be usable after the user
// has plausibly walked away or the underlying data has moved on.
const confirmationTTL = 2 * time.Minute

// confirmation is a single-use, short-lived mutation authorization created by
// a Preview call and consumed by exactly one matching Confirm call (FR31,
// FR32). PayloadHash binds it to the exact normalized payload the user
// previewed -- Confirm must present the same payload (see
// hashMutationPayload) or the confirmation is rejected, which is what makes
// "invalidated if any proposed field changes" true without a mutable stored
// draft.
type confirmation struct {
	ID          string
	UserID      string
	WorkspaceID string
	BindingID   string
	Operation   string
	PayloadHash string
	ExpiresAt   time.Time
	Consumed    bool
}

// confirmationStore holds pending mutation confirmations in memory. Like
// readCache, it is process-local and never persisted -- a server restart
// invalidates every pending confirmation, which is the correct behavior for a
// short-lived, single-use authorization token.
type confirmationStore struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]*confirmation
}

func newConfirmationStore(ttl time.Duration) *confirmationStore {
	return &confirmationStore{ttl: ttl, entries: make(map[string]*confirmation)}
}

// create mints a new confirmation with a cryptographically random id
// (uuid.New uses crypto/rand) and stores it. Also opportunistically sweeps
// expired entries so the store doesn't grow unboundedly across a long-running
// process.
func (s *confirmationStore) create(userID, workspaceID, bindingID, operation, payloadHash string) *confirmation {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for id, c := range s.entries {
		if now.After(c.ExpiresAt) {
			delete(s.entries, id)
		}
	}

	c := &confirmation{
		ID:          uuid.New().String(),
		UserID:      userID,
		WorkspaceID: workspaceID,
		BindingID:   bindingID,
		Operation:   operation,
		PayloadHash: payloadHash,
		ExpiresAt:   now.Add(s.ttl),
	}
	s.entries[c.ID] = c
	return c
}

// consume atomically validates and marks a confirmation used. It returns an
// error (never a usable confirmation) unless every check passes: the id
// exists, is not expired, has not already been consumed, matches the current
// user/workspace/binding/operation, and matches payloadHash exactly. Holding
// the store lock across the whole check-and-mark sequence is what makes two
// concurrent Confirm calls for the same id resolve to exactly one success --
// the loser sees Consumed=true and is rejected (FR31: "single-use").
func (s *confirmationStore) consume(id, userID, workspaceID, bindingID, operation, payloadHash string) (*confirmation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.entries[id]
	if !ok {
		return nil, fmt.Errorf("confirmation not found or already expired")
	}
	if c.Consumed {
		return nil, fmt.Errorf("confirmation has already been used")
	}
	if time.Now().After(c.ExpiresAt) {
		delete(s.entries, id)
		return nil, fmt.Errorf("confirmation has expired")
	}
	if c.UserID != userID || c.WorkspaceID != workspaceID {
		return nil, fmt.Errorf("confirmation does not belong to the current user/workspace")
	}
	if c.BindingID != bindingID || c.Operation != operation {
		return nil, fmt.Errorf("confirmation does not match the requested binding/operation")
	}
	if c.PayloadHash != payloadHash {
		return nil, fmt.Errorf("proposed fields changed since preview; request a new preview")
	}

	c.Consumed = true
	copied := *c
	return &copied, nil
}

// hashMutationPayload canonically hashes a normalized mutation payload.
// Hashing a struct (fixed field order, via encoding/json's declaration-order
// marshaling) rather than a map means the hash is stable across calls with
// identical logical content, and any field the user changes between preview
// and confirm changes the hash.
func hashMutationPayload(payload normalizedMutationPayload) string {
	b, err := json.Marshal(payload)
	if err != nil {
		// Unreachable in practice (payload is entirely plain
		// strings/slices), but never let a marshal failure produce an
		// empty/zero hash that could accidentally match another payload.
		return "unhashable:" + err.Error()
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
