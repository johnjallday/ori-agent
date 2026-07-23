package connections

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"
)

// PendingAuth is one in-flight browser authorization. The state value is the
// map key; it is bound to everything needed to safely resume and validate the
// callback (FR 20): the initiating local user, the requested product, the
// active account (so a reconnect can reject a different subject), the return
// destination, and the callback URI. Nonce is validated in the returned ID
// token (FR 21).
type PendingAuth struct {
	State         string
	Nonce         string
	LocalUserID   string
	Product       ProductKey
	ActiveSubject string
	ReturnTo      string
	CallbackURI   string
	// CodeVerifier is the PKCE code verifier for this authorization. It is a
	// per-flow secret that must be remembered between the authorize request and
	// the token exchange; it never leaves the server.
	CodeVerifier string
	CreatedAt    time.Time
}

// BeginParams are the caller-supplied inputs to start an authorization; the
// store generates the state, nonce, and timestamp.
type BeginParams struct {
	LocalUserID   string
	Product       ProductKey
	ActiveSubject string
	ReturnTo      string
	CallbackURI   string
	CodeVerifier  string
}

// StateStore tracks in-flight authorizations keyed by their state value.
// Entries are single-use (removed on Consume) and expire after ttl.
//
// It is in-memory by design for V1: a process restart drops all in-flight
// flows, so a late callback finds nothing in Consume and the handler reports a
// safe "expired link" result instead of resuming into an unknown state. This is
// the explicit restart behavior FR 20 and the Technical notes require.
type StateStore struct {
	mu       sync.Mutex
	entries  map[string]PendingAuth
	ttl      time.Duration
	now      func() time.Time
	randRead func([]byte) (int, error)
}

// NewStateStore returns a store whose entries expire after ttl.
func NewStateStore(ttl time.Duration) *StateStore {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &StateStore{
		entries:  make(map[string]PendingAuth),
		ttl:      ttl,
		now:      time.Now,
		randRead: rand.Read,
	}
}

// Begin generates a cryptographically random, single-use state and nonce,
// records the pending authorization, and returns it. The caller places the
// state and nonce on the provider authorize URL.
func (s *StateStore) Begin(p BeginParams) (PendingAuth, error) {
	state, err := s.randomToken()
	if err != nil {
		return PendingAuth{}, fmt.Errorf("generate state: %w", err)
	}
	nonce, err := s.randomToken()
	if err != nil {
		return PendingAuth{}, fmt.Errorf("generate nonce: %w", err)
	}
	pa := PendingAuth{
		State:         state,
		Nonce:         nonce,
		LocalUserID:   p.LocalUserID,
		Product:       p.Product,
		ActiveSubject: p.ActiveSubject,
		ReturnTo:      p.ReturnTo,
		CallbackURI:   p.CallbackURI,
		CodeVerifier:  p.CodeVerifier,
		CreatedAt:     s.now(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictExpiredLocked()
	s.entries[state] = pa
	return pa, nil
}

// Consume returns and removes the pending authorization for a state exactly
// once. ok is false when the state is blank, unknown, already used, or expired
// — all of which the caller must treat as an expired/replayed link, not a fresh
// error (FR 20).
func (s *StateStore) Consume(state string) (PendingAuth, bool) {
	state = strings.TrimSpace(state)
	if state == "" {
		return PendingAuth{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictExpiredLocked()
	pa, ok := s.entries[state]
	if !ok {
		return PendingAuth{}, false
	}
	delete(s.entries, state) // single-use: gone before we release the lock
	return pa, true
}

// Product returns the product recorded for a pending state WITHOUT consuming it,
// so a single OAuth callback route can dispatch to the right completion (base
// identity vs product enablement). ok is false for an unknown or expired state.
func (s *StateStore) Product(state string) (ProductKey, bool) {
	state = strings.TrimSpace(state)
	if state == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictExpiredLocked()
	pa, ok := s.entries[state]
	if !ok {
		return "", false
	}
	return pa.Product, true
}

// Discard drops a state whose flow the caller abandoned (timeout/cancel) so a
// late duplicate callback finds nothing to replay against.
func (s *StateStore) Discard(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, strings.TrimSpace(state))
}

func (s *StateStore) evictExpiredLocked() {
	cutoff := s.now().Add(-s.ttl)
	for state, pa := range s.entries {
		if pa.CreatedAt.Before(cutoff) {
			delete(s.entries, state)
		}
	}
}

func (s *StateStore) randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := s.randRead(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
