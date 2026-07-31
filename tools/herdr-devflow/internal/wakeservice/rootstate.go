package wakeservice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/johnjallday/ori-agent/tools/herdr-devflow/internal/wakeprotocol"
)

const (
	maxRootStateBytes  = 256 * 1024
	maxIdempotencyKeys = 256
)

// rootState is the complete bounded privileged state. It intentionally carries
// no prompt, transcript, credential, environment, or repository fields.
type rootState struct {
	Version      int                      `json:"version"`
	AllowedUID   int                      `json:"allowed_uid"`
	Candidates   []wakeprotocol.Candidate `json:"candidates"`
	Programmed   *wakeprotocol.Programmed `json:"programmed,omitempty"`
	Intent       *replacementIntent       `json:"replacement_intent,omitempty"`
	Idempotency  []idempotencyRecord      `json:"idempotency,omitempty"`
	UpdatedAt    time.Time                `json:"updated_at"`
	ReconciledAt time.Time                `json:"reconciled_at,omitempty"`
}

type replacementIntent struct {
	Previous  *wakeprotocol.Programmed `json:"previous,omitempty"`
	Desired   *wakeprotocol.Candidate  `json:"desired,omitempty"`
	StartedAt time.Time                `json:"started_at"`
}

type idempotencyRecord struct {
	Key       string                 `json:"key"`
	Digest    string                 `json:"digest"`
	Operation wakeprotocol.Operation `json:"operation"`
	Result    wakeprotocol.Result    `json:"result"`
	Code      wakeprotocol.Code      `json:"code"`
	Message   string                 `json:"message,omitempty"`
	AppliedAt time.Time              `json:"applied_at"`
}

type rootStore struct {
	dir         string
	path        string
	lockPath    string
	allowedUID  int
	requireRoot bool
}

func newRootStore(dir string, allowedUID int, requireRoot bool) *rootStore {
	return &rootStore{
		dir:         dir,
		path:        filepath.Join(dir, StateFile),
		lockPath:    filepath.Join(dir, LockFile),
		allowedUID:  allowedUID,
		requireRoot: requireRoot,
	}
}

func (s *rootStore) lock(ctx context.Context) (func(), error) {
	if err := prepareStateDir(s.dir, s.requireRoot); err != nil {
		return nil, err
	}
	return acquireRootLock(ctx, s.lockPath, s.requireRoot)
}

func (s *rootStore) load() (rootState, error) {
	info, err := os.Lstat(s.path)
	if os.IsNotExist(err) {
		return newRootState(s.allowedUID), nil
	}
	if err != nil {
		return rootState{}, fmt.Errorf("inspect wake state: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return rootState{}, fmt.Errorf("wake state is not a regular file")
	}
	if info.Mode().Perm()&0077 != 0 {
		return rootState{}, fmt.Errorf("wake state permissions are broader than 0600")
	}
	if s.requireRoot {
		if owner, ok := fileOwnerUID(info); !ok || owner != 0 {
			return rootState{}, fmt.Errorf("wake state is not root-owned")
		}
	}
	file, err := os.Open(s.path) // #nosec G304 -- path is derived from the fixed/test state root.
	if err != nil {
		return rootState{}, fmt.Errorf("open wake state: %w", err)
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, maxRootStateBytes+1))
	if err != nil {
		return rootState{}, fmt.Errorf("read wake state: %w", err)
	}
	if len(payload) > maxRootStateBytes {
		return rootState{}, fmt.Errorf("wake state exceeds %d bytes", maxRootStateBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var state rootState
	if err := decoder.Decode(&state); err != nil {
		return rootState{}, fmt.Errorf("decode wake state: %w", err)
	}
	if err := validateRootState(state, s.allowedUID); err != nil {
		return rootState{}, err
	}
	return state, nil
}

func (s *rootStore) save(state rootState) error {
	if err := validateRootState(state, s.allowedUID); err != nil {
		return err
	}
	if err := prepareStateDir(s.dir, s.requireRoot); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode wake state: %w", err)
	}
	if len(payload) > maxRootStateBytes {
		return fmt.Errorf("wake state exceeds %d bytes", maxRootStateBytes)
	}
	temporary, err := os.CreateTemp(s.dir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary wake state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure temporary wake state: %w", err)
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary wake state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary wake state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary wake state: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replace wake state: %w", err)
	}
	directory, err := os.Open(s.dir)
	if err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func newRootState(allowedUID int) rootState {
	return rootState{
		Version:     wakeprotocol.StateVersion,
		AllowedUID:  allowedUID,
		Candidates:  []wakeprotocol.Candidate{},
		Idempotency: []idempotencyRecord{},
	}
}

func validateRootState(state rootState, allowedUID int) error {
	if state.Version != wakeprotocol.StateVersion {
		return fmt.Errorf("unsupported wake state version %d", state.Version)
	}
	if state.AllowedUID != allowedUID || state.AllowedUID < 0 {
		return fmt.Errorf("wake state allowed uid does not match installation")
	}
	if len(state.Candidates) > wakeprotocol.MaxCandidates {
		return fmt.Errorf("wake state has too many candidates")
	}
	if len(state.Idempotency) > maxIdempotencyKeys {
		return fmt.Errorf("wake state has too many idempotency records")
	}
	seenCandidates := make(map[string]struct{}, len(state.Candidates))
	for _, candidate := range state.Candidates {
		key := candidateKey(candidate.Source, candidate.Purpose, candidate.ID)
		if _, exists := seenCandidates[key]; exists {
			return fmt.Errorf("wake state has duplicate candidate %s", key)
		}
		seenCandidates[key] = struct{}{}
	}
	seenKeys := make(map[string]struct{}, len(state.Idempotency))
	for _, record := range state.Idempotency {
		if record.Key == "" || record.Digest == "" {
			return fmt.Errorf("wake state has incomplete idempotency record")
		}
		if _, exists := seenKeys[record.Key]; exists {
			return fmt.Errorf("wake state has duplicate idempotency key")
		}
		seenKeys[record.Key] = struct{}{}
	}
	return nil
}

func (s rootState) public() wakeprotocol.State {
	candidates := append([]wakeprotocol.Candidate(nil), s.Candidates...)
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].WakeAt.Equal(candidates[j].WakeAt) {
			return candidateKey(candidates[i].Source, candidates[i].Purpose, candidates[i].ID) <
				candidateKey(candidates[j].Source, candidates[j].Purpose, candidates[j].ID)
		}
		return candidates[i].WakeAt.Before(candidates[j].WakeAt)
	})
	return wakeprotocol.State{
		StateVersion: wakeprotocol.StateVersion,
		AllowedUID:   s.AllowedUID,
		Candidates:   candidates,
		Programmed:   cloneProgrammed(s.Programmed),
		ReconciledAt: s.ReconciledAt,
	}
}

func (s *rootState) findCandidate(target wakeprotocol.Target) (int, *wakeprotocol.Candidate) {
	for index := range s.Candidates {
		candidate := &s.Candidates[index]
		if candidate.ID == target.ID && candidate.Source == target.Source && candidate.Purpose == target.Purpose {
			return index, candidate
		}
	}
	return -1, nil
}

func (s *rootState) winner(now time.Time) *wakeprotocol.Candidate {
	var winner *wakeprotocol.Candidate
	for index := range s.Candidates {
		candidate := &s.Candidates[index]
		if !candidate.WakeAt.After(now) || !candidate.ExpiresAt.After(now) {
			continue
		}
		if winner == nil || candidate.WakeAt.Before(winner.WakeAt) ||
			(candidate.WakeAt.Equal(winner.WakeAt) &&
				candidateKey(candidate.Source, candidate.Purpose, candidate.ID) <
					candidateKey(winner.Source, winner.Purpose, winner.ID)) {
			copy := *candidate
			winner = &copy
		}
	}
	return winner
}

func (s *rootState) prune(now time.Time) {
	kept := s.Candidates[:0]
	for _, candidate := range s.Candidates {
		if candidate.WakeAt.After(now) && candidate.ExpiresAt.After(now) {
			kept = append(kept, candidate)
		}
	}
	s.Candidates = kept
}

func (s *rootState) idempotency(key string) *idempotencyRecord {
	for index := range s.Idempotency {
		if s.Idempotency[index].Key == key {
			return &s.Idempotency[index]
		}
	}
	return nil
}

func (s *rootState) remember(record idempotencyRecord) {
	if len(s.Idempotency) >= maxIdempotencyKeys {
		copy(s.Idempotency, s.Idempotency[len(s.Idempotency)-maxIdempotencyKeys+1:])
		s.Idempotency = s.Idempotency[:maxIdempotencyKeys-1]
	}
	s.Idempotency = append(s.Idempotency, record)
}

func candidateKey(source wakeprotocol.Source, purpose wakeprotocol.Purpose, id string) string {
	return string(source) + "/" + string(purpose) + "/" + id
}

func cloneProgrammed(programmed *wakeprotocol.Programmed) *wakeprotocol.Programmed {
	if programmed == nil {
		return nil
	}
	copy := *programmed
	return &copy
}
