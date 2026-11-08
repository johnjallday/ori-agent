package server

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// Every provider method would panic through the nil embedded interface. Roster
// readiness is only registry metadata, not even a model-discovery HTTP call.
type uncallableStaffingProvider struct{ llm.Provider }

func TestWorkspaceStaffingConfigurationNeverCallsProviders(t *testing.T) {
	factory := llm.NewFactory()
	factory.Register("claude", &uncallableStaffingProvider{})
	factory.Register("lmstudio", &uncallableStaffingProvider{})
	for _, tc := range []struct {
		provider, model string
		want            bool
	}{
		{"anthropic", "claude-sonnet-5-5", true}, {"", "claude-sonnet-5-5", true},
		{"lmstudio", "custom-saved-model", true}, {"lmstudio", "", false},
		{"", "custom-saved-model", false}, {"missing", "model", false},
	} {
		if got := workspaceStaffingProfileConfigured(factory, tc.provider, tc.model); got != tc.want {
			t.Fatalf("%q/%q: %v", tc.provider, tc.model, got)
		}
	}
}
