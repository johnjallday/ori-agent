package blueprintintake

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	ErrProposalNotFound = errors.New("intake proposal not found")
	ErrProposalStale    = errors.New("the intake proposal changed; review it again")
)

type proposalFile struct {
	Version   int                      `json:"version"`
	Proposals map[string]Proposal      `json:"proposals"`
	Ledger    map[string][]LedgerEntry `json:"ledger,omitempty"`
}

type ProposalStore struct {
	folders workspaceFolderResolver
	mu      sync.Mutex
}

type workspaceFolderResolver interface {
	GetFolderPath(string) (string, error)
}

func NewProposalStore(folders workspaceFolderResolver) *ProposalStore {
	return &ProposalStore{folders: folders}
}

func (s *ProposalStore) Save(proposal Proposal) error {
	return s.update(proposal.WorkspaceID, func(state *proposalFile) error {
		if state.Proposals == nil {
			state.Proposals = make(map[string]Proposal)
		}
		if current, ok := state.Proposals[proposal.IntakeKey]; ok && current.Status == "applying" {
			return errors.New("the current intake proposal is being applied")
		}
		state.Proposals[proposal.IntakeKey] = proposal
		return nil
	})
}

func (s *ProposalStore) Get(workspaceID, intakeKey string) (Proposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, _, err := s.load(workspaceID)
	if err != nil {
		return Proposal{}, err
	}
	proposal, ok := state.Proposals[strings.TrimSpace(intakeKey)]
	if !ok {
		return Proposal{}, ErrProposalNotFound
	}
	return proposal, nil
}

func (s *ProposalStore) Ledger(workspaceID, intakeKey string) ([]LedgerEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, _, err := s.load(workspaceID)
	if err != nil {
		return nil, err
	}
	return append([]LedgerEntry(nil), state.Ledger[strings.TrimSpace(intakeKey)]...), nil
}

func (s *ProposalStore) BeginApply(workspaceID, intakeKey, hash string) (Proposal, error) {
	var proposal Proposal
	err := s.update(workspaceID, func(state *proposalFile) error {
		current, ok := state.Proposals[strings.TrimSpace(intakeKey)]
		if !ok {
			return ErrProposalNotFound
		}
		if strings.TrimSpace(hash) == "" || current.Hash != hash || current.Status != "pending" {
			return ErrProposalStale
		}
		current.Status = "applying"
		state.Proposals[current.IntakeKey] = current
		proposal = current
		return nil
	})
	return proposal, err
}

func (s *ProposalStore) Finish(workspaceID string, proposal Proposal, ledger []LedgerEntry) error {
	return s.update(workspaceID, func(state *proposalFile) error {
		current, ok := state.Proposals[proposal.IntakeKey]
		if !ok || current.Hash != proposal.Hash || current.Status != "applying" {
			return ErrProposalStale
		}
		state.Proposals[proposal.IntakeKey] = proposal
		if len(ledger) > 0 {
			state.Ledger[proposal.IntakeKey] = append(state.Ledger[proposal.IntakeKey], ledger...)
		}
		return nil
	})
}

func (s *ProposalStore) Skip(workspaceID, intakeKey, hash string) (Proposal, error) {
	var proposal Proposal
	err := s.update(workspaceID, func(state *proposalFile) error {
		current, ok := state.Proposals[strings.TrimSpace(intakeKey)]
		if !ok {
			return ErrProposalNotFound
		}
		if strings.TrimSpace(hash) == "" || current.Hash != hash || current.Status != "pending" {
			return ErrProposalStale
		}
		current.Status = "skipped"
		state.Proposals[current.IntakeKey] = current
		proposal = current
		return nil
	})
	return proposal, err
}

func (s *ProposalStore) update(workspaceID string, mutate func(*proposalFile) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, path, err := s.load(workspaceID)
	if err != nil {
		return err
	}
	if err := mutate(&state); err != nil {
		return err
	}
	return writeJSONAtomic(path, state)
}

func (s *ProposalStore) load(workspaceID string) (proposalFile, string, error) {
	if s == nil || s.folders == nil {
		return proposalFile{}, "", errors.New("proposal store is unavailable")
	}
	root, err := s.folders.GetFolderPath(strings.TrimSpace(workspaceID))
	if err != nil || strings.TrimSpace(root) == "" {
		return proposalFile{}, "", fmt.Errorf("resolve workspace folder: %w", err)
	}
	path := filepath.Join(filepath.Clean(root), StateDirName, "proposals.json")
	state := proposalFile{Version: 1, Proposals: make(map[string]Proposal), Ledger: make(map[string][]LedgerEntry)}
	data, err := os.ReadFile(path) // #nosec G304 -- fixed file under the resolved workspace folder
	if err != nil {
		if os.IsNotExist(err) {
			return state, path, nil
		}
		return proposalFile{}, "", fmt.Errorf("read intake proposals: %w", err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return proposalFile{}, "", fmt.Errorf("read intake proposals: %w", err)
	}
	if state.Proposals == nil {
		state.Proposals = make(map[string]Proposal)
	}
	if state.Ledger == nil {
		state.Ledger = make(map[string][]LedgerEntry)
	}
	state.Version = 1
	return state, path, nil
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create intake state directory: %w", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".proposal-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
