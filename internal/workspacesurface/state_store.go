package workspacesurface

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const (
	stateFileSchemaVersion = 1
	maxStateKeys           = 64
	maxStateValueBytes     = 64 << 10
	maxStateNamespaceBytes = 256 << 10
)

var (
	ErrStateInvalid       = errors.New("workspace surface state is invalid")
	ErrStateConflict      = errors.New("workspace surface state revision conflict")
	ErrStateQuotaExceeded = errors.New("workspace surface state quota exceeded")
)

type StateValue struct {
	Found         bool            `json:"found"`
	SchemaVersion int             `json:"schema_version,omitempty"`
	Revision      string          `json:"revision"`
	Value         json.RawMessage `json:"value,omitempty"`
}

type stateFile struct {
	SchemaVersion int                   `json:"schema_version"`
	Revision      uint64                `json:"revision"`
	Entries       map[string]stateEntry `json:"entries"`
}

type stateEntry struct {
	SchemaVersion int             `json:"schema_version"`
	Value         json.RawMessage `json:"value"`
}

type StateStore struct {
	root  string
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func NewStateStore(root string) *StateStore {
	return &StateStore{root: filepath.Clean(root), locks: make(map[string]*sync.Mutex)}
}

func (s *StateStore) Get(pluginID, workspaceID, key string) (StateValue, error) {
	if err := validateStateAddress(pluginID, workspaceID, key); err != nil {
		return StateValue{}, err
	}
	lock := s.lock(pluginID, workspaceID)
	lock.Lock()
	defer lock.Unlock()
	state, err := s.load(pluginID, workspaceID)
	if err != nil {
		return StateValue{}, err
	}
	entry, found := state.Entries[key]
	result := StateValue{Found: found, Revision: strconv.FormatUint(state.Revision, 10)}
	if found {
		result.SchemaVersion = entry.SchemaVersion
		result.Value = append(json.RawMessage(nil), entry.Value...)
	}
	return result, nil
}

func (s *StateStore) Set(pluginID, workspaceID, key string, schemaVersion int, expectedRevision string, value json.RawMessage) (StateValue, error) {
	if err := validateStateAddress(pluginID, workspaceID, key); err != nil || schemaVersion < 1 || len(value) == 0 || len(value) > maxStateValueBytes || !json.Valid(value) || !boundedJSON(value) {
		return StateValue{}, ErrStateInvalid
	}
	expected, err := strconv.ParseUint(expectedRevision, 10, 64)
	if err != nil {
		return StateValue{}, ErrStateInvalid
	}
	lock := s.lock(pluginID, workspaceID)
	lock.Lock()
	defer lock.Unlock()
	state, err := s.load(pluginID, workspaceID)
	if err != nil {
		return StateValue{}, err
	}
	if state.Revision != expected {
		return StateValue{}, ErrStateConflict
	}
	if _, exists := state.Entries[key]; !exists && len(state.Entries) >= maxStateKeys {
		return StateValue{}, ErrStateQuotaExceeded
	}
	state.Entries[key] = stateEntry{SchemaVersion: schemaVersion, Value: append(json.RawMessage(nil), value...)}
	if stateSize(state.Entries) > maxStateNamespaceBytes {
		return StateValue{}, ErrStateQuotaExceeded
	}
	state.Revision++
	if err := s.save(pluginID, workspaceID, state); err != nil {
		return StateValue{}, err
	}
	return StateValue{
		Found: true, SchemaVersion: schemaVersion,
		Revision: strconv.FormatUint(state.Revision, 10), Value: append(json.RawMessage(nil), value...),
	}, nil
}

func (s *StateStore) Delete(pluginID, workspaceID, key, expectedRevision string) (StateValue, error) {
	if err := validateStateAddress(pluginID, workspaceID, key); err != nil {
		return StateValue{}, err
	}
	expected, err := strconv.ParseUint(expectedRevision, 10, 64)
	if err != nil {
		return StateValue{}, ErrStateInvalid
	}
	lock := s.lock(pluginID, workspaceID)
	lock.Lock()
	defer lock.Unlock()
	state, err := s.load(pluginID, workspaceID)
	if err != nil {
		return StateValue{}, err
	}
	if state.Revision != expected {
		return StateValue{}, ErrStateConflict
	}
	if _, exists := state.Entries[key]; exists {
		delete(state.Entries, key)
		state.Revision++
		if err := s.save(pluginID, workspaceID, state); err != nil {
			return StateValue{}, err
		}
	}
	return StateValue{Found: false, Revision: strconv.FormatUint(state.Revision, 10)}, nil
}

// DeletePlugin is called only by explicit confirmed uninstall. Disable and
// update never call it, so compatible namespaced state remains byte-for-byte.
func (s *StateStore) DeletePlugin(pluginID string) error {
	if !idPattern.MatchString(pluginID) || s == nil {
		return ErrStateInvalid
	}
	path := filepath.Join(s.root, hashStatePart(pluginID))
	if err := os.RemoveAll(path); err != nil { // #nosec G703 -- plugin id is validated then SHA-256 encoded before joining the managed root
		return fmt.Errorf("%w: delete plugin namespace", ErrStateInvalid)
	}
	return nil
}

func (s *StateStore) load(pluginID, workspaceID string) (stateFile, error) {
	state := stateFile{SchemaVersion: stateFileSchemaVersion, Entries: make(map[string]stateEntry)}
	data, err := os.ReadFile(s.path(pluginID, workspaceID)) // #nosec G304 -- both IDs are SHA-256 encoded under the managed state root
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return stateFile{}, fmt.Errorf("%w: read namespace", ErrStateInvalid)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil || decoder.Decode(&struct{}{}) != io.EOF || state.SchemaVersion != stateFileSchemaVersion || state.Entries == nil || len(state.Entries) > maxStateKeys {
		return stateFile{}, ErrStateInvalid
	}
	for key, entry := range state.Entries {
		if !idPattern.MatchString(key) || entry.SchemaVersion < 1 || len(entry.Value) > maxStateValueBytes || !json.Valid(entry.Value) || !boundedJSON(entry.Value) {
			return stateFile{}, ErrStateInvalid
		}
	}
	if stateSize(state.Entries) > maxStateNamespaceBytes {
		return stateFile{}, ErrStateQuotaExceeded
	}
	return state, nil
}

func (s *StateStore) save(pluginID, workspaceID string, state stateFile) error {
	path := s.path(pluginID, workspaceID)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("%w: create namespace", ErrStateInvalid)
	}
	data, err := json.Marshal(state)
	if err != nil || len(data) > maxStateNamespaceBytes+(16<<10) {
		return ErrStateQuotaExceeded
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("%w: create temporary state", ErrStateInvalid)
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return ErrStateInvalid
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return ErrStateInvalid
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return ErrStateInvalid
	}
	if err := temp.Close(); err != nil {
		return ErrStateInvalid
	}
	if err := os.Rename(tempPath, path); err != nil {
		return ErrStateInvalid
	}
	committed = true
	return nil
}

func (s *StateStore) path(pluginID, workspaceID string) string {
	return filepath.Join(s.root, hashStatePart(pluginID), hashStatePart(workspaceID)+".json")
}

func (s *StateStore) lock(pluginID, workspaceID string) *sync.Mutex {
	key := pluginID + "\x00" + workspaceID
	s.mu.Lock()
	defer s.mu.Unlock()
	lock := s.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		s.locks[key] = lock
	}
	return lock
}

func validateStateAddress(pluginID, workspaceID, key string) error {
	if !idPattern.MatchString(pluginID) || strings.TrimSpace(workspaceID) == "" || len(workspaceID) > 128 || !idPattern.MatchString(key) {
		return ErrStateInvalid
	}
	return nil
}

func hashStatePart(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func stateSize(entries map[string]stateEntry) int {
	total := 0
	for key, entry := range entries {
		total += len(key) + len(entry.Value) + 32
	}
	return total
}

func boundedJSON(raw json.RawMessage) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return false
	}
	return boundedJSONValue(value, 0)
}

func boundedJSONValue(value any, depth int) bool {
	if depth > 16 {
		return false
	}
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) > 256 {
			return false
		}
		for key, child := range typed {
			if len(key) > 256 || !boundedJSONValue(child, depth+1) {
				return false
			}
		}
	case []any:
		if len(typed) > 256 {
			return false
		}
		for _, child := range typed {
			if !boundedJSONValue(child, depth+1) {
				return false
			}
		}
	case string:
		return len(typed) <= maxStateValueBytes
	}
	return true
}
