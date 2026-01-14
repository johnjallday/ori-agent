package privateservices

import (
	"os"
	"strings"
)

// Capabilities describe which private-service backed features are available.
type Capabilities struct {
	Web3Wallet          bool
	MarketplacePayments bool
	TokenPayouts        bool
}

// Provider exposes which private-service capabilities are available.
type Provider interface {
	Capabilities() Capabilities
}

// EnvProvider reads capability flags from environment variables.
type EnvProvider struct{}

// NewEnvProvider returns a provider backed by environment variables.
func NewEnvProvider() Provider {
	return EnvProvider{}
}

// Capabilities returns the capability set derived from environment variables.
func (EnvProvider) Capabilities() Capabilities {
	return Capabilities{
		Web3Wallet:          envBool("ORI_WEB3_WALLET_ENABLED", true),
		MarketplacePayments: envBool("ORI_MARKETPLACE_PAYMENTS_ENABLED", false),
		TokenPayouts:        envBool("ORI_TOKEN_PAYOUTS_ENABLED", false),
	}
}

// NoopProvider disables all private-service capabilities.
type NoopProvider struct{}

// Capabilities returns an empty capability set.
func (NoopProvider) Capabilities() Capabilities {
	return Capabilities{}
}

func envBool(name string, defaultValue bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue
	}

	switch strings.ToLower(raw) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return defaultValue
	}
}
