package usagelimits

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/johnjallday/ori-agent/internal/config"
)

const mirrorWrapperName = "ori-claude-statusline"

var (
	errMirrorSettings = errors.New("claude settings could not be read safely; no settings were changed")
	errMirrorConflict = errors.New("claude's status line changed outside Ori; refusing to overwrite it")
	errMirrorWrite    = errors.New("the mirror change could not be completed; Ori retained the restoration record for retry")
)

type mirrorSettings interface {
	ClaudeStatuslineMirror() (bool, json.RawMessage)
	SetClaudeStatuslineMirror(bool, json.RawMessage)
	Save() error
}

// MirrorInstaller only modifies fixed files chosen at startup, never paths or
// command text supplied by an HTTP caller. A single owner serializes mutations.
type MirrorInstaller struct {
	mu           sync.Mutex
	settings     mirrorSettings
	settingsPath string
	wrapperPath  string
	mirrorPath   string
	status       atomic.Pointer[MirrorStatus]
}

func NewMirrorInstaller(settings *config.Manager) *MirrorInstaller {
	m := &MirrorInstaller{}
	if settings == nil {
		return m
	}
	m.settings = settings
	if home, err := os.UserHomeDir(); err == nil {
		// Deliberately independent of the read-only credential directory override.
		m.settingsPath = filepath.Join(home, ".claude", "settings.json")
	}
	dataDir, err := filepath.Abs(config.DefaultDataDir())
	if err == nil {
		m.wrapperPath = filepath.Join(dataDir, "bin", mirrorWrapperName)
		m.mirrorPath = filepath.Join(dataDir, ClaudeMirrorFilename)
	}
	m.refreshStatus()
	return m
}

// Status must not wait on a settings write or filesystem read: optional mirror
// controls cannot consume the usage endpoint's five-second response budget.
func (m *MirrorInstaller) Status() MirrorStatus {
	if status := m.status.Load(); status != nil {
		return *status
	}
	return MirrorStatus{}
}

// Called at startup and after explicit mutations, never by a polling goroutine.
// Mutations re-read Claude's actual settings before deciding what to change.
func (m *MirrorInstaller) refreshStatus() {
	if m.settings == nil {
		return
	}
	registered, previous := m.settings.ClaudeStatuslineMirror()
	doc, _, err := readMirrorSettings(m.settingsPath)
	installed := false
	if registered && err == nil && equalMirrorJSON(doc["statusLine"], m.statusLine()) {
		info, statErr := os.Lstat(m.wrapperPath)
		installed = statErr == nil && info.Mode().IsRegular() && info.Mode().Perm()&0100 != 0
	}
	if !registered && err == nil {
		previous = doc["statusLine"]
	}
	command, _ := previousStatuslineCommand(previous)
	m.status.Store(&MirrorStatus{Installed: installed, PreviousCommandPresent: command != ""})
}

func (m *MirrorInstaller) Install() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	defer m.refreshStatus()
	if m.settings == nil || runtime.GOOS == "windows" || m.settingsPath == "" || m.wrapperPath == "" {
		return errors.New("the status-line mirror requires a local POSIX shell and writable configuration directories")
	}
	doc, original, err := readMirrorSettings(m.settingsPath)
	if err != nil {
		return err
	}
	installed, previous := m.settings.ClaudeStatuslineMirror()
	current := doc["statusLine"]
	ours := m.statusLine()
	if installed {
		if !equalMirrorJSON(current, ours) && !equalMirrorJSON(current, previous) {
			return errMirrorConflict
		}
	} else {
		previous = current
	}
	command, err := previousStatuslineCommand(previous)
	if err != nil {
		return err
	}
	if strings.Contains(command, mirrorWrapperName) {
		return errors.New("a status-line mirror is already configured; remove it in its owning Ori instance first")
	}
	script, err := renderClaudeMirrorWrapper(m.mirrorPath, command)
	if err != nil {
		return errMirrorSettings
	}
	// Persist the exact prior JSON value before pointing Claude at our wrapper.
	// Keep it on partial failure: Install/Remove can safely resume after restart.
	if !installed {
		if err := m.save(true, previous); err != nil {
			return err
		}
	}
	if err := writeMirrorFile(m.wrapperPath, []byte(script), 0700); err != nil {
		return errMirrorWrite
	}
	doc["statusLine"] = ours
	return replaceMirrorSettings(m.settingsPath, original, doc)
}

func (m *MirrorInstaller) Remove() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	defer m.refreshStatus()
	if m.settings == nil {
		return errMirrorSettings
	}
	installed, previous := m.settings.ClaudeStatuslineMirror()
	if !installed {
		return nil
	}
	doc, original, err := readMirrorSettings(m.settingsPath)
	if err != nil {
		return err
	}
	current := doc["statusLine"]
	if equalMirrorJSON(current, m.statusLine()) {
		if previous == nil {
			delete(doc, "statusLine")
		} else {
			doc["statusLine"] = previous
		}
		if err := replaceMirrorSettings(m.settingsPath, original, doc); err != nil {
			return err
		}
	} else if !equalMirrorJSON(current, previous) {
		return errMirrorConflict
	}
	// Restore first, delete second, clear the backup last. A retry is safe after
	// any partial failure; an edited status line is never silently overwritten.
	for _, path := range []string{m.wrapperPath, m.mirrorPath} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) { // #nosec G703 -- fixed Ori-owned wrapper/mirror paths selected at startup, not HTTP input.
			return errMirrorWrite
		}
	}
	return m.save(false, nil)
}

func (m *MirrorInstaller) save(installed bool, previous json.RawMessage) error {
	oldInstalled, oldPrevious := m.settings.ClaudeStatuslineMirror()
	m.settings.SetClaudeStatuslineMirror(installed, previous)
	if err := m.settings.Save(); err != nil {
		m.settings.SetClaudeStatuslineMirror(oldInstalled, oldPrevious)
		return errMirrorWrite
	}
	return nil
}

func (m *MirrorInstaller) statusLine() json.RawMessage {
	data, _ := json.Marshal(map[string]string{"type": "command", "command": quoteMirrorShell(m.wrapperPath)})
	return data
}

func previousStatuslineCommand(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}
	var value struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	if json.Unmarshal(raw, &value) != nil || value.Type != "command" || strings.ContainsRune(value.Command, 0) {
		return "", errors.New("ori can only chain an existing command-type status line; no settings were changed")
	}
	return value.Command, nil
}

func equalMirrorJSON(a, b json.RawMessage) bool {
	var left, right bytes.Buffer
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	return json.Compact(&left, a) == nil && json.Compact(&right, b) == nil && bytes.Equal(left.Bytes(), right.Bytes())
}

func readMirrorSettings(path string) (map[string]json.RawMessage, []byte, error) {
	// Refuse symlinks, devices and oversized settings; never replace a symlink
	// with a new regular file and silently break the user's configuration.
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]json.RawMessage), nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return nil, nil, errMirrorSettings
	}
	file, err := os.Open(path) // #nosec G304 -- fixed Claude settings path under the user's HOME, never a request parameter.
	if err != nil {
		return nil, nil, errMirrorSettings
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	var doc map[string]json.RawMessage
	if err != nil || len(data) > 1024*1024 || json.Unmarshal(data, &doc) != nil || doc == nil {
		return nil, nil, errMirrorSettings
	}
	return doc, data, nil
}

func replaceMirrorSettings(path string, original []byte, doc map[string]json.RawMessage) error {
	_, current, err := readMirrorSettings(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, original) {
		return errMirrorConflict
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return errMirrorSettings
	}
	return writeMirrorFile(path, append(data, '\n'), 0600)
}

func writeMirrorFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return errMirrorWrite
	}
	file, err := os.CreateTemp(dir, ".ori-mirror-*")
	if err != nil {
		return errMirrorWrite
	}
	defer func() { _ = os.Remove(file.Name()) }() // #nosec G703 -- only the temporary file we just created.
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return errMirrorWrite
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return errMirrorWrite
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return errMirrorWrite
	}
	if err := file.Close(); err != nil {
		return errMirrorWrite
	}
	if err := os.Rename(file.Name(), path); err != nil { // #nosec G703 -- atomic replacement of a fixed local settings/wrapper path, not request input.
		return errMirrorWrite
	}
	return nil
}
