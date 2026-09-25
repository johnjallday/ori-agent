package server

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/personalassistanthttp"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
)

// folderHomeProviderSetup uses the same host-reviewed release resolver as
// blueprint recovery. A request can name only a confirmed offer, not a source
// URL or plugin ID. Preview and install both re-verify the release.
type folderHomeProviderSetup struct{ builder *ServerBuilder }

func (s folderHomeProviderSetup) Preview(ctx context.Context, key string) (personalassistanthttp.FolderHomeProviderPreview, error) {
	b := s.builder
	if b == nil || b.server == nil || b.server.reviewedReleases == nil || b.pluginHandler == nil {
		return personalassistanthttp.FolderHomeProviderPreview{}, personalassistant.ErrFolderOutcomeUnavailable
	}
	var provider *reviewedintegration.HomeProvider
	for _, entry := range reviewedintegration.HomeProviders() {
		if entry.Key == key {
			copy := entry
			provider = &copy
			break
		}
	}
	if provider == nil {
		return personalassistanthttp.FolderHomeProviderPreview{}, personalassistant.ErrFolderOutcomeUnavailable
	}
	result := personalassistanthttp.FolderHomeProviderPreview{
		PluginID: provider.PluginID,
	}
	release, ok, err := b.server.reviewedReleases.homeProviderInstall(ctx, *provider)
	if err != nil || !ok {
		return personalassistanthttp.FolderHomeProviderPreview{}, personalassistant.ErrFolderOutcomeUnavailable
	}
	result.Version, result.Source, result.Disclosure = release.version, release.source, release.report
	installed, err := b.pluginHandler.Manager().List()
	if err != nil {
		return personalassistanthttp.FolderHomeProviderPreview{}, personalassistant.ErrFolderOutcomeUnavailable
	}
	for _, entry := range installed {
		if entry.Name != provider.PluginID {
			continue
		}
		// A plugin with this name alone is not evidence of the reviewed pin.
		// Never enable an unrelated installation on a folder-card click.
		if !reviewedHomeProviderInstalled(entry, release, provider.SourceFormat) {
			return personalassistanthttp.FolderHomeProviderPreview{}, personalassistant.ErrFolderOutcomeUnavailable
		}
		result.Installed, result.Ready = true, entry.Enabled
		return result, nil
	}
	return result, nil
}

func reviewedHomeProviderInstalled(entry plugin.InstalledPlugin, release verifiedRelease, format plugin.SourceFormat) bool {
	return entry.Source == release.source && entry.Version == release.version && entry.Format == format && release.source != "" && release.version != ""
}

func (s folderHomeProviderSetup) Install(ctx context.Context, key, reviewedVersion string) (personalassistanthttp.FolderHomeProviderPreview, error) {
	preview, err := s.Preview(ctx, key)
	if err != nil || preview.Ready {
		return preview, err
	}
	if preview.Installed {
		if reviewedVersion != preview.Version {
			return personalassistanthttp.FolderHomeProviderPreview{}, personalassistant.ErrFolderWorkspaceRefused
		}
		if err := s.builder.pluginHandler.Manager().SetEnabled(preview.PluginID, true); err != nil {
			return personalassistanthttp.FolderHomeProviderPreview{}, personalassistant.ErrFolderOutcomeUnavailable
		}
		preview.Ready = true
		return preview, nil
	}
	if preview.Version == "" || preview.Version != reviewedVersion || preview.Source == "" {
		return personalassistanthttp.FolderHomeProviderPreview{}, personalassistant.ErrFolderWorkspaceRefused
	}
	manager := s.builder.pluginHandler.Manager()
	var sourceFormat plugin.SourceFormat
	for _, entry := range reviewedintegration.HomeProviders() {
		if entry.Key == key {
			sourceFormat = entry.SourceFormat
			break
		}
	}
	installed, err := manager.Install(preview.Source, sourceFormat, func(report plugin.TrustReport) bool {
		return report.Name == preview.PluginID && report.Format == sourceFormat
	})
	if err != nil || installed.Name != preview.PluginID {
		return personalassistanthttp.FolderHomeProviderPreview{}, personalassistant.ErrFolderOutcomeUnavailable
	}
	if err := manager.SetEnabled(preview.PluginID, true); err != nil {
		return personalassistanthttp.FolderHomeProviderPreview{}, personalassistant.ErrFolderOutcomeUnavailable
	}
	verified, err := s.Preview(ctx, key)
	if err != nil || !verified.Ready {
		return personalassistanthttp.FolderHomeProviderPreview{}, errors.New("reviewed Home provider was not ready after installation")
	}
	return verified, nil
}
