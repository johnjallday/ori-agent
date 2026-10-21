package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/resourcecatalog"
	"github.com/johnjallday/ori-agent/internal/workspacesurface"
)

// ErrResourceCatalogUnavailable means the installed plugin cannot serve its
// catalog now: no declaration, disabled, or its service artifact is missing.
var ErrResourceCatalogUnavailable = errors.New("plugin has no usable resource catalog")

// Which catalog operation to call.
const (
	ResourceCatalogSources   = "sources"
	ResourceCatalogInventory = "inventory"
)

// ResourceCatalogCall calls one of the installed plugin's two declared
// catalog operations as a machine-level read-only call.
func (l *SurfaceLifecycle) ResourceCatalogCall(ctx context.Context, installed InstalledPlugin, which string, input json.RawMessage) (json.RawMessage, error) {
	declaration, err := installed.WorkspaceSurfaces.ResourceCatalogDeclaration()
	if err != nil || declaration == nil || !installed.Enabled {
		return nil, ErrResourceCatalogUnavailable
	}
	operation := declaration.SourcesOperation
	if which == ResourceCatalogInventory {
		operation = declaration.InventoryOperation
	} else if which != ResourceCatalogSources {
		return nil, ErrResourceCatalogUnavailable
	}
	output, err := l.machineReadOnlyCall(ctx, installed, declaration.ServiceID, operation, input)
	if errors.Is(err, errMachineOperationUnavailable) {
		return nil, ErrResourceCatalogUnavailable
	}
	return output, err
}

// declaresResourceWorkspaceFields reports whether the manifest uses anything
// only resource_workspaces_v1 defines. An explicit "project" kind counts too:
// an older host rejects the field itself, so the feature must be required.
func (c *SurfaceContribution) declaresResourceWorkspaceFields() bool {
	if len(strings.TrimSpace(string(c.ResourceCatalog))) > 0 {
		return true
	}
	for _, home := range c.AssistantProgramHomes {
		for _, attachment := range home.AllowedProjectAttachments {
			if strings.TrimSpace(attachment.WorkspaceKind) != "" || strings.TrimSpace(attachment.Label) != "" || strings.TrimSpace(attachment.Description) != "" {
				return true
			}
		}
	}
	return false
}

// ResourceCatalogDeclaration returns the manifest's decoded catalog
// declaration, or nil when it declares none. A contribution that passed
// validation never returns an error here.
func (c *SurfaceContribution) ResourceCatalogDeclaration() (*resourcecatalog.Declaration, error) {
	if c == nil || len(strings.TrimSpace(string(c.ResourceCatalog))) == 0 {
		return nil, nil
	}
	return resourcecatalog.DecodeDeclaration(c.ResourceCatalog)
}

// validateResourceCatalog requires the declaration to name one of this
// contribution's blueprints and two distinct read-only operations of one of
// its services. Whether that blueprint's team is a resource team is checked
// when the blueprints are resolved, because only then is its template read.
func validateResourceCatalog(c *SurfaceContribution, services map[string]*ContributedService) error {
	declaration, err := c.ResourceCatalogDeclaration()
	if err != nil {
		return contributionError(CodeContributionInvalid, "resource_catalog", "declaration", "resource catalog declaration is invalid", err)
	}
	if declaration == nil {
		return nil
	}
	const component = "resource_catalog"
	blueprintFound := false
	for _, blueprint := range c.Blueprints {
		blueprintFound = blueprintFound || blueprint.ID == declaration.BlueprintID
	}
	if !blueprintFound {
		return contributionError(CodeComponentUnknown, component, "blueprint_id", "resource catalog names an unknown blueprint", nil)
	}
	service := services[declaration.ServiceID]
	if service == nil {
		return contributionError(CodeComponentUnknown, component, "service_id", "resource catalog references an unknown service", nil)
	}
	for _, operationID := range []string{declaration.SourcesOperation, declaration.InventoryOperation} {
		var declared *ContributedOperation
		for index := range service.Operations {
			if service.Operations[index].ID == operationID {
				declared = &service.Operations[index]
			}
		}
		if declared == nil {
			return contributionError(CodeComponentUnknown, component, "operation", "resource catalog operation is not declared by that service", nil)
		}
		// A catalog read never changes anything and never needs a grant the
		// host would have to mint for a machine-level call.
		if declared.Policy != string(workspacesurface.PolicyReadOnly) || len(declared.Scopes) != 0 {
			return contributionError(CodeOperationPolicyInvalid, component, "operation", "resource catalog operations must be read_only without scopes", nil)
		}
	}
	return nil
}
