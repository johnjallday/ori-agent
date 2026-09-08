// Package pathselection owns opaque, expiring references to paths returned by
// trusted native pickers. Feature APIs accept the token rather than a browser-
// supplied path, so typed consumers can revalidate the exact selection.
package pathselection

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const DefaultTTL = 30 * time.Minute

var ErrUnavailable = errors.New("trusted path selection is unavailable")

type record struct {
	path      string
	expiresAt time.Time
}

type Store struct {
	mu      sync.Mutex
	now     func() time.Time
	ttl     time.Duration
	records map[string]record
}

func NewStore() *Store {
	return &Store{now: func() time.Time { return time.Now().UTC() }, ttl: DefaultTTL, records: make(map[string]record)}
}

// Issue is called only with a path returned by Ori's native picker. The path is
// retained in process memory and never embedded in the opaque token.
func (s *Store) Issue(path string) (string, error) {
	if s == nil {
		return "", ErrUnavailable
	}
	path = filepath.Clean(strings.TrimSpace(path))
	if !filepath.IsAbs(path) {
		return "", ErrUnavailable
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", ErrUnavailable
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for key, candidate := range s.records {
		if !candidate.expiresAt.After(now) {
			delete(s.records, key)
		}
	}
	s.records[token] = record{path: path, expiresAt: now.Add(s.ttl)}
	return token, nil
}

// Resolve returns the exact picker selection while it remains live. Tokens are
// deliberately reusable across review and commit; the durable journey review
// receipt provides single-use commit consent.
func (s *Store) Resolve(token string) (string, error) {
	if s == nil {
		return "", ErrUnavailable
	}
	token = strings.TrimSpace(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate, ok := s.records[token]
	if !ok || !candidate.expiresAt.After(s.now()) {
		delete(s.records, token)
		return "", ErrUnavailable
	}
	return candidate.path, nil
}
