package featureflags

import (
	"os"
	"strings"
)

const envEvolutionEnabled = "ORI_EVOLUTION_ENABLED"

const envEconomyEnabled = "ORI_ECONOMY_ENABLED"

// EvolutionEnabled reports whether evolution features should be active.
// Defaults to true unless explicitly disabled.
func EvolutionEnabled() bool {
	return parseBoolDefaultTrue(os.Getenv(envEvolutionEnabled))
}

// EconomyEnabled reports whether the City Economy should be active: the HUD,
// the Farm badges and harvest piles, the price line, and every ledger write.
// Defaults to true unless explicitly disabled (city-economy FR47).
//
// With it off the economy endpoints answer 404 and the price check always
// passes, so a save that would have cost Craft simply succeeds. Turning it back
// on restores every balance, because the ledger was only ever paused, not
// cleared.
func EconomyEnabled() bool {
	return parseBoolDefaultTrue(os.Getenv(envEconomyEnabled))
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
