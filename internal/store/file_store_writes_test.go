package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

// These tests pin the write discipline that makes agent folders safe to keep
// under git or a sync tool: a save touches only the agent it names, only when
// its bytes change, atomically, and never over an edit made outside Ori.

// oldTime is far enough in the past that a rewrite is unmistakable even on a
// file system with one-second modification-time resolution.
var oldTime = time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

func writesStore(t *testing.T, dir string) *fileStore {
	t.Helper()
	st, err := NewFileStore(filepath.Join(dir, "agents.json"), types.Settings{
		Model:       "gpt-4o-mini",
		Temperature: 1.0,
	})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	fs, ok := st.(*fileStore)
	if !ok {
		t.Fatalf("NewFileStore returned %T, want *fileStore", st)
	}
	return fs
}

func definitionPath(dir, name string) string {
	return filepath.Join(dir, "agents", name, "agent_settings.json")
}

// backdate sets a file's modification time to oldTime, so any later write shows.
func backdate(t *testing.T, path string) {
	t.Helper()
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatalf("backdate %s: %v", path, err)
	}
}

func assertUntouched(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !info.ModTime().Equal(oldTime) {
		t.Errorf("%s was rewritten (mtime %v)", path, info.ModTime())
	}
}

// editOnDisk changes an agent's definition file the way a text editor would.
func editOnDisk(t *testing.T, path string, edit func(doc map[string]any)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	edit(doc)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func setPrompt(prompt string) func(doc map[string]any) {
	return func(doc map[string]any) {
		settings, _ := doc["Settings"].(map[string]any)
		settings["system_prompt"] = prompt
	}
}

func TestSaveLeavesAnUnchangedDefinitionFileAlone(t *testing.T) {
	dir := t.TempDir()
	fs := writesStore(t, dir)
	if err := fs.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	path := definitionPath(dir, "Scout")
	backdate(t, path)
	backdate(t, filepath.Join(dir, "agents.json"))

	if err := fs.Save(); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if err := fs.Save(); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	assertUntouched(t, path)
	assertUntouched(t, filepath.Join(dir, "agents.json"))
}

func TestRestartLeavesAnUnchangedDefinitionFileAlone(t *testing.T) {
	dir := t.TempDir()
	fs := writesStore(t, dir)
	if err := fs.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "scout things"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	path := definitionPath(dir, "Scout")
	backdate(t, path)

	// Opening the store runs the startup normalization and save. Every agent
	// file used to be rewritten here, which is what made a synced or
	// version-controlled folder change on every launch.
	writesStore(t, dir)
	writesStore(t, dir)
	assertUntouched(t, path)
}

func TestSetAgentWritesOnlyTheAgentItNames(t *testing.T) {
	dir := t.TempDir()
	fs := writesStore(t, dir)
	for _, name := range []string{"Alpha", "Beta"} {
		if err := fs.CreateAgent(name, &CreateAgentConfig{}); err != nil {
			t.Fatalf("CreateAgent %s: %v", name, err)
		}
	}
	betaPath := definitionPath(dir, "Beta")
	backdate(t, betaPath)

	alpha, _ := fs.GetAgent("Alpha")
	alpha.Settings.SystemPrompt = "only alpha changes"
	if err := fs.SetAgent("Alpha", alpha); err != nil {
		t.Fatalf("SetAgent: %v", err)
	}
	if err := fs.UpdateAgent("Alpha", func(ag *agent.Agent) error {
		ag.Settings.Temperature = 0.3
		return nil
	}); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}

	assertUntouched(t, betaPath)
	data, err := os.ReadFile(definitionPath(dir, "Alpha"))
	if err != nil {
		t.Fatalf("read alpha: %v", err)
	}
	if !strings.Contains(string(data), "only alpha changes") {
		t.Errorf("alpha's change was not written: %s", data)
	}
}

func TestDefinitionFileFormatIsStable(t *testing.T) {
	dir := t.TempDir()
	fs := writesStore(t, dir)
	if err := fs.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	data, err := os.ReadFile(definitionPath(dir, "Scout"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(data)
	if !strings.HasSuffix(text, "}\n") || strings.HasSuffix(text, "\n\n") {
		t.Errorf("definition must end with exactly one newline, got %q", text[max(0, len(text)-10):])
	}
	if !strings.HasPrefix(text, "{\n  \"") {
		t.Errorf("definition must use a two-space indent, got %q", text[:min(len(text), 20)])
	}
	if strings.Contains(text, "\t") {
		t.Error("definition must not contain tabs")
	}
}

func TestWritesLeaveNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	fs := writesStore(t, dir)
	if err := fs.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := fs.UpdateAgent("Scout", func(ag *agent.Agent) error {
		ag.Settings.SystemPrompt = "changed"
		return nil
	}); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}
	var leftovers []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.Contains(filepath.Base(path), ".tmp") {
			leftovers = append(leftovers, path)
		}
		return nil
	})
	if len(leftovers) > 0 {
		t.Errorf("temporary files survived: %v", leftovers)
	}
}

func TestUpdateAgentAfterAnEditOnDiskKeepsBothChanges(t *testing.T) {
	dir := t.TempDir()
	fs := writesStore(t, dir)
	if err := fs.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "original"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	path := definitionPath(dir, "Scout")
	editOnDisk(t, path, setPrompt("edited in a text editor"))

	if err := fs.UpdateAgent("Scout", func(ag *agent.Agent) error {
		ag.Settings.Temperature = 0.25
		return nil
	}); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}

	reopened := writesStore(t, dir)
	got, ok := reopened.GetAgent("Scout")
	if !ok {
		t.Fatal("Scout disappeared")
	}
	if got.Settings.SystemPrompt != "edited in a text editor" {
		t.Errorf("the on-disk edit was lost: prompt %q", got.Settings.SystemPrompt)
	}
	if got.Settings.Temperature != 0.25 {
		t.Errorf("the update was lost: temperature %v", got.Settings.Temperature)
	}
}

func TestSetAgentAfterAnEditOnDiskRefusesAndReloads(t *testing.T) {
	dir := t.TempDir()
	fs := writesStore(t, dir)
	if err := fs.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "original"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	stale, _ := fs.GetAgent("Scout")
	path := definitionPath(dir, "Scout")
	editOnDisk(t, path, setPrompt("edited in a text editor"))
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	stale.Settings.Temperature = 0.25
	err = fs.SetAgent("Scout", stale)
	if !errors.Is(err, ErrAgentChangedOnDisk) {
		t.Fatalf("SetAgent error = %v, want ErrAgentChangedOnDisk", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(after) != string(onDisk) {
		t.Error("SetAgent overwrote a file that was edited on disk")
	}
	got, _ := fs.GetAgent("Scout")
	if got.Settings.SystemPrompt != "edited in a text editor" {
		t.Errorf("memory does not match disk after the refusal: prompt %q", got.Settings.SystemPrompt)
	}
	if got.Settings.Temperature == 0.25 {
		t.Error("the refused change leaked into memory")
	}

	// The reload makes the store current again, so a retry succeeds.
	got.Settings.Temperature = 0.25
	if err := fs.SetAgent("Scout", got); err != nil {
		t.Fatalf("retry after reload: %v", err)
	}
}

func TestSetAgentAfterTheFileVanishedRefuses(t *testing.T) {
	dir := t.TempDir()
	fs := writesStore(t, dir)
	if err := fs.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	stale, _ := fs.GetAgent("Scout")
	if err := os.Remove(definitionPath(dir, "Scout")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if err := fs.SetAgent("Scout", stale); !errors.Is(err, ErrAgentChangedOnDisk) {
		t.Fatalf("SetAgent error = %v, want ErrAgentChangedOnDisk", err)
	}
	if _, err := os.Stat(definitionPath(dir, "Scout")); !os.IsNotExist(err) {
		t.Error("SetAgent recreated a definition that was deleted on disk")
	}
	if _, ok := fs.GetAgent("Scout"); ok {
		t.Error("memory still holds an agent whose file was deleted on disk")
	}
}

func TestWritesWithoutAnEditOnDiskBehaveAsBefore(t *testing.T) {
	dir := t.TempDir()
	fs := writesStore(t, dir)
	if err := fs.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	for i, prompt := range []string{"one", "two", "three"} {
		ag, _ := fs.GetAgent("Scout")
		ag.Settings.SystemPrompt = prompt
		if err := fs.SetAgent("Scout", ag); err != nil {
			t.Fatalf("SetAgent #%d: %v", i, err)
		}
	}
	if err := fs.UpdateAgent("Scout", func(ag *agent.Agent) error {
		ag.Settings.Temperature = 0.5
		return nil
	}); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}
	reopened := writesStore(t, dir)
	got, _ := reopened.GetAgent("Scout")
	if got.Settings.SystemPrompt != "three" || got.Settings.Temperature != 0.5 {
		t.Errorf("got prompt %q temperature %v", got.Settings.SystemPrompt, got.Settings.Temperature)
	}
}

func TestSaveKeepsAnEditOnDisk(t *testing.T) {
	dir := t.TempDir()
	fs := writesStore(t, dir)
	if err := fs.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "original"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	path := definitionPath(dir, "Scout")
	editOnDisk(t, path, setPrompt("edited in a text editor"))
	backdate(t, path)

	if err := fs.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	assertUntouched(t, path)
	got, _ := fs.GetAgent("Scout")
	if got.Settings.SystemPrompt != "edited in a text editor" {
		t.Errorf("Save did not pick up the on-disk edit: prompt %q", got.Settings.SystemPrompt)
	}
}

func TestStoreNoLongerWritesAnIndexIntoTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(t.TempDir())
	writesStore(t, dir)
	if _, err := os.Stat("agents.json"); !os.IsNotExist(err) {
		t.Errorf("the store wrote agents.json into the working directory (err=%v)", err)
	}
}
