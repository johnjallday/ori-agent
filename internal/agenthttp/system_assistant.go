package agenthttp

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

const (
	systemAssistantAgentName       = "Ori"
	systemAssistantAgentLegacyName = "__assistant__"
)

var systemAssistantTags = []string{"system", "orchestrator", "assistant", "utility", "time", "weather", "facts"}

const (
	systemAssistantDescription = "System orchestrator for unmatched everyday requests. Route to specialists when deeper context or tools are needed."
	systemAssistantPrompt      = "You are Ori's system orchestrator assistant. Handle simple utility requests directly and delegate complex work to specialized agents when appropriate. Keep responses concise and actionable."
)

func isSystemAssistantAgent(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), systemAssistantAgentName)
}

// EnsureSystemAssistantAgent ensures the canonical Assistant agent exists.
func EnsureSystemAssistantAgent(st store.Store) error {
	return ensureSystemAssistantAgent(st)
}

// EnsureSystemAssistantAgentWithSystemModel ensures the canonical Assistant
// agent exists and is aligned with the configured system model.
func EnsureSystemAssistantAgentWithSystemModel(st store.Store, systemProvider, systemModel string) error {
	return ensureSystemAssistantAgentWithSystemModel(st, systemProvider, systemModel)
}

func ensureSystemAssistantAgent(st store.Store) error {
	return ensureSystemAssistantAgentWithSystemModel(st, "", "")
}

func ensureSystemAssistantAgentWithSystemModel(st store.Store, systemProvider, systemModel string) error {
	if st == nil {
		return fmt.Errorf("store is required")
	}

	if err := migrateLegacySystemAssistantName(st); err != nil {
		return err
	}

	hasSystemModel := strings.TrimSpace(systemProvider) != "" && strings.TrimSpace(systemModel) != ""

	if _, exists := st.GetAgent(systemAssistantAgentName); !exists {
		cfg := &store.CreateAgentConfig{
			Type:         agent.TypeGeneral,
			SystemPrompt: systemAssistantPrompt,
		}
		if hasSystemModel {
			cfg.LLMProvider = systemProvider
			cfg.Model = systemModel
		}
		if err := st.CreateAgent(systemAssistantAgentName, cfg); err != nil {
			return err
		}
	}

	ag, ok := st.GetAgent(systemAssistantAgentName)
	if !ok || ag == nil {
		return fmt.Errorf("system assistant %q not found after creation", systemAssistantAgentName)
	}

	changed := false

	if ag.Type == "" {
		ag.Type = agent.TypeGeneral
		changed = true
	}
	if ag.Status == "" {
		ag.Status = types.AgentStatusActive
		changed = true
	}
	if strings.TrimSpace(ag.Settings.SystemPrompt) == "" {
		ag.Settings.SystemPrompt = systemAssistantPrompt
		changed = true
	}
	if hasSystemModel {
		if ag.Settings.Provider != systemProvider {
			ag.Settings.Provider = systemProvider
			changed = true
		}
		if ag.Settings.Model != systemModel {
			ag.Settings.Model = systemModel
			changed = true
		}
	}
	if ag.Metadata == nil {
		ag.Metadata = &types.AgentMetadata{}
		changed = true
	}
	if strings.TrimSpace(ag.Metadata.Description) == "" {
		ag.Metadata.Description = systemAssistantDescription
		changed = true
	}

	for _, tag := range systemAssistantTags {
		if !containsNormalized(ag.Metadata.Tags, tag) {
			ag.Metadata.Tags = append(ag.Metadata.Tags, tag)
			changed = true
		}
	}

	if !changed {
		return nil
	}

	return st.SetAgent(systemAssistantAgentName, ag)
}

func migrateLegacySystemAssistantName(st store.Store) error {
	// Nothing to migrate if the current canonical name already exists.
	if _, exists := st.GetAgent(systemAssistantAgentName); exists {
		return nil
	}

	legacyAgent, exists := st.GetAgent(systemAssistantAgentLegacyName)
	if !exists || legacyAgent == nil {
		return nil
	}

	if err := st.SetAgent(systemAssistantAgentName, legacyAgent); err != nil {
		return err
	}

	// Best-effort cleanup of the legacy record.
	_ = st.DeleteAgent(systemAssistantAgentLegacyName)
	return nil
}
