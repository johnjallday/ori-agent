package workspacesurfacehttp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

const (
	defaultSessionTTL = 15 * time.Minute
	maxOpenSessions   = 256
)

var (
	errSessionUnknown = errors.New("workspace surface session is unknown")
	errSessionLimit   = errors.New("workspace surface session limit reached")
)

type surfaceSession struct {
	UserID      string
	WorkspaceID string
	SurfaceKey  string
	PluginID    string
	Generation  uint64
	Credential  string
	FrameToken  string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	Invalidated bool
}

type sessionStore struct {
	mu           sync.Mutex
	byCredential map[[sha256.Size]byte]surfaceSession
	byFrame      map[[sha256.Size]byte][sha256.Size]byte
	now          func() time.Time
	ttl          time.Duration
}

func newSessionStore() *sessionStore {
	return &sessionStore{
		byCredential: make(map[[sha256.Size]byte]surfaceSession),
		byFrame:      make(map[[sha256.Size]byte][sha256.Size]byte),
		now:          time.Now,
		ttl:          defaultSessionTTL,
	}
}

func (s *sessionStore) open(record surfaceSession) (surfaceSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	s.removeExpiredLocked(now)
	if len(s.byCredential) >= maxOpenSessions {
		return surfaceSession{}, errSessionLimit
	}
	credential, err := randomToken()
	if err != nil {
		return surfaceSession{}, err
	}
	frame, err := randomToken()
	if err != nil {
		return surfaceSession{}, err
	}
	record.Credential = credential
	record.FrameToken = frame
	record.CreatedAt = now
	record.ExpiresAt = now.Add(s.ttl)
	credentialKey := tokenKey(credential)
	s.byCredential[credentialKey] = record
	s.byFrame[tokenKey(frame)] = credentialKey
	return record, nil
}

func (s *sessionStore) credential(token string) (surfaceSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	s.removeExpiredLocked(now)
	record, ok := s.byCredential[tokenKey(token)]
	if !ok || record.Invalidated {
		return surfaceSession{}, errSessionUnknown
	}
	return record, nil
}

func (s *sessionStore) frame(token string) (surfaceSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	s.removeExpiredLocked(now)
	credentialKey, ok := s.byFrame[tokenKey(token)]
	if !ok {
		return surfaceSession{}, errSessionUnknown
	}
	record, ok := s.byCredential[credentialKey]
	if !ok || record.Invalidated {
		return surfaceSession{}, errSessionUnknown
	}
	return record, nil
}

func (s *sessionStore) close(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tokenKey(token)
	record, ok := s.byCredential[key]
	if !ok {
		return
	}
	delete(s.byCredential, key)
	delete(s.byFrame, tokenKey(record.FrameToken))
}

func (s *sessionStore) invalidateOwner(pluginID string, generation uint64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for key, record := range s.byCredential {
		if record.PluginID != pluginID || record.Generation != generation {
			continue
		}
		delete(s.byCredential, key)
		delete(s.byFrame, tokenKey(record.FrameToken))
		count++
	}
	return count
}

func (s *sessionStore) removeExpiredLocked(now time.Time) {
	for key, record := range s.byCredential {
		if now.Before(record.ExpiresAt) && !record.Invalidated {
			continue
		}
		delete(s.byCredential, key)
		delete(s.byFrame, tokenKey(record.FrameToken))
	}
}

func (s *sessionStore) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

func randomToken() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func tokenKey(token string) [sha256.Size]byte {
	return sha256.Sum256([]byte(token))
}
