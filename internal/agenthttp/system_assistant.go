package agenthttp

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
	"github.com/johnjallday/ori-agent/internal/types"
)

// systemAssistantAgentName is the working agent that routes, plans, and
// executes real work.
//
// It was "Ori", then "Workspace Manager" — a split that deliberately kept the
// navigation guide and the working agent apart. Issue #350 reverses that: users
// had to learn an internal product distinction before deciding where to type, so
// both surfaces are now one identity, "Ask Ori".
//
// The name itself lives in internal/systemassistant, which every layer shares.
const systemAssistantAgentName = systemassistant.CanonicalName

// systemAssistantLegacyNames are previous names for the same agent, migrated
// forward on startup in order. Existing installs have one of these on disk.
var systemAssistantLegacyNames = systemassistant.LegacyNames

var systemAssistantTags = []string{"system", "orchestrator", "assistant", "utility", "time", "weather", "facts"}

const (
	systemAssistantDescription = "System orchestrator for unmatched everyday requests. Route to specialists when deeper context or tools are needed."
	systemAssistantPrompt      = "You are Ori's system orchestrator assistant. Handle simple utility requests directly and delegate complex work to specialized agents when appropriate. Keep responses concise and actionable."
)

// isSystemAssistantAgent guards protected operations (delete, rename, disable,
// bulk edits). It matches the canonical identity only: a retired name is a
// migration concern, and once migration has run a user is free to own an agent
// by that name.
func isSystemAssistantAgent(name string) bool {
	return systemassistant.IsCanonicalName(name)
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

	// Normally the canonical name. In the collision case the assistant stays
	// under its legacy name, and aligning the canonical record instead would
	// silently take over an agent the user created.
	effectiveName, _ := systemAssistantRecordName(st)

	if _, exists := st.GetAgent(effectiveName); !exists {
		cfg := &store.CreateAgentConfig{
			Type:         agent.TypeGeneral,
			Role:         types.RoleOrchestrator,
			SystemPrompt: systemAssistantPrompt,
		}
		if hasSystemModel {
			cfg.LLMProvider = systemProvider
			cfg.Model = systemModel
		}
		if err := st.CreateAgent(effectiveName, cfg); err != nil {
			return err
		}
	}

	ag, ok := st.GetAgent(effectiveName)
	if !ok || ag == nil {
		return fmt.Errorf("system assistant %q not found after creation", effectiveName)
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

	// Stamp the durable marker that positively identifies this record as the
	// protected assistant. Without it, a later migration cannot tell the system
	// record apart from a user-created agent that happens to share the name
	// (FR55) — the name alone stopped being sufficient the moment "Ask Ori"
	// became something a user is allowed to call their own agent.
	if !systemassistant.HasProtectedMarker(ag.Metadata.Tags) {
		ag.Metadata.Tags = systemassistant.EnsureProtectedMarker(ag.Metadata.Tags)
		changed = true
	}

	if !changed {
		return nil
	}

	return st.SetAgent(effectiveName, ag)
}

// findLegacySystemAssistantRecord returns the newest legacy-named record still
// on disk, or "" when none is present. Order comes from the shared contract, so
// an install carrying both a recent and an ancient name migrates the one the
// user has actually been using.
func findLegacySystemAssistantRecord(st store.Store) string {
	for _, legacy := range systemAssistantLegacyNames {
		if ag, exists := st.GetAgent(legacy); exists && ag != nil {
			return legacy
		}
	}
	return ""
}

// systemAssistantRecordName resolves which stored record IS the protected system
// assistant, and reports whether a name collision blocked the migration.
//
// The subtle case is an install where the user already created their own agent
// called "Ask Ori" while the real system assistant is still on disk under a
// legacy name. Adopting the canonical record there would hijack the user's agent
// AND strand the real assistant's settings, so instead the system assistant
// stays under its legacy name and both records remain intact and
// distinguishable — the marker tells them apart (FR55).
func systemAssistantRecordName(st store.Store) (name string, collided bool) {
	legacy := findLegacySystemAssistantRecord(st)

	canonical, canonicalExists := st.GetAgent(systemAssistantAgentName)
	if canonicalExists && canonical != nil {
		alreadyOurs := canonical.Metadata != nil &&
			systemassistant.HasProtectedMarker(canonical.Metadata.Tags)
		if alreadyOurs || legacy == "" {
			return systemAssistantAgentName, false
		}
		return legacy, true
	}

	if legacy != "" {
		return legacy, false
	}
	return systemAssistantAgentName, false
}

// migrateLegacySystemAssistantName moves an existing install's system assistant
// to the current canonical name, preserving its configuration and every file in
// its agent folder.
//
// It is a no-op once migrated, so repeated startups and the settings handler's
// re-run are both safe (FR54).
func migrateLegacySystemAssistantName(st store.Store) error {
	current, collided := systemAssistantRecordName(st)

	if collided {
		// Nothing is moved or deleted. The install keeps working with the
		// assistant under its legacy name; the diagnostic is how the situation
		// stays visible instead of being silently resolved (FR60).
		logger.Warn("System assistant kept under its legacy name: the canonical name is taken by a user-created agent", logger.Fields{
			"legacy_name":    current,
			"canonical_name": systemAssistantAgentName,
			"resolution":     "both records preserved; rename the user-created agent to complete the migration",
		})
		// Still mark it, so the two records stay tellable apart by something
		// more durable than which name each happens to hold (FR55).
		return markSystemAssistantRecord(st, current)
	}

	if current == systemAssistantAgentName {
		return nil
	}

	// Move the record and its whole folder — skills_state.json and the per-agent
	// skills/ tree live there and are not part of the in-memory record, so a
	// SetAgent+DeleteAgent pair would destroy them.
	renamer, ok := st.(store.AgentRenamer)
	if !ok {
		// No move capability (in-memory stores used by tests). Copy forward and
		// deliberately leave the source intact rather than risk losing it.
		legacyAgent, exists := st.GetAgent(current)
		if !exists || legacyAgent == nil {
			return nil
		}
		return st.SetAgent(systemAssistantAgentName, legacyAgent)
	}

	if err := renamer.RenameAgent(current, systemAssistantAgentName); err != nil {
		return fmt.Errorf("migrate system assistant %q -> %q: %w",
			current, systemAssistantAgentName, err)
	}

	// Stamp the marker as part of the move, not later. An install can carry a
	// second, staler legacy record (a "Workspace Manager" plus a long-dead
	// "__assistant__"); until the freshly migrated record is marked, that
	// leftover makes the canonical name look like a user-created collision and
	// the assistant gets resolved back to the stale record.
	if err := markSystemAssistantRecord(st, systemAssistantAgentName); err != nil {
		return err
	}

	logger.Info("System assistant migrated to its canonical identity", logger.Fields{
		"from": current,
		"to":   systemAssistantAgentName,
	})
	return nil
}

// markSystemAssistantRecord stamps the protected marker on a stored record.
func markSystemAssistantRecord(st store.Store, name string) error {
	ag, ok := st.GetAgent(name)
	if !ok || ag == nil {
		return fmt.Errorf("system assistant %q not found after migration", name)
	}
	if ag.Metadata == nil {
		ag.Metadata = &types.AgentMetadata{}
	}
	if systemassistant.HasProtectedMarker(ag.Metadata.Tags) {
		return nil
	}
	ag.Metadata.Tags = systemassistant.EnsureProtectedMarker(ag.Metadata.Tags)
	return st.SetAgent(name, ag)
}
