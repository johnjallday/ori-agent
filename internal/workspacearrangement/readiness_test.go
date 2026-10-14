package workspacearrangement

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

type readinessProvider struct {
	plannerProvider
	caps llm.ProviderCapabilities
}

func (p readinessProvider) Capabilities() llm.ProviderCapabilities { return p.caps }

func TestPlanningReadiness_PreservesMetadataOnlyTransportBoundary(t *testing.T) {
	if SupportsMetadataPlanning(nil) {
		t.Fatal("nil provider accepted")
	}
	for _, name := range []string{"openai", "claude", "gemini", "ollama", "lmstudio", "mlx_lm", "codex", "claude_code", "unknown"} {
		for _, tools := range []bool{false, true} {
			for _, native := range []bool{false, true} {
				p := readinessProvider{plannerProvider: plannerProvider{name: name}, caps: llm.ProviderCapabilities{SupportsTools: tools, SupportsNativeMCP: native}}
				want := tools && !native && name != "codex" && name != "claude_code" && name != "unknown"
				if SupportsMetadataPlanning(p) != want {
					t.Fatalf("provider=%s tools=%v native=%v", name, tools, native)
				}
			}
		}
	}
}

func TestPlanningReadiness_ValidGoalMatchesWireLimits(t *testing.T) {
	for _, goal := range []string{"Keep experiments catalog-only", strings.Repeat("é", 1000), "<inert user text>"} {
		if !ValidGoal(goal) {
			t.Fatal("valid goal rejected")
		}
	}
	for _, goal := range []string{"", " ", " padded", "two\nlines", "invisible\u202e", string([]byte{0xff}), strings.Repeat("é", 1001)} {
		if ValidGoal(goal) {
			t.Fatal("invalid goal accepted")
		}
	}
}
