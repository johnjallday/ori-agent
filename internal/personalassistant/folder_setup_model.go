package personalassistant

import "fmt"

// FolderSetupModelIntent is a material configuration witness, separate from the
// optional display projection. It is never an API input or a provider health
// claim. Known model changes use the existing changed-plan refusal.
type FolderSetupModelIntent struct {
	Source    string `json:"source"` // system, agent, template
	AgentName string `json:"agent_name,omitempty"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
}

func (m FolderSetupModelIntent) Validate() error {
	if m.Source != "system" && m.Source != "agent" && m.Source != "template" {
		return fmt.Errorf("%w: model configuration source", errFolderDigestInvalid)
	}
	if m.Provider == "" || m.Model == "" || (m.Source == "agent" && m.AgentName == "") ||
		!visualText(m.Provider, 80) || !visualText(m.Model, 120) || !visualText(m.AgentName, 120) {
		return fmt.Errorf("%w: model configuration witness", errFolderDigestInvalid)
	}
	return nil
}
