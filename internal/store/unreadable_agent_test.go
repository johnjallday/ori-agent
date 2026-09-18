package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
)

const brokenDefinition = `{"Settings": {"model": "gpt-4o-mini",` // truncated mid-edit

func TestAnUnreadableDefinitionIsListedAsSuchAndNeverTouched(t *testing.T) {
	f := newCompositeFixture(t)
	dir := filepath.Join(f.root, "Agents", "Broken")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "agent_settings.json")
	if err := os.WriteFile(path, []byte(brokenDefinition), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	backdate(t, path)

	c := f.open(t)
	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if names := c.ListAgents(); len(names) != 0 {
		t.Errorf("ListAgents = %v; an unreadable agent is not a usable roster entry", names)
	}
	unreadable := c.UnreadableAgents()
	if len(unreadable) != 1 || unreadable[0].Name != "Broken" || unreadable[0].File != path || unreadable[0].Error == "" {
		t.Fatalf("UnreadableAgents = %+v", unreadable)
	}

	for op, err := range map[string]error{
		"SetAgent":    c.SetAgent("Broken", &agent.Agent{}),
		"CreateAgent": c.CreateAgent("Broken", &CreateAgentConfig{}),
		"UpdateAgent": c.UpdateAgent("Broken", func(*agent.Agent) error { return nil }),
		"DeleteAgent": c.DeleteAgent("Broken"),
	} {
		var unreadableErr *UnreadableAgentError
		if !errors.Is(err, ErrAgentUnreadable) || !errors.As(err, &unreadableErr) || unreadableErr.File != path {
			t.Errorf("%s: err = %v, want ErrAgentUnreadable naming %s", op, err, path)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the unreadable file is gone: %v", err)
	}
	if string(data) != brokenDefinition {
		t.Errorf("the unreadable file was rewritten: %q", data)
	}
	assertUntouched(t, path)

	// Fixed in a text editor, it loads on the next rescan.
	if err := os.WriteFile(path, []byte(`{"Settings":{"model":"gpt-4o-mini"}}`), 0o600); err != nil {
		t.Fatalf("fix: %v", err)
	}
	c.ReloadRoot()
	if _, ok := c.GetAgent("Broken"); !ok || len(c.UnreadableAgents()) != 0 {
		t.Errorf("after fixing and rescanning: loaded %v, unreadable %+v", ok, c.UnreadableAgents())
	}
}

func TestAnUnreadableDefinitionInTheDataDirIsNotOverwrittenAtStartup(t *testing.T) {
	dir := t.TempDir()
	path := writeLegacyAgent(t, dir, "Broken", brokenDefinition)
	fs := writesStore(t, dir)
	if _, ok := fs.GetAgent("Broken"); ok {
		t.Error("an unreadable definition was loaded as an agent")
	}
	if data, _ := os.ReadFile(path); string(data) != brokenDefinition {
		t.Errorf("startup rewrote the unreadable file: %q", data)
	}
}
