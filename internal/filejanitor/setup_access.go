package filejanitor

import (
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Only permission-bearing workspace state joins the pre-grant revision. The
// expected grant changes it, so continuation accepts either that exact original
// snapshot or the canonical resources this operation already established.
func setupAccessRevision(ws *workspace.Workspace) (string, error) {
	install, _ := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
	return setupDigest([]any{ws.MCPBindings, install.OwnedResources})
}

func hasJanitorAccessResources(ws *workspace.Workspace) bool {
	install, _ := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
	for _, resource := range install.OwnedResources {
		if resource.Kind == workspace.ResourceMCPBinding || resource.Kind == workspace.ResourceDirectoryReference {
			return true
		}
	}
	return false
}

func preparationAccessMatches(ws *workspace.Workspace, preparation *SetupPreparation, root string) bool {
	if ws == nil || preparation == nil {
		return false
	}
	if !hasJanitorAccessResources(ws) {
		revision, err := setupAccessRevision(ws)
		return err == nil && preparation.AccessRevision != "" && revision == preparation.AccessRevision
	}
	ref, err := ws.GetDirectoryReference(preparation.ReferenceID)
	if err != nil || ref == nil || ref.Purpose != "" || filepath.Clean(ref.Path) != filepath.Clean(root) {
		return false
	}
	install, _ := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
	owns := func(kind, id string) bool {
		for _, resource := range install.OwnedResources {
			if resource.Kind == kind && resource.ID == id {
				return true
			}
		}
		return false
	}
	if !owns(workspace.ResourceDirectoryReference, preparation.ReferenceID) {
		return false
	}
	expectedConfig, err := setupDigest(map[string]any{"roots": []string{filepath.Clean(root)}})
	if err != nil {
		return false
	}
	for _, binding := range ws.MCPBindings {
		if !strings.EqualFold(strings.TrimSpace(binding.Alias), LegacyBindingAlias) {
			continue
		}
		if !binding.Enabled || !strings.EqualFold(binding.ServerName, filesystemServerName) || binding.DefaultSideEffect != workspace.SideEffectRead ||
			!owns(workspace.ResourceMCPBinding, binding.ID) || len(binding.AllowedTools) != len(JanitorReadTools) || !bindingToolsAreReadOnly(binding) {
			return false
		}
		for _, required := range JanitorReadTools {
			found := false
			for _, actual := range binding.AllowedTools {
				if strings.EqualFold(strings.TrimSpace(actual), required) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		actualConfig, err := setupDigest(binding.Config)
		return err == nil && actualConfig == expectedConfig
	}
	return false
}
