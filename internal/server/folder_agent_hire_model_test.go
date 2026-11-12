package server

import (
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/llm"
)

// The server's setups tell the folder card which model a Home's hires run on.
var _ folderagent.HomeHireModeler = folderAgentSetups{}

func TestFolderAgentSetups_HomeHireModel(t *testing.T) {
	factory := llm.NewFactory()
	factory.Register("claude", hireProvider{modelListProvider{"claude", []string{"claude-sonnet-5-5"}}, llm.ProviderTypeCloud, true})
	cfg := config.NewManager(filepath.Join(t.TempDir(), "settings.json"))
	setups := folderAgentSetups{builder: &ServerBuilder{llmFactory: factory, configManager: cfg}}

	// No system model: the card says "No model yet" (an empty answer here).
	if got := setups.HomeHireModel(); got != "" {
		t.Fatalf("with no system model = %q", got)
	}
	// The label reads as the Hire step's: "<model> (<provider>)".
	if err := cfg.SetSystemModel("claude", "claude-sonnet-5-5"); err != nil {
		t.Fatal(err)
	}
	if got := setups.HomeHireModel(); got != "claude-sonnet-5-5 (claude)" {
		t.Fatalf("label = %q", got)
	}
	// A provider this build has not registered cannot run the hires.
	if err := cfg.SetSystemModel("gemini", "some-model"); err != nil {
		t.Fatal(err)
	}
	if got := setups.HomeHireModel(); got != "" {
		t.Fatalf("an unregistered provider = %q", got)
	}
	// A model set in Settings after the card was drawn shows on the next one:
	// it is read each time, never kept.
	if err := cfg.SetSystemModel("claude", "claude-sonnet-5-5"); err != nil {
		t.Fatal(err)
	}
	if got := setups.HomeHireModel(); got != "claude-sonnet-5-5 (claude)" {
		t.Fatalf("after the model came back = %q", got)
	}
	// No builder and no provider registry answer nothing, never panic.
	if got := (folderAgentSetups{}).HomeHireModel(); got != "" {
		t.Fatalf("no builder = %q", got)
	}
	if got := (folderAgentSetups{builder: &ServerBuilder{configManager: cfg}}).HomeHireModel(); got != "" {
		t.Fatalf("no provider registry = %q", got)
	}
}
