package server

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// hireProvider is a registered provider with a model list, a kind and tool
// support of its own.
type hireProvider struct {
	modelListProvider
	kind  llm.ProviderType
	tools bool
}

func (p hireProvider) Type() llm.ProviderType { return p.kind }
func (p hireProvider) Capabilities() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{SupportsTools: p.tools}
}

func TestFolderAgentHiring_OffersRunnableToolModelsOnly(t *testing.T) {
	factory := llm.NewFactory()
	factory.Register("claude", hireProvider{modelListProvider{"claude", []string{"claude-sonnet-5-5", "claude-sonnet-4-5-20250929"}}, llm.ProviderTypeCloud, true})
	factory.Register("ollama", hireProvider{modelListProvider{"ollama", []string{"llama"}}, llm.ProviderTypeLocal, true})
	factory.Register("plain", hireProvider{modelListProvider{"plain", []string{"no-tools"}}, llm.ProviderTypeCloud, false})
	models := folderAgentHiring{builder: &ServerBuilder{llmFactory: factory}}.Models(context.Background())
	if len(models) != 1 || models[0].Provider != "claude" || models[0].Model != "claude-sonnet-5-5" || models[0].Default {
		t.Fatalf("models = %+v", models)
	}
}

func TestFolderAgentHiring_NeverOffersTheAssistantsOwnProfile(t *testing.T) {
	st, err := store.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Atlas", "Journal"} {
		if err := st.CreateAgent(name, &store.CreateAgentConfig{Role: types.RoleOrchestrator}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpdateAgent("Atlas", func(ag *agent.Agent) error {
		ag.Metadata = &types.AgentMetadata{Tags: []string{personalassistant.ProfileAssistantMarker("pa-1")}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	hiring := folderAgentHiring{builder: &ServerBuilder{st: st}}
	agents := hiring.Agents(context.Background(), "u1")
	if len(agents) != 1 || agents[0].Name != "Journal" {
		t.Fatalf("agents = %+v", agents)
	}
	if !hiring.AgentExists("journal") || !hiring.AgentExists("Atlas") || hiring.AgentExists("Nobody") {
		t.Fatal("AgentExists must see every agent, case-insensitively")
	}
}
