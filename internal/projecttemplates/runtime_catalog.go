package projecttemplates

import (
	"slices"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

// RuntimeCatalog is the read-only trust boundary used while normalizing a
// template. Template JSON can reference known IDs, but can never provide an
// implementation, command, endpoint, module, or path grant.
type RuntimeCatalog interface {
	HasCapability(id string) bool
	HasRuntimeAdapter(id string) bool
}

type builtinRuntimeCatalog struct {
	capabilities *workspacecapability.Registry
}

func (c builtinRuntimeCatalog) HasCapability(id string) bool {
	return c.capabilities != nil && c.capabilities.Has(id)
}

func (builtinRuntimeCatalog) HasRuntimeAdapter(id string) bool {
	return slices.Contains(ValidRuntimeRequirementAdapters, strings.ToLower(strings.TrimSpace(id)))
}

func defaultRuntimeCatalog() RuntimeCatalog {
	registry, err := workspacecapability.NewBuiltinRegistry()
	if err != nil {
		return builtinRuntimeCatalog{}
	}
	return builtinRuntimeCatalog{capabilities: registry}
}
