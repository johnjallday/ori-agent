// Package folderagent is "feed a folder, get a workspace with an agent": the
// one Start action (anchored workspace, its agent, the first ticket), the
// host's walk of the folder, the report, and the evolution a Set it up runs.
//
// State lives in one record per workspace, a JSON file in the workspace's own
// sidecar folder (<managed folder>/.ori/folder-agent-v1.json). The record
// holds the folder map and facts (relative paths only), never the anchor's
// absolute path: that stays in the workspace's directory reference. Deleting
// the workspace moves its managed folder to the trash and the record with it.
package folderagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// RecordVersion is the schema version of a stored record.
const RecordVersion = 1

// recordFile is the record's name inside the workspace's .ori folder.
const recordFile = "folder-agent-v1.json"

// sidecarDirName mirrors workspace.SidecarDirName.
const sidecarDirName = ".ori"

// State is where a folder's first look stands.
type State string

const (
	StateLooking   State = "looking"
	StateReported  State = "reported"
	StateKept      State = "kept"
	StateSettingUp State = "setting_up"
	StateSetUp     State = "set_up"
	StateFailed    State = "failed"
)

// Progress is the walk's live count, shown on the tile and the Needs you row.
type Progress struct {
	Entries     int `json:"entries"`
	FoldersLeft int `json:"folders_left"`
}

// TurnReceipt says how the agent's turn over the map ended (PRD FR11):
// finished with an empty reason or step_limit, time_limit, token_limit; or
// skipped with no_model, provider_unavailable or interrupted.
type TurnReceipt struct {
	Status     string    `json:"status"`
	Reason     string    `json:"reason,omitempty"`
	Model      string    `json:"model,omitempty"`
	Tokens     int       `json:"tokens,omitempty"`
	ModelCalls int       `json:"model_calls,omitempty"`
	ToolCalls  int       `json:"tool_calls,omitempty"`
	Looks      int       `json:"looks,omitempty"`
	StartedAt  time.Time `json:"started_at,omitzero"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
}

// Turn receipt statuses and skip reasons.
const (
	TurnFinished = "finished"
	TurnSkipped  = "skipped"

	SkipNoModel             = "no_model"
	SkipProviderUnavailable = "provider_unavailable"
	SkipInterrupted         = "interrupted"
)

// Setup is a Set it up run: the plan lines the user confirmed and how far
// the run got.
type Setup struct {
	Stage      foldermap.Stage                    `json:"stage"`
	Selected   []string                           `json:"selected,omitempty"`
	Lines      []personalassistant.FolderPlanLine `json:"lines"`
	Status     string                             `json:"status"`
	StopReason string                             `json:"stop_reason,omitempty"`
	StartedAt  time.Time                          `json:"started_at"`
	FinishedAt time.Time                          `json:"finished_at,omitzero"`
}

// Record is one folder workspace's state.
type Record struct {
	Version       int    `json:"version"`
	WorkspaceID   string `json:"workspace_id"`
	OwnerUserID   string `json:"owner_user_id"`
	FolderName    string `json:"folder_name"`
	WorkspaceName string `json:"workspace_name"`
	AgentName     string `json:"agent_name"`
	TicketID      string `json:"ticket_id"`
	Brief         string `json:"brief"`
	State         State  `json:"state"`

	Map        *foldermap.Map         `json:"map,omitempty"`
	Facts      foldermap.Facts        `json:"facts"`
	Stage      foldermap.Stage        `json:"stage,omitempty"`
	Candidates []folderlook.Candidate `json:"candidates,omitempty"`
	Progress   Progress               `json:"progress"`
	Turn       *TurnReceipt           `json:"turn,omitempty"`
	Setup      *Setup                 `json:"setup,omitempty"`
	// Failure is the plain sentence a failed look shows.
	Failure string `json:"failure,omitempty"`

	Revision  int       `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (r *Record) validate() error {
	if r == nil || r.Version != RecordVersion || r.WorkspaceID == "" {
		return errors.New("folderagent: invalid record")
	}
	if r.Map != nil {
		if err := r.Map.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// ErrNoRecord means the workspace was not started from a folder.
var ErrNoRecord = errors.New("folderagent: no folder record for this workspace")

// Store reads and writes records in each workspace's sidecar folder. Writes
// are atomic (temp file + rename) and serialized per process.
type Store struct {
	sidecarDir func(workspaceID string) (string, error)
	mu         sync.Mutex
	now        func() time.Time
}

// NewStore builds a store over a resolver from workspace id to its managed
// (sidecar) folder.
func NewStore(sidecarDir func(workspaceID string) (string, error)) *Store {
	return &Store{sidecarDir: sidecarDir, now: time.Now}
}

func (s *Store) path(workspaceID string) (string, error) {
	if s == nil || s.sidecarDir == nil || workspaceID == "" {
		return "", ErrNoRecord
	}
	dir, err := s.sidecarDir(workspaceID)
	if err != nil || dir == "" {
		return "", ErrNoRecord
	}
	return filepath.Join(dir, sidecarDirName, recordFile), nil
}

// Read returns the workspace's record, or ErrNoRecord.
func (s *Store) Read(workspaceID string) (*Record, error) {
	path, err := s.path(workspaceID)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- the path is the workspace's own managed folder resolved by the workspace store
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoRecord
	}
	if err != nil {
		return nil, err
	}
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("folderagent: read record: %w", err)
	}
	if err := rec.validate(); err != nil {
		return nil, err
	}
	if rec.WorkspaceID != workspaceID {
		return nil, ErrNoRecord
	}
	return &rec, nil
}

// Create writes a new record; it refuses to overwrite one.
func (s *Store) Create(rec *Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, err := s.Read(rec.WorkspaceID); err == nil && existing != nil {
		return errors.New("folderagent: record already exists")
	}
	now := s.now().UTC()
	rec.Version, rec.Revision, rec.CreatedAt, rec.UpdatedAt = RecordVersion, 1, now, now
	return s.write(rec)
}

// Update applies fn to the current record and writes it back.
func (s *Store) Update(workspaceID string, fn func(*Record) error) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.Read(workspaceID)
	if err != nil {
		return nil, err
	}
	if err := fn(rec); err != nil {
		return nil, err
	}
	rec.Revision++
	rec.UpdatedAt = s.now().UTC()
	if err := s.write(rec); err != nil {
		return nil, err
	}
	return rec, nil
}

func (s *Store) write(rec *Record) error {
	if err := rec.validate(); err != nil {
		return err
	}
	path, err := s.path(rec.WorkspaceID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".folder-agent-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
