package trigger

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/logger"
)

// TriggersFileName is the per-workspace triggers file, a sibling of
// workspace.json. Disk is truth — there is no SQLite mirror.
const TriggersFileName = "triggers.json"

// ErrNotFound is returned when a trigger lookup misses.
var ErrNotFound = errors.New("trigger not found")

// WorkspaceSource is the slice of the workspace store the trigger store
// needs: workspace enumeration (startup load) and folder resolution
// (triggers.json placement). *workspace.FileStore satisfies it.
type WorkspaceSource interface {
	List() ([]string, error)
	GetFolderPath(workspaceID string) (string, error)
}

// triggersFile is the on-disk shape of triggers.json.
type triggersFile struct {
	Triggers []*Trigger `json:"triggers"`
}

// Store persists triggers in each workspace's folder and maintains an
// in-memory cache plus a token → trigger index for webhook lookup.
type Store struct {
	source WorkspaceSource

	mu          sync.RWMutex
	byWorkspace map[string][]*Trigger // workspaceID → triggers (cache of disk state)
	tokenIndex  map[string]*Trigger   // webhook token → trigger (enabled or not)
}

// NewStore creates a trigger store. Call LoadAll before serving lookups so
// the token index covers existing triggers.
func NewStore(source WorkspaceSource) *Store {
	return &Store{
		source:      source,
		byWorkspace: make(map[string][]*Trigger),
		tokenIndex:  make(map[string]*Trigger),
	}
}

// LoadAll reads triggers.json from every known workspace folder, replacing
// the cache and token index. Workspaces without a triggers file are skipped
// silently; unreadable files are logged and skipped so one corrupt file
// doesn't take down every trigger.
func (s *Store) LoadAll() error {
	ids, err := s.source.List()
	if err != nil {
		return fmt.Errorf("list workspaces: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.byWorkspace = make(map[string][]*Trigger)
	s.tokenIndex = make(map[string]*Trigger)

	for _, wsID := range ids {
		triggers, err := s.readWorkspaceFile(wsID)
		if err != nil {
			logger.Warn("trigger store: skipping unreadable triggers.json", logger.Fields{
				"workspace_id": wsID, "error": err,
			})
			continue
		}
		if len(triggers) == 0 {
			continue
		}
		s.byWorkspace[wsID] = triggers
		s.indexLocked(triggers)
	}
	return nil
}

// readWorkspaceFile loads and parses one workspace's triggers.json. Returns
// (nil, nil) when the file doesn't exist.
func (s *Store) readWorkspaceFile(wsID string) ([]*Trigger, error) {
	folder, err := s.source.GetFolderPath(wsID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(folder, TriggersFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var f triggersFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", TriggersFileName, err)
	}
	for _, t := range f.Triggers {
		t.WorkspaceID = wsID // folder location is authoritative
	}
	return f.Triggers, nil
}

// indexLocked adds webhook tokens to the token index. Caller holds s.mu.
func (s *Store) indexLocked(triggers []*Trigger) {
	for _, t := range triggers {
		if t.Type == TypeWebhook && t.Webhook != nil && t.Webhook.Token != "" {
			s.tokenIndex[t.Webhook.Token] = t
		}
	}
}

// persistLocked writes a workspace's triggers to disk atomically (temp file +
// rename). Caller holds s.mu.
func (s *Store) persistLocked(wsID string) error {
	folder, err := s.source.GetFolderPath(wsID)
	if err != nil {
		return fmt.Errorf("resolve workspace folder: %w", err)
	}
	f := triggersFile{Triggers: s.byWorkspace[wsID]}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal triggers: %w", err)
	}
	path := filepath.Join(folder, TriggersFileName)
	tmp, err := os.CreateTemp(folder, ".triggers-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp triggers file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write temp triggers file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp triggers file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename triggers file into place: %w", err)
	}
	return nil
}

// List returns copies of a workspace's triggers.
func (s *Store) List(wsID string) []Trigger {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Trigger, 0, len(s.byWorkspace[wsID]))
	for _, t := range s.byWorkspace[wsID] {
		out = append(out, *t)
	}
	return out
}

// ListAll returns copies of every loaded trigger across workspaces.
func (s *Store) ListAll() []Trigger {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Trigger
	for _, ts := range s.byWorkspace {
		for _, t := range ts {
			out = append(out, *t)
		}
	}
	return out
}

// Get returns a copy of one trigger.
func (s *Store) Get(wsID, triggerID string) (Trigger, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t := s.findLocked(wsID, triggerID)
	if t == nil {
		return Trigger{}, ErrNotFound
	}
	return *t, nil
}

// GetByToken resolves a webhook token to a copy of its trigger using a
// constant-time comparison over the candidate set, so lookup timing doesn't
// reveal near-miss tokens. Disabled triggers resolve too — the HTTP layer
// returns the same 404 for unknown and disabled so the two are
// indistinguishable to callers, but internally we must know the trigger
// exists to avoid re-registering its token.
func (s *Store) GetByToken(token string) (Trigger, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Map lookup alone would short-circuit on length/prefix via hashing—fine
	// in practice—but iterate with SecureCompare for explicit constant-time
	// guarantees on the match itself.
	for tok, t := range s.tokenIndex {
		if SecureCompare(tok, token) {
			return *t, true
		}
	}
	return Trigger{}, false
}

// Create validates, fills server-side fields (ID, token for webhooks,
// timestamps), persists, and returns the stored trigger.
func (s *Store) Create(t Trigger) (Trigger, error) {
	if t.ID == "" {
		t.ID = "trg-" + uuid.NewString()
	}
	now := time.Now()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	if t.Type == TypeWebhook {
		if t.Webhook == nil {
			t.Webhook = &WebhookConfig{}
		}
		if t.Webhook.Token == "" {
			tok, err := GenerateToken()
			if err != nil {
				return Trigger{}, err
			}
			t.Webhook.Token = tok
		}
	}
	if err := t.Validate(); err != nil {
		return Trigger{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	stored := t
	s.byWorkspace[t.WorkspaceID] = append(s.byWorkspace[t.WorkspaceID], &stored)
	if err := s.persistLocked(t.WorkspaceID); err != nil {
		// Roll the cache back so memory matches disk.
		ts := s.byWorkspace[t.WorkspaceID]
		s.byWorkspace[t.WorkspaceID] = ts[:len(ts)-1]
		return Trigger{}, err
	}
	s.indexLocked([]*Trigger{&stored})
	return stored, nil
}

// Update applies fn to a trigger under the store lock and persists the
// result. fn sees the live record; mutations are visible to subsequent reads
// only when persist succeeds (on failure the previous state is restored).
func (s *Store) Update(wsID, triggerID string, fn func(*Trigger) error) (Trigger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.findLocked(wsID, triggerID)
	if t == nil {
		return Trigger{}, ErrNotFound
	}
	before := *t
	oldToken := ""
	if t.Webhook != nil {
		oldToken = t.Webhook.Token
	}
	if err := fn(t); err != nil {
		*t = before
		return Trigger{}, err
	}
	t.UpdatedAt = time.Now()
	if err := t.Validate(); err != nil {
		*t = before
		return Trigger{}, err
	}
	if err := s.persistLocked(wsID); err != nil {
		*t = before
		return Trigger{}, err
	}
	// Keep the token index in sync if the token changed (regeneration).
	newToken := ""
	if t.Webhook != nil {
		newToken = t.Webhook.Token
	}
	if oldToken != newToken {
		if oldToken != "" {
			delete(s.tokenIndex, oldToken)
		}
		if newToken != "" {
			s.tokenIndex[newToken] = t
		}
	}
	return *t, nil
}

// Delete removes a trigger and persists the workspace file.
func (s *Store) Delete(wsID, triggerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts := s.byWorkspace[wsID]
	idx := -1
	for i, t := range ts {
		if t.ID == triggerID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return ErrNotFound
	}
	removed := ts[idx]
	s.byWorkspace[wsID] = append(ts[:idx:idx], ts[idx+1:]...)
	if err := s.persistLocked(wsID); err != nil {
		s.byWorkspace[wsID] = ts // restore
		return err
	}
	if removed.Webhook != nil && removed.Webhook.Token != "" {
		delete(s.tokenIndex, removed.Webhook.Token)
	}
	return nil
}

// RecordFire appends a fire record to a trigger (updating aggregates) and
// persists. Best-effort callers may ignore the error after logging.
func (s *Store) RecordFire(wsID, triggerID string, rec FireRecord) error {
	_, err := s.Update(wsID, triggerID, func(t *Trigger) error {
		t.RecordFire(rec)
		return nil
	})
	return err
}

// SetPendingFire persists (or clears, with nil) a trigger's queued fire.
func (s *Store) SetPendingFire(wsID, triggerID string, pf *PendingFire) error {
	_, err := s.Update(wsID, triggerID, func(t *Trigger) error {
		t.PendingFire = pf
		return nil
	})
	return err
}

// FindByID locates a trigger by ID across all workspaces (used by the watch
// manager, whose watch keys carry only the trigger ID).
func (s *Store) FindByID(triggerID string) (Trigger, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ts := range s.byWorkspace {
		for _, t := range ts {
			if t.ID == triggerID {
				return *t, true
			}
		}
	}
	return Trigger{}, false
}

// findLocked locates a trigger pointer. Caller holds s.mu (read or write).
func (s *Store) findLocked(wsID, triggerID string) *Trigger {
	for _, t := range s.byWorkspace[wsID] {
		if t.ID == triggerID {
			return t
		}
	}
	return nil
}
