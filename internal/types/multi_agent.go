package types

// MultiAgentMode controls orchestration routing decisions.
type MultiAgentMode string

const (
	MultiAgentModeAuto  MultiAgentMode = "auto"
	MultiAgentModeForce MultiAgentMode = "force"
	MultiAgentModeOff   MultiAgentMode = "off"
)

// ParseMultiAgentMode validates and normalizes a mode value.
func ParseMultiAgentMode(value string) (MultiAgentMode, bool) {
	switch MultiAgentMode(value) {
	case MultiAgentModeAuto, MultiAgentModeForce, MultiAgentModeOff:
		return MultiAgentMode(value), true
	default:
		return MultiAgentModeAuto, false
	}
}
