package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/johnjallday/ori-agent/internal/logger"
)

// A user who runs codex, claude, or gemini by hand in a workspace folder should
// see what that workspace's agents remember (PRD FR23–FR26). Ori always renders
// the workspace's own in-prompt memory (the selection RenderMemoryPromptSection
// makes, so a CLI sees what the workspace's agents see; global memory is never
// included) into identical AGENTS.md, CLAUDE.md and GEMINI.md under
// <workspace folder>/.ori/context/. When the user switches it on for a
// workspace, Ori also keeps a managed block in those three files at the root of
// the folder they code in.

// MemoryContextDir is where the rendered files live inside the workspace's own
// folder.
const MemoryContextDir = ".ori/context"

// MemoryContextFileNames are the files each CLI reads natively from its folder.
var MemoryContextFileNames = []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"}

const memoryContextFilePerm = 0o600

// RenderMemoryContextFile is the text of a context file, or "" when the
// workspace has no memory a prompt would include.
func RenderMemoryContextFile(workspaceName string, doc MemoryDocument) string {
	section := strings.TrimSpace(RenderMemoryPromptSection(doc, false))
	if section == "" {
		return ""
	}
	name := strings.Join(strings.Fields(workspaceName), " ")
	if name == "" {
		name = "this workspace"
	}
	return fmt.Sprintf("<!-- Written by Ori from the memory of the workspace %q. Ori rewrites it when that memory changes; edit the memory in Ori's Memory station, not here. -->\n\n%s\n", name, section)
}

// --- the per-workspace switch -------------------------------------------------

// MemoryContextSettings keeps the "Also write to the folder root" switch per
// workspace in the data dir. It is installation-local on purpose: it is
// consent to write into the user's own folder, so it must not travel with a
// copied workspace.json (the same reasoning as native-MCP consent).
type MemoryContextSettings struct {
	path string
	mu   sync.Mutex
}

type memoryContextSettingsDoc struct {
	Version   int             `json:"version"`
	RootFiles map[string]bool `json:"root_files"`
}

// NewMemoryContextSettings keeps the switches in the file at path.
func NewMemoryContextSettings(path string) *MemoryContextSettings {
	return &MemoryContextSettings{path: path}
}

func (s *MemoryContextSettings) load() memoryContextSettingsDoc {
	doc := memoryContextSettingsDoc{Version: 1, RootFiles: map[string]bool{}}
	// path is the data-dir file the server configured.
	data, err := os.ReadFile(s.path) // #nosec G304 -- a fixed file in Ori's own data dir
	if err != nil {
		return doc
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.RootFiles == nil {
		return memoryContextSettingsDoc{Version: 1, RootFiles: map[string]bool{}}
	}
	return doc
}

// RootFiles reports whether the workspace's root files are switched on.
func (s *MemoryContextSettings) RootFiles(workspaceID string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load().RootFiles[workspaceID]
}

// SetRootFiles saves the switch.
func (s *MemoryContextSettings) SetRootFiles(workspaceID string, on bool) error {
	if s == nil {
		return errors.New("memory context settings are unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.load()
	if on {
		doc.RootFiles[workspaceID] = true
	} else {
		delete(doc.RootFiles, workspaceID)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return err
	}
	return atomicWriteFile(s.path, append(data, '\n'))
}

// --- the writer ----------------------------------------------------------------

// MemoryContextWriter renders a workspace's context files. RootFolder and
// Allowed come from the server: the folder the user codes in, and the guard
// that refuses folders Ori must never write (drive roots, home, credentials).
type MemoryContextWriter struct {
	Folders    FolderResolver
	Workspaces interface {
		Get(id string) (*Workspace, error)
	}
	Settings   *MemoryContextSettings
	RootFolder func(ws *Workspace) string
	Allowed    func(folder string) error
}

// MemoryContextStatus describes a workspace's context files for the Memory
// station.
type MemoryContextStatus struct {
	RootFiles  bool     `json:"root_files"`
	ContextDir string   `json:"context_dir"`
	RootDir    string   `json:"root_dir,omitempty"`
	FileNames  []string `json:"file_names"`
}

// Status reports where the files go and whether the root switch is on.
func (w *MemoryContextWriter) Status(workspaceID string) MemoryContextStatus {
	status := MemoryContextStatus{FileNames: MemoryContextFileNames}
	if w == nil {
		return status
	}
	status.RootFiles = w.Settings.RootFiles(workspaceID)
	if folder, err := w.folder(workspaceID); err == nil {
		status.ContextDir = filepath.Join(folder, filepath.FromSlash(MemoryContextDir))
	}
	if ws, err := w.workspace(workspaceID); err == nil && w.RootFolder != nil {
		status.RootDir = w.RootFolder(ws)
	}
	return status
}

func (w *MemoryContextWriter) workspace(workspaceID string) (*Workspace, error) {
	if w.Workspaces == nil {
		return nil, errors.New("workspace lookup unavailable")
	}
	ws, err := w.Workspaces.Get(workspaceID)
	if err != nil || ws == nil {
		return nil, fmt.Errorf("workspace %s not found", workspaceID)
	}
	return ws, nil
}

func (w *MemoryContextWriter) folder(workspaceID string) (string, error) {
	if w.Folders == nil {
		return "", errors.New("folder resolver unavailable")
	}
	folder, err := w.Folders.GetFolderPath(workspaceID)
	if err != nil || strings.TrimSpace(folder) == "" {
		return "", fmt.Errorf("workspace %s has no folder", workspaceID)
	}
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		return "", fmt.Errorf("workspace folder is missing")
	}
	return folder, nil
}

// Render rewrites the workspace's context files from its memory. Every failure
// is a log line and a skip: rendering never fails a memory write.
func (w *MemoryContextWriter) Render(workspaceID string) {
	w.render(workspaceID, false)
}

// RemoveRootBlocks takes Ori's block out of the root files after the switch
// went off, deleting a file left with nothing else in it.
func (w *MemoryContextWriter) RemoveRootBlocks(workspaceID string) {
	w.render(workspaceID, true)
}

func (w *MemoryContextWriter) render(workspaceID string, removingRoot bool) {
	if w == nil || strings.TrimSpace(workspaceID) == "" {
		return
	}
	ws, err := w.workspace(workspaceID)
	if err != nil {
		return
	}
	folder, err := w.folder(workspaceID)
	if err != nil {
		logger.Info("Skipped workspace memory context files", logger.Fields{"workspace_id": workspaceID, "reason": err.Error()})
		return
	}
	doc, err := NewMemoryStore(w.Folders).Read(workspaceID)
	if err != nil {
		logger.Info("Skipped workspace memory context files", logger.Fields{"workspace_id": workspaceID, "reason": "memory unreadable"})
		return
	}
	text := RenderMemoryContextFile(ws.Name, doc)

	contextDir := filepath.Join(folder, filepath.FromSlash(MemoryContextDir))
	for _, name := range MemoryContextFileNames {
		path := filepath.Join(contextDir, name)
		if err := syncWholeFile(path, text); err != nil {
			logger.Info("Skipped a workspace memory context file", logger.Fields{"workspace_id": workspaceID, "path": path, "reason": err.Error()})
		}
	}

	if !removingRoot && !w.Settings.RootFiles(workspaceID) {
		return
	}
	block := text
	if removingRoot {
		block = ""
	}
	root := ""
	if w.RootFolder != nil {
		root = w.RootFolder(ws)
	}
	if root == "" {
		logger.Info("Skipped root memory files", logger.Fields{"workspace_id": workspaceID, "reason": "no folder"})
		return
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		logger.Info("Skipped root memory files", logger.Fields{"workspace_id": workspaceID, "path": root, "reason": "folder missing"})
		return
	}
	if w.Allowed != nil {
		if err := w.Allowed(root); err != nil {
			logger.Info("Skipped root memory files", logger.Fields{"workspace_id": workspaceID, "path": root, "reason": "folder not allowed"})
			return
		}
	}
	for _, name := range MemoryContextFileNames {
		path := filepath.Join(root, name)
		if err := syncManagedBlock(path, block); err != nil {
			logger.Info("Skipped a root memory file", logger.Fields{"workspace_id": workspaceID, "path": path, "reason": err.Error()})
		}
	}
}

// syncWholeFile makes an Ori-owned file hold text, removing it when text is
// empty, and writes only when the content changes.
func syncWholeFile(path, text string) error {
	// path is <workspace folder>/.ori/context/<fixed name>.
	current, err := os.ReadFile(path) // #nosec G304 -- Ori's own rendered file inside the workspace folder
	exists := err == nil
	if text == "" {
		if exists {
			return os.Remove(path)
		}
		return nil
	}
	if exists && string(current) == text {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return atomicWriteFileMode(path, []byte(text), memoryContextFilePerm)
}

// syncManagedBlock applies Ori's managed block to one of the user's files,
// touching nothing outside the markers (FR24).
func syncManagedBlock(path, block string) error {
	// path is <the folder the user codes in>/<fixed name>, already checked
	// against the not-allowed guard.
	current, err := os.ReadFile(path) // #nosec G304 -- fixed file name in a guarded, user-approved folder
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	next, keep, err := ApplyManagedMemoryBlock(string(current), exists, block)
	if err != nil {
		return err
	}
	switch {
	case !keep && exists:
		return os.Remove(path)
	case !keep:
		return nil
	case exists && next == string(current):
		return nil
	}
	mode := os.FileMode(0o644)
	if exists {
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
		}
	}
	return atomicWriteFileMode(path, []byte(next), mode)
}

// atomicWriteFileMode is atomicWriteFile with a chosen mode, so a user's file
// keeps its permissions when Ori updates its block.
func atomicWriteFileMode(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// --- the write hook ------------------------------------------------------------

var (
	memoryWriteObserverMu sync.RWMutex
	memoryWriteObserver   func(workspaceID string)
)

// SetMemoryWriteObserver registers fn to run after every MEMORY.md mutation
// (FR26), once the memory lock is released. A nil fn removes it. MemoryStore
// values are built per caller, so the hook is package-wide.
func SetMemoryWriteObserver(fn func(workspaceID string)) {
	memoryWriteObserverMu.Lock()
	memoryWriteObserver = fn
	memoryWriteObserverMu.Unlock()
}

// notifyMemoryWritten runs the observer. Mutators defer it before taking the
// memory lock, so it runs after the lock is released. It also runs after a
// refused mutation; rendering is idempotent, so that costs one comparison.
func notifyMemoryWritten(workspaceID string) {
	memoryWriteObserverMu.RLock()
	fn := memoryWriteObserver
	memoryWriteObserverMu.RUnlock()
	if fn != nil && strings.TrimSpace(workspaceID) != "" {
		fn(workspaceID)
	}
}
