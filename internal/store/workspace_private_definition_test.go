package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

func TestWorkspacePrivateDefinitionDoesNotBecomeGlobalAuthority(t *testing.T) {
	yes := true
	source := &agent.Agent{Role: types.RoleOrchestrator, WorkspaceLocalConfigID: "synthetic-local-slot",
		Settings: types.Settings{APIKey: "synthetic-scoped-key", Model: "offline", SystemPrompt: "Authored definition",
			AllowWebSearch: &yes, AllowNativeMCPTools: &yes, FallbackAllowCloud: &yes}}
	// A misrouted global save refuses before filesystem or map mutation.
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("ORI_DATA_DIR", filepath.Join(root, "data"))
	global := &fileStore{path: filepath.Join(root, "agents.json"), dir: filepath.Join(root, "agents")}
	if err := global.SetAgent("Guide", source); !errors.Is(err, ErrWorkspaceOwnedAgent) {
		t.Fatalf("global save accepted a workspace-local configuration: %v", err)
	}
	if _, err := encodeDefinition(source); !errors.Is(err, ErrWorkspaceOwnedAgent) {
		t.Fatal("alternate global serializer accepted scoped private settings", err)
	}
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := global.decodeAgentUnlocked("Guide", data); !errors.Is(err, ErrWorkspaceOwnedAgent) {
		t.Fatal("global definition loader accepted a copied workspace slot", err)
	}
	promoted := copyDefinition(source)
	if promoted.WorkspaceLocalConfigID != "" || promoted.Settings.APIKey != "" || promoted.Settings.IsWebSearchAllowed() ||
		promoted.Settings.IsNativeMCPToolsAllowed() || promoted.Settings.FallbackAllowCloud == nil || *promoted.Settings.FallbackAllowCloud {
		t.Fatal("explicit definition promotion inherited workspace authority")
	}
	if promoted.Settings.Model != source.Settings.Model || promoted.Settings.SystemPrompt != source.Settings.SystemPrompt ||
		source.Settings.APIKey != "synthetic-scoped-key" || !source.Settings.IsWebSearchAllowed() {
		t.Fatal("promotion cleared source credentials or lost authored choices")
	}
}
