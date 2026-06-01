package featureflags

import (
	"os"
	"strings"
)

const envEvolutionEnabled = "ORI_EVOLUTION_ENABLED"
const envWorkspaceFloatingAssistantEnabled = "ORI_WORKSPACE_FLOATING_ASSISTANT_ENABLED"

// EvolutionEnabled reports whether evolution features should be active.
// Defaults to true unless explicitly disabled.
func EvolutionEnabled() bool {
	return parseBoolDefaultTrue(os.Getenv(envEvolutionEnabled))
}

// WorkspaceFloatingAssistantEnabled reports whether workspace detail uses the
// consolidated floating assistant instead of the legacy inline bar.
// Defaults to true unless explicitly disabled.
func WorkspaceFloatingAssistantEnabled() bool {
	return parseBoolDefaultTrue(os.Getenv(envWorkspaceFloatingAssistantEnabled))
}

func parseBoolDefaultTrue(raw string) bool {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "1", "true", "yes", "on", "enabled":
		return true
	case "0", "false", "no", "off", "disabled":
		return false
	default:
		return true
	}
}
