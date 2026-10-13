package usagelimits

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

type memoryMirrorSettings struct {
	installed bool
	previous  json.RawMessage
	failSave  bool
	saveHook  func()
}

func (m *memoryMirrorSettings) ClaudeStatuslineMirror() (bool, json.RawMessage) {
	return m.installed, append(json.RawMessage(nil), m.previous...)
}
func (m *memoryMirrorSettings) SetClaudeStatuslineMirror(installed bool, previous json.RawMessage) {
	m.installed, m.previous = installed, append(json.RawMessage(nil), previous...)
}
func (m *memoryMirrorSettings) Save() error {
	if m.saveHook != nil {
		m.saveHook()
	}
	if m.failSave {
		return errors.New("private settings path")
	}
	return nil
}

func mirrorInstallerFixture(t *testing.T, document string) (*MirrorInstaller, *memoryMirrorSettings) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX status line installer")
	}
	dir := t.TempDir()
	store := &memoryMirrorSettings{}
	m := &MirrorInstaller{
		settings: store, settingsPath: filepath.Join(dir, ".claude", "settings.json"),
		wrapperPath: filepath.Join(dir, "ori", "bin", mirrorWrapperName),
		mirrorPath:  filepath.Join(dir, "ori", ClaudeMirrorFilename),
	}
	if document != "" {
		if err := os.MkdirAll(filepath.Dir(m.settingsPath), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(m.settingsPath, []byte(document), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m.refreshStatus()
	return m, store
}

func TestClaudeMirrorStatusDoesNotBlockOnMutation(t *testing.T) {
	m, store := mirrorInstallerFixture(t, `{}`)
	started, release := make(chan struct{}), make(chan struct{})
	store.saveHook = func() { close(started); <-release }
	done := make(chan error, 1)
	go func() { done <- m.Install() }()
	<-started
	status := make(chan MirrorStatus, 1)
	go func() { status <- m.Status() }()
	select {
	case got := <-status:
		if got.Installed {
			t.Error("incomplete mutation was published")
		}
	case <-time.After(time.Second):
		t.Error("usage snapshot status waited for a disk writer")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !m.Status().Installed {
		t.Fatal("completed mutation was not published")
	}
}

func TestClaudeMirrorInstallRemoveRoundTrip(t *testing.T) {
	for _, previous := range []string{"", "null", `{"type":"command","command":"printf 'previous status'","padding":3}`} {
		t.Run(previous, func(t *testing.T) {
			doc := `{"future_key":{"large_integer":9007199254740993},"model":"opus"}`
			var initial map[string]json.RawMessage
			_ = json.Unmarshal([]byte(doc), &initial)
			if previous != "" {
				initial["statusLine"] = json.RawMessage(previous)
			}
			canonical, _ := json.MarshalIndent(initial, "", "  ")
			canonical = append(canonical, '\n')
			m, store := mirrorInstallerFixture(t, string(canonical))
			before := m.Status()
			if before.Installed || before.PreviousCommandPresent != (previous != "" && previous != "null") {
				t.Fatal("initial mirror status does not describe the existing command")
			}
			for range 2 { // Installing twice must not chain our own wrapper.
				if err := m.Install(); err != nil {
					t.Fatal(err)
				}
			}
			if !store.installed || !m.Status().Installed || !equalMirrorJSON(store.previous, initial["statusLine"]) {
				t.Fatal("lost exact previous statusLine value")
			}
			current, _, err := readMirrorSettings(m.settingsPath)
			if err != nil || !equalMirrorJSON(current["statusLine"], m.statusLine()) || !equalMirrorJSON(current["future_key"], initial["future_key"]) {
				t.Fatal("installation lost unrelated settings")
			}
			if info, err := os.Stat(m.wrapperPath); err != nil || info.Mode().Perm() != 0700 {
				t.Fatal("wrapper must be executable only by its owner")
			}
			for range 2 {
				if err := m.Remove(); err != nil {
					t.Fatal(err)
				}
			}
			restored, err := os.ReadFile(m.settingsPath)
			if err != nil || !bytes.Equal(restored, canonical) {
				t.Fatal("canonical settings fixture did not restore byte-for-byte")
			}
			if store.installed || store.previous != nil {
				t.Fatal("restoration record not cleared")
			}
			if _, err := os.Stat(m.wrapperPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("wrapper still exists")
			}
		})
	}
}

func TestClaudeMirrorInstallCreatesMissingSettings(t *testing.T) {
	m, _ := mirrorInstallerFixture(t, "")
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(); err != nil {
		t.Fatal(err)
	}
	doc, _, err := readMirrorSettings(m.settingsPath)
	if err != nil || len(doc) != 0 {
		t.Fatal("missing original statusLine must be deleted, not set to null")
	}
}

func TestClaudeMirrorRefusesMalformedAndUnsupportedSettings(t *testing.T) {
	for _, document := range []string{"{broken", "null", "[]", `{"statusLine":{"type":"future","command":"private"}}`} {
		t.Run(document, func(t *testing.T) {
			m, store := mirrorInstallerFixture(t, document)
			if m.Install() == nil || store.installed {
				t.Fatal("unsafe settings accepted")
			}
			got, _ := os.ReadFile(m.settingsPath)
			if string(got) != document {
				t.Fatal("invalid settings were modified")
			}
		})
	}
}

func TestClaudeMirrorPreservesExternalEdits(t *testing.T) {
	m, _ := mirrorInstallerFixture(t, `{"model":"opus"}`)
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	doc, original, _ := readMirrorSettings(m.settingsPath)
	doc["model"] = json.RawMessage(`"sonnet"`)
	if err := replaceMirrorSettings(m.settingsPath, original, doc); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(); err != nil {
		t.Fatal(err)
	}
	doc, _, _ = readMirrorSettings(m.settingsPath)
	if string(doc["model"]) != `"sonnet"` {
		t.Fatal("removal reverted an unrelated user change")
	}
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	doc, original, _ = readMirrorSettings(m.settingsPath)
	doc["statusLine"] = json.RawMessage(`{"type":"command","command":"new user command"}`)
	if err := replaceMirrorSettings(m.settingsPath, original, doc); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(m.settingsPath)
	if !errors.Is(m.Remove(), errMirrorConflict) || !errors.Is(m.Install(), errMirrorConflict) {
		t.Fatal("external statusLine edit must cause a conflict")
	}
	after, _ := os.ReadFile(m.settingsPath)
	if !bytes.Equal(before, after) {
		t.Fatal("external statusLine edit was overwritten")
	}
}

func TestClaudeMirrorBackupFailureLeavesClaudeUntouched(t *testing.T) {
	m, store := mirrorInstallerFixture(t, `{"model":"opus"}`)
	store.failSave = true
	if !errors.Is(m.Install(), errMirrorWrite) || store.installed {
		t.Fatal("failed backup was not rolled back")
	}
	data, _ := os.ReadFile(m.settingsPath)
	if string(data) != `{"model":"opus"}` {
		t.Fatal("Claude changed before its restoration record was saved")
	}
}

func TestClaudeMirrorPartialInstallCanBeRemoved(t *testing.T) {
	m, store := mirrorInstallerFixture(t, `{"statusLine":{"type":"command","command":"printf original"}}`)
	// Force wrapper creation to fail after saving the restoration record.
	if err := os.MkdirAll(m.wrapperPath, 0700); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(m.Install(), errMirrorWrite) || !store.installed || len(store.previous) == 0 {
		t.Fatal("partial install lost its recovery record")
	}
	if m.Status().Installed {
		t.Fatal("partial install must not claim the wrapper is active")
	}
	if err := os.Remove(m.wrapperPath); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(); err != nil || store.installed {
		t.Fatal("could not safely recover partial install")
	}
}

func TestClaudeMirrorRejectsSettingsSymlink(t *testing.T) {
	m, store := mirrorInstallerFixture(t, `{}`)
	target := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(target, []byte(`{"private":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(m.settingsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, m.settingsPath); err != nil {
		t.Fatal(err)
	}
	if m.Install() == nil || store.installed {
		t.Fatal("symlink settings accepted")
	}
	data, _ := os.ReadFile(target)
	if string(data) != `{"private":true}` {
		t.Fatal("symlink target changed")
	}
}
