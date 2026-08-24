package plugin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type contributionRuntimeCatalog struct {
	capabilities map[string]struct{}
	adapters     map[string]struct{}
}

func (c contributionRuntimeCatalog) HasCapability(id string) bool {
	_, ok := c.capabilities[workspace.NormalizeCapabilityID(id)]
	return ok
}

func (c contributionRuntimeCatalog) HasRuntimeAdapter(id string) bool {
	_, ok := c.adapters[workspace.NormalizeRuntimeAdapterID(id)]
	return ok
}

// ResolvePluginBlueprints validates every descriptor and skeleton before the
// installed-plugin record can publish it to the creation catalog.
func ResolvePluginBlueprints(descriptor PluginDescriptor) ([]ResolvedBlueprint, error) {
	contribution := descriptor.WorkspaceSurfaces
	if contribution == nil || len(contribution.Blueprints) == 0 {
		return nil, nil
	}
	catalog := contributionRuntimeCatalog{
		capabilities: make(map[string]struct{}, len(contribution.Capabilities)),
		adapters:     make(map[string]struct{}),
	}
	pluginID := workspace.NormalizeCapabilityID(descriptor.Name)
	for _, capability := range contribution.Capabilities {
		catalog.capabilities[workspace.NormalizeCapabilityID(capability.ID)] = struct{}{}
		if capability.RuntimeProvider != nil {
			catalog.adapters["plugin:"+pluginID+":"+capability.RuntimeProvider.ID] = struct{}{}
		}
	}

	resolved := make([]ResolvedBlueprint, 0, len(contribution.Blueprints))
	for _, blueprint := range contribution.Blueprints {
		manifestPath, err := containedPluginComponent(descriptor.InstallDir, blueprint.Manifest, false)
		if err != nil {
			return nil, fmt.Errorf("plugin blueprint %q manifest: %w", blueprint.ID, err)
		}
		skeletonRoot, err := containedPluginComponent(descriptor.InstallDir, blueprint.Skeleton, true)
		if err != nil {
			return nil, fmt.Errorf("plugin blueprint %q skeleton: %w", blueprint.ID, err)
		}
		template, digest, err := projecttemplates.LoadPluginBlueprint(manifestPath, skeletonRoot, catalog)
		if err != nil {
			return nil, fmt.Errorf("plugin blueprint %q: %w", blueprint.ID, err)
		}
		if !sameCapabilityIDs(template.Capabilities, blueprint.Capabilities) {
			return nil, fmt.Errorf("plugin blueprint %q capability projection does not match its contribution descriptor", blueprint.ID)
		}
		owner := &workspace.PluginTemplateOwner{
			PluginID: descriptor.Name, PluginVersion: descriptor.Version,
			BlueprintID: blueprint.ID, BlueprintVersion: blueprint.Version,
		}
		template.ID = "plugin:" + pluginID + ":" + blueprint.ID
		template.PluginOwner = owner
		resolved = append(resolved, ResolvedBlueprint{
			ID: blueprint.ID, QualifiedID: template.ID, Version: blueprint.Version,
			Template: template, SkeletonRoot: skeletonRoot, SkeletonDigest: digest,
		})
	}
	return resolved, nil
}

func containedPluginComponent(root, relative string, wantDirectory bool) (string, error) {
	if !filepath.IsAbs(root) || !safeContributionPath(relative) {
		return "", errors.New("path is invalid")
	}
	root = filepath.Clean(root)
	rootInfo, err := os.Lstat(root) // #nosec G304 -- trusted install root selected by plugin resolution
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("plugin root is unsafe")
	}
	current := root
	for _, part := range strings.Split(filepath.FromSlash(relative), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current) // #nosec G304 -- each validated relative component remains under root
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("path contains a symlink or missing component")
		}
	}
	info, err := os.Lstat(current) // #nosec G304 -- exact contained component
	if err != nil || (wantDirectory && !info.IsDir()) || (!wantDirectory && !info.Mode().IsRegular()) {
		return "", errors.New("component has the wrong file type")
	}
	return current, nil
}

func sameCapabilityIDs(installs []projecttemplates.CapabilityInstall, declared []string) bool {
	fromTemplate := make([]string, 0, len(installs))
	for _, install := range installs {
		fromTemplate = append(fromTemplate, workspace.NormalizeCapabilityID(install.ID))
	}
	fromContribution := make([]string, 0, len(declared))
	for _, id := range declared {
		fromContribution = append(fromContribution, workspace.NormalizeCapabilityID(id))
	}
	sort.Strings(fromTemplate)
	sort.Strings(fromContribution)
	return strings.Join(fromTemplate, "\x00") == strings.Join(fromContribution, "\x00")
}
