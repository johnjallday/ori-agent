package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeMirrorSettingsPersistAndDoNotAlias(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	m := NewManager(path)
	staleSettings := m.Get()
	previous := json.RawMessage(`{"type":"command","command":"printf invented"}`)
	m.SetClaudeStatuslineMirror(true, previous)
	previous[0] = 'x'
	installed, saved := m.ClaudeStatuslineMirror()
	if !installed || !json.Valid(saved) {
		t.Fatal("caller mutated the restoration record")
	}
	saved[0] = 'x'
	copy := m.Get()
	copy.ClaudeStatuslineMirrorPrevious[0] = 'x'
	_, saved = m.ClaudeStatuslineMirror()
	if !json.Valid(saved) {
		t.Fatal("getter exposed mutable restoration record")
	}
	staleSettings.ClaudeStatuslineMirrorPrevious = json.RawMessage(`{"command":"injected"}`)
	if err := m.Update(staleSettings); err != nil {
		t.Fatal(err)
	}
	installed, saved = m.ClaudeStatuslineMirror()
	if !installed || string(saved) != `{"type":"command","command":"printf invented"}` {
		t.Fatal("unrelated settings update erased or replaced the restoration record")
	}
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var disk Settings
	if json.Unmarshal(data, &disk) != nil || !disk.ClaudeStatuslineMirrorInstalled || string(disk.ClaudeStatuslineMirrorPrevious) == "null" {
		t.Fatal("restoration record was not persisted")
	}
}
