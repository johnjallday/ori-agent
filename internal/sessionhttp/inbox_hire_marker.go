package sessionhttp

import (
	"errors"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// An inbox hire uses the reviewed roster but is an ordinary anchored workspace.
// Record the operation and the actual lead without claiming template origin or
// creating a capability. This is a receipt, not consent to access the folder.
func (h *Handler) recordInboxHire(id string, marker *workspace.AssistantSetupCreation) error {
	if marker == nil || marker.OperationID == "" || marker.FolderReferenceID == "" || h.workspaceTaskStore == nil {
		return errors.New("missing inbox hire operation")
	}
	return h.workspaceTaskStore.Update(id, func(w *workspace.Workspace) error {
		if !w.IsAnchored() || w.AnchorDirectoryReferenceID() != marker.FolderReferenceID || w.HasInstalledCapability(workspace.CapabilityFileJanitor) || w.GetTemplateProvenance() != nil {
			return errors.New("inbox hire changed")
		}
		lead, ok := w.FindUniqueAgentInstance(w.EntryAgentName(), "")
		if !ok || lead.ID == "" {
			return errors.New("inbox hire lead missing")
		}
		digest, err := h.preparedWorkspaceTeamDigest(w)
		if err != nil {
			return err
		}
		m := *marker
		m.InboxHireLeadID = lead.ID
		m.TeamRevision = digest
		w.SetTemplateProvenance(&workspace.TemplateProvenance{AssistantSetup: &m})
		return nil
	})
}
