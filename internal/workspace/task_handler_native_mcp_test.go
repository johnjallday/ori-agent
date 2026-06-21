package workspace

import (
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
)

func TestEffectiveNativeMCPExecTimeout(t *testing.T) {
	h := &LLMTaskHandler{}
	if got := h.effectiveNativeMCPExecTimeout(); got != defaultNativeMCPExecTimeout {
		t.Errorf("unset default = %v, want %v", got, defaultNativeMCPExecTimeout)
	}
	h.SetNativeMCPExecTimeout(45 * time.Second)
	if got := h.effectiveNativeMCPExecTimeout(); got != 45*time.Second {
		t.Errorf("override = %v, want 45s", got)
	}
	h.SetNativeMCPExecTimeout(0) // non-positive restores the default
	if got := h.effectiveNativeMCPExecTimeout(); got != defaultNativeMCPExecTimeout {
		t.Errorf("reset = %v, want default", got)
	}
}

func nativeMCPAgent(allow bool) *resolvedTaskAgent {
	return &resolvedTaskAgent{
		Agent: &agent.Agent{
			Settings: types.Settings{AllowNativeMCPTools: &allow},
		},
	}
}

func TestNativeMCPGateAllowed(t *testing.T) {
	cases := []struct {
		name      string
		wsAllow   bool
		nilWS     bool
		agentNil  bool
		agentAllo bool
		want      bool
	}{
		{name: "both opted in", wsAllow: true, agentAllo: true, want: true},
		{name: "workspace off", wsAllow: false, agentAllo: true, want: false},
		{name: "agent off", wsAllow: true, agentAllo: false, want: false},
		{name: "both off", wsAllow: false, agentAllo: false, want: false},
		{name: "nil workspace", nilWS: true, agentAllo: true, want: false},
		{name: "nil agent", wsAllow: true, agentNil: true, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ws *Workspace
			if !tc.nilWS {
				ws = &Workspace{AllowNativeMCPCLI: tc.wsAllow}
			}
			var ag *resolvedTaskAgent
			if !tc.agentNil {
				ag = nativeMCPAgent(tc.agentAllo)
			}
			if got := nativeMCPGateAllowed(ws, ag); got != tc.want {
				t.Errorf("nativeMCPGateAllowed = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsNativeMCPToolsAllowed(t *testing.T) {
	if (types.Settings{}).IsNativeMCPToolsAllowed() {
		t.Error("default (nil) must be false")
	}
	f := false
	if (types.Settings{AllowNativeMCPTools: &f}).IsNativeMCPToolsAllowed() {
		t.Error("explicit false must be false")
	}
	tr := true
	if !(types.Settings{AllowNativeMCPTools: &tr}).IsNativeMCPToolsAllowed() {
		t.Error("explicit true must be true")
	}
}
