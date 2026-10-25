package server

import (
	"github.com/johnjallday/ori-agent/internal/foldersetup"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

// validateSetupModel checks a known configuration before effects through the
// runner's existing material-witness guard. It never refreshes an accepted
// plan or replaces its brief/model with newly read facts.
func (h *folderSetupHost) validateSetupModel(req personalassistant.FolderSetupRequest) error {
	expected := req.Plan.Intent.Model
	if expected == nil {
		return nil // legacy/pending blueprint retains its existing recovery
	}
	if expected.Validate() != nil || h == nil || h.builder == nil || h.builder.sessionHandler == nil {
		return personalassistant.ErrFolderPlanChanged
	}
	if req.Plan.Intent.Portfolio {
		provider, model := h.builder.systemModel()
		if expected.Source != "system" || provider != expected.Provider || model != expected.Model {
			return personalassistant.ErrFolderPlanChanged
		}
		return nil
	}
	if expected.Source == "agent" {
		provider, model, found := h.builder.sessionHandler.SavedAgentModel(expected.AgentName)
		if !found || provider != expected.Provider || model != expected.Model {
			return personalassistant.ErrFolderPlanChanged
		}
		return nil
	}
	target, err := h.target(req.Offer)
	if err != nil {
		return personalassistant.ErrFolderPlanChanged
	}
	// This only reads the installed blueprint and applicable local settings;
	// it performs no release lookup or conversational inference.
	_, _, actual := h.pluginHire(req.UserID, target, foldersetup.Plugin{State: personalassistant.FolderInstallReady})
	if actual.State != "configured" || actual.Provider != expected.Provider || actual.Name != expected.Model {
		return personalassistant.ErrFolderPlanChanged
	}
	return nil
}
