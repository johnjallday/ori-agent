package workspacesurfacehttp

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"
)

const (
	confirmationTTL  = 2 * time.Minute
	maxConfirmations = 256
)

var errConfirmationInvalid = errors.New("workspace surface confirmation is invalid")

type confirmationBinding struct {
	UserID       string
	WorkspaceID  string
	PluginID     string
	Generation   uint64
	CapabilityID string
	CallerID     string
	OperationID  string
	PayloadHash  [sha256.Size]byte
}

type confirmationRecord struct {
	ID        string
	TokenHash [sha256.Size]byte
	Binding   confirmationBinding
	ExpiresAt time.Time
	Approved  bool
}

type confirmationStore struct {
	mu      sync.Mutex
	byID    map[string]confirmationRecord
	byToken map[[sha256.Size]byte]string
	now     func() time.Time
}

func newConfirmationStore() *confirmationStore {
	return &confirmationStore{
		byID: make(map[string]confirmationRecord), byToken: make(map[[sha256.Size]byte]string), now: time.Now,
	}
}

func (s *confirmationStore) issue(binding confirmationBinding, input json.RawMessage) (string, error) {
	payloadHash, err := canonicalPayloadHash(input)
	if err != nil {
		return "", errConfirmationInvalid
	}
	binding.PayloadHash = payloadHash
	id, err := randomToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpiredLocked(s.clock())
	if len(s.byID) >= maxConfirmations {
		return "", errConfirmationInvalid
	}
	s.byID[id] = confirmationRecord{ID: id, Binding: binding, ExpiresAt: s.clock().Add(confirmationTTL)}
	return id, nil
}

// approve is called only by the authenticated host confirmation endpoint. The
// plugin frame sees the pending ID/error but never receives the returned token.
func (s *confirmationStore) approve(id string, binding confirmationBinding) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	s.removeExpiredLocked(now)
	record, ok := s.byID[id]
	if !ok || record.Approved || !sameConfirmationCaller(record.Binding, binding) {
		return "", errConfirmationInvalid
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	record.Approved = true
	record.TokenHash = tokenKey(token)
	s.byID[id] = record
	s.byToken[record.TokenHash] = id
	return token, nil
}

// consume validates and deletes before service dispatch, so replay and a retry
// after service crash both require a new host confirmation.
func (s *confirmationStore) consume(token string, binding confirmationBinding, input json.RawMessage) error {
	payloadHash, err := canonicalPayloadHash(input)
	if err != nil {
		return errConfirmationInvalid
	}
	binding.PayloadHash = payloadHash
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpiredLocked(s.clock())
	tokenHash := tokenKey(token)
	id, ok := s.byToken[tokenHash]
	if !ok {
		return errConfirmationInvalid
	}
	record, ok := s.byID[id]
	if !ok || !record.Approved || record.TokenHash != tokenHash || !sameConfirmationBinding(record.Binding, binding, true) {
		return errConfirmationInvalid
	}
	delete(s.byID, id)
	delete(s.byToken, tokenHash)
	return nil
}

func (s *confirmationStore) cancel(id string, binding confirmationBinding) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.byID[id]
	if !ok || !sameConfirmationCaller(record.Binding, binding) {
		return false
	}
	delete(s.byID, id)
	if record.Approved {
		delete(s.byToken, record.TokenHash)
	}
	return true
}

func (s *confirmationStore) invalidateOwner(pluginID string, generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, record := range s.byID {
		if record.Binding.PluginID == pluginID && record.Binding.Generation == generation {
			delete(s.byID, id)
			delete(s.byToken, record.TokenHash)
		}
	}
}

func (s *confirmationStore) invalidateCapability(workspaceID, pluginID, capabilityID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, record := range s.byID {
		if record.Binding.WorkspaceID == workspaceID && record.Binding.PluginID == pluginID && record.Binding.CapabilityID == capabilityID {
			delete(s.byID, id)
			delete(s.byToken, record.TokenHash)
		}
	}
}

func (s *confirmationStore) removeExpiredLocked(now time.Time) {
	for id, record := range s.byID {
		if now.Before(record.ExpiresAt) {
			continue
		}
		delete(s.byID, id)
		delete(s.byToken, record.TokenHash)
	}
}

func (s *confirmationStore) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

func sameConfirmationCaller(left, right confirmationBinding) bool {
	return left.UserID == right.UserID && left.WorkspaceID == right.WorkspaceID && left.PluginID == right.PluginID &&
		left.Generation == right.Generation && left.CapabilityID == right.CapabilityID && left.CallerID == right.CallerID
}

func sameConfirmationBinding(left, right confirmationBinding, includePayload bool) bool {
	if !sameConfirmationCaller(left, right) || left.OperationID != right.OperationID {
		return false
	}
	return !includePayload || left.PayloadHash == right.PayloadHash
}

func canonicalPayloadHash(input json.RawMessage) ([sha256.Size]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return [sha256.Size]byte{}, errConfirmationInvalid
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return [sha256.Size]byte{}, errConfirmationInvalid
	}
	return sha256.Sum256(canonical), nil
}
