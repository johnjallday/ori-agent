package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

// These tests pin the split between an agent's definition (what the user
// authored, in agent_settings.json) and its runtime state (status, statistics,
// evolution, under <data dir>/agent_state/). Using an agent must never rewrite
// the definition file.

func stateFilePath(fs *fileStore, name string) string {
	return fs.stateStore().path(name)
}

func definitionKeys(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return keys
}

func TestDefinitionFileHoldsOnlyTheDefinition(t *testing.T) {
	dir := t.TempDir()
	fs := writesStore(t, dir)
	if err := fs.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "scout"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	keys := definitionKeys(t, definitionPath(dir, "Scout"))
	for _, runtimeKey := range []string{"status", "statistics", "evolution", "paused"} {
		if _, has := keys[runtimeKey]; has {
			t.Errorf("agent_settings.json holds %q", runtimeKey)
		}
	}
	if _, err := os.Stat(stateFilePath(fs, "Scout")); err != nil {
		t.Errorf("no runtime state file was written: %v", err)
	}
	if !strings.HasPrefix(stateFilePath(fs, "Scout"), filepath.Join(dir, "agent_state")+string(filepath.Separator)) {
		t.Errorf("runtime state is not under the data dir's agent_state: %s", stateFilePath(fs, "Scout"))
	}
}

func TestStatisticsUpdateLeavesTheDefinitionByteIdentical(t *testing.T) {
	dir := t.TempDir()
	fs := writesStore(t, dir)
	if err := fs.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	definition := definitionPath(dir, "Scout")
	before, err := os.ReadFile(definition)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	backdate(t, definition)
	stateBefore, _ := os.ReadFile(stateFilePath(fs, "Scout"))

	// The evolution service records a chat turn exactly like this.
	if err := fs.UpdateAgent("Scout", func(ag *agent.Agent) error {
		ag.Statistics.MessageCount += 3
		ag.Evolution.Experience += 10
		ag.Status = types.AgentStatusActive
		return nil
	}); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}
	// So does the chat handler, through SetAgent with a copy.
	ag, _ := fs.GetAgent("Scout")
	ag.Statistics.MessageCount++
	if err := fs.SetAgent("Scout", ag); err != nil {
		t.Fatalf("SetAgent: %v", err)
	}

	after, err := os.ReadFile(definition)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("a statistics update changed agent_settings.json:\n%s", after)
	}
	assertUntouched(t, definition)
	stateAfter, _ := os.ReadFile(stateFilePath(fs, "Scout"))
	if bytes.Equal(stateBefore, stateAfter) {
		t.Error("the runtime state file did not change")
	}

	reopened := writesStore(t, dir)
	got, _ := reopened.GetAgent("Scout")
	if got.Statistics.MessageCount != 4 || got.Evolution.Experience != 10 || got.Status != types.AgentStatusActive {
		t.Errorf("runtime state did not survive a restart: status %q messages %d xp %d",
			got.Status, got.Statistics.MessageCount, got.Evolution.Experience)
	}
}

func TestStateOnlyWriteIsNotBlockedByAnEditOnDisk(t *testing.T) {
	dir := t.TempDir()
	fs := writesStore(t, dir)
	if err := fs.CreateAgent("Scout", &CreateAgentConfig{SystemPrompt: "original"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	stale, _ := fs.GetAgent("Scout")
	definition := definitionPath(dir, "Scout")
	editOnDisk(t, definition, setEditedPrompt)
	edited, err := os.ReadFile(definition)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	stale.Statistics.MessageCount = 9
	if err := fs.SetAgent("Scout", stale); err != nil {
		t.Fatalf("a statistics-only SetAgent was refused: %v", err)
	}

	after, err := os.ReadFile(definition)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if !bytes.Equal(edited, after) {
		t.Error("a statistics-only write touched a definition edited on disk")
	}
	got, _ := fs.GetAgent("Scout")
	if got.Settings.SystemPrompt != "edited in a text editor" {
		t.Errorf("the edit on disk was not reloaded: prompt %q", got.Settings.SystemPrompt)
	}
	if got.Statistics.MessageCount != 9 {
		t.Errorf("the statistics were lost: %d", got.Statistics.MessageCount)
	}
	state, ok, err := fs.stateStore().Load("Scout")
	if err != nil || !ok || state.Statistics.MessageCount != 9 {
		t.Errorf("the state file was not written: ok %v err %v %+v", ok, err, state)
	}
}

func TestLegacyDefinitionWithStateKeysLoadsAndMovesItsState(t *testing.T) {
	dir := t.TempDir()
	legacy := writeLegacyAgent(t, dir, "Veteran", `{
		"role":"general",
		"Settings":{"model":"gpt-4o-mini","system_prompt":"old hand"},
		"status":"active",
		"statistics":{"message_count":41,"token_usage":900},
		"evolution":{"level":2,"experience":250}
	}`)
	if err := os.WriteFile(filepath.Join(dir, "agents.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("seed index: %v", err)
	}

	fs := writesStore(t, dir)
	got, ok := fs.GetAgent("Veteran")
	if !ok {
		t.Fatal("legacy agent did not load")
	}
	if got.Status != types.AgentStatusActive || got.Statistics.MessageCount != 41 || got.Evolution.Experience != 250 {
		t.Fatalf("legacy state was not read as the initial state: status %q messages %d xp %d",
			got.Status, got.Statistics.MessageCount, got.Evolution.Experience)
	}

	// The state now lives in the state store, and the definition no longer
	// carries it: nothing was lost moving it.
	state, ok, err := fs.stateStore().Load("Veteran")
	if err != nil || !ok || state.Statistics.MessageCount != 41 || state.Evolution.Experience != 250 {
		t.Fatalf("legacy state did not reach the state store: ok %v err %v %+v", ok, err, state)
	}
	keys := definitionKeys(t, legacy)
	for _, runtimeKey := range []string{"status", "statistics", "evolution"} {
		if _, has := keys[runtimeKey]; has {
			t.Errorf("the rewritten definition still holds %q", runtimeKey)
		}
	}

	reopened := writesStore(t, dir)
	again, _ := reopened.GetAgent("Veteran")
	if again.Statistics.MessageCount != 41 || again.Settings.SystemPrompt != "old hand" {
		t.Errorf("after restart: messages %d prompt %q", again.Statistics.MessageCount, again.Settings.SystemPrompt)
	}
}

func TestPauseIsStoredInTheDefinitionAndSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	fs := writesStore(t, dir)
	if err := fs.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	ag, _ := fs.GetAgent("Scout")
	ag.Status = types.AgentStatusDisabled
	if err := fs.SetAgent("Scout", ag); err != nil {
		t.Fatalf("pause: %v", err)
	}

	keys := definitionKeys(t, definitionPath(dir, "Scout"))
	if string(keys["paused"]) != "true" {
		t.Errorf(`agent_settings.json should hold "paused": true, got keys %v`, keys)
	}
	state, err := os.ReadFile(stateFilePath(fs, "Scout"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if strings.Contains(string(state), "disabled") {
		t.Errorf("the state file stores disabled: %s", state)
	}

	reopened := writesStore(t, dir)
	got, _ := reopened.GetAgent("Scout")
	if got.Status != types.AgentStatusDisabled {
		t.Fatalf("pause did not survive a restart: status %q", got.Status)
	}

	got.Status = types.AgentStatusActive
	if err := reopened.SetAgent("Scout", got); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if _, has := definitionKeys(t, definitionPath(dir, "Scout"))["paused"]; has {
		t.Error(`resuming must drop the "paused" key`)
	}
	final := writesStore(t, dir)
	if resumed, _ := final.GetAgent("Scout"); resumed.Status != types.AgentStatusActive {
		t.Errorf("resume did not survive a restart: status %q", resumed.Status)
	}
}

func TestLegacyDisabledStatusBecomesPaused(t *testing.T) {
	dir := t.TempDir()
	legacy := writeLegacyAgent(t, dir, "Sleeper", `{"Settings":{"model":"gpt-4o-mini"},"status":"disabled"}`)
	fs := writesStore(t, dir)
	got, _ := fs.GetAgent("Sleeper")
	if got.Status != types.AgentStatusDisabled {
		t.Fatalf("a legacy disabled agent must stay paused, got %q", got.Status)
	}
	if string(definitionKeys(t, legacy)["paused"]) != "true" {
		t.Error(`a legacy "status": "disabled" must become "paused": true`)
	}
}

func TestRenameMovesRuntimeStateAndDeleteRemovesIt(t *testing.T) {
	dir := t.TempDir()
	fs := writesStore(t, dir)
	if err := fs.CreateAgent("Scout", &CreateAgentConfig{}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := fs.UpdateAgent("Scout", func(ag *agent.Agent) error {
		ag.Statistics.MessageCount = 12
		return nil
	}); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}
	if err := fs.RenameAgent("Scout", "Ranger"); err != nil {
		t.Fatalf("RenameAgent: %v", err)
	}
	if _, err := os.Stat(stateFilePath(fs, "Scout")); !os.IsNotExist(err) {
		t.Errorf("state stayed under the old name (err=%v)", err)
	}
	state, ok, err := fs.stateStore().Load("Ranger")
	if err != nil || !ok || state.Statistics.MessageCount != 12 {
		t.Fatalf("state did not follow the rename: ok %v err %v %+v", ok, err, state)
	}

	if err := fs.DeleteAgent("Ranger"); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
	if _, err := os.Stat(stateFilePath(fs, "Ranger")); !os.IsNotExist(err) {
		t.Errorf("state survived the delete (err=%v)", err)
	}
}
