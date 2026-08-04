package agenthttp

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

const (
	// systemAssistantAgentName is the working agent that routes, plans, and
	// executes real work from Home.
	//
	// It was called "Ori" until the cozy-character-experience feature, which
	// reserves that name for the app's setup-and-navigation guide. Keeping both
	// would have put a working agent and a navigation-only guide under one name
	// — exactly the distinction the guide exists to make (PRD FR-28/FR-81).
	systemAssistantAgentName = "Workspace Manager"
)

// systemAssistantLegacyNames are previous names for the same agent, migrated
// forward on startup in order. Existing installs have one of these on disk.
var systemAssistantLegacyNames = []string{"Ori", "__assistant__"}

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
			Role:         types.RoleOrchestrator,
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
	// The system assistant is the primary orchestrator; keep its role aligned
	// with that purpose so orchestration routing and the UI badge agree with
	// the "System orchestrator" description.
	if ag.Role != types.RoleOrchestrator {
		ag.Role = types.RoleOrchestrator
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

// migrateLegacySystemAssistantName moves an existing install's system assistant
// to the current canonical name, preserving its configuration.
//
// If the canonical name is already taken it does nothing — including when the
// user happens to have their own agent by that name, which must not be
// overwritten. In that case the legacy record is left alone rather than
// destroyed, so nothing is lost and the situation stays visible.
func migrateLegacySystemAssistantName(st store.Store) error {
	if _, exists := st.GetAgent(systemAssistantAgentName); exists {
		return nil
	}

	for _, legacy := range systemAssistantLegacyNames {
		legacyAgent, exists := st.GetAgent(legacy)
		if !exists || legacyAgent == nil {
			continue
		}
		if err := st.SetAgent(systemAssistantAgentName, legacyAgent); err != nil {
			return err
		}
		// Best-effort cleanup of the legacy record.
		_ = st.DeleteAgent(legacy)
		return nil
	}
	return nil
}
