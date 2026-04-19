package llm

import "strings"

var localModelProviderPriority = []string{"ollama", "lmstudio", "mlx_lm"}

// LocalModelProviderNames returns the lookup priority used when inferring
// which local provider owns a model identifier.
func LocalModelProviderNames() []string {
	names := make([]string, len(localModelProviderPriority))
	copy(names, localModelProviderPriority)
	return names
}

// IsLocalProviderName reports whether the provider name belongs to a built-in local provider.
func IsLocalProviderName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "ollama", "lmstudio", "mlx_lm":
		return true
	default:
		return false
	}
}

// FindLocalProviderByModel returns the first built-in local provider that reports
// the given model as available.
func FindLocalProviderByModel(factory *Factory, model string) string {
	if factory == nil {
		return ""
	}

	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return ""
	}

	candidates := []string{trimmed}
	normalized := strings.ToLower(trimmed)
	if normalized != trimmed {
		candidates = append(candidates, normalized)
	}

	for _, providerName := range localModelProviderPriority {
		provider, err := factory.GetProvider(providerName)
		if err != nil {
			continue
		}

		checker, ok := provider.(ModelPresenceChecker)
		if !ok {
			continue
		}

		for _, candidate := range candidates {
			if checker.HasModel(candidate) {
				return providerName
			}
		}
	}

	return ""
}
