package server

import (
	"context"
	"errors"
	"os"

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
	if source := os.Getenv("ORI_REVIEWED_HOME_PROVIDER_DEV_SOURCE"); source != "" {
		installed, err := b.pluginHandler.Manager().List()
		if err != nil {
			return result, personalassistant.ErrFolderOutcomeUnavailable
		}
		for _, entry := range installed {
			if folderHomeDevelopmentReady(entry, *provider, source) {
				result.Installed, result.Ready, result.DevelopmentCopy, result.Version = true, true, true, entry.Version
				return result, nil
			}
		}
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
		switch classifyInstalledHomeProvider(entry, release, *provider) {
		case installedHomeProviderCurrent:
			result.Installed, result.Ready = true, entry.Enabled
		case installedHomeProviderOlder:
			// An older reviewed release is updated to this one on confirm. The
			// replacement guard still refuses it while a Home is pinned to it.
			result.Installed, result.Update, result.InstalledVersion = true, true, entry.Version
		default:
			// A plugin with this name alone is not evidence of the reviewed pin.
			// Never enable an unrelated installation on a folder-card click.
			return personalassistanthttp.FolderHomeProviderPreview{}, personalassistant.ErrFolderOutcomeUnavailable
		}
		return result, nil
	}
	return result, nil
}

type installedHomeProvider int

const (
	installedHomeProviderUnrelated installedHomeProvider = iota
	installedHomeProviderCurrent
	installedHomeProviderOlder
)

// classifyInstalledHomeProvider compares an installed plugin with the newest
// reviewed release of the Home provider it is named after.
func classifyInstalledHomeProvider(entry plugin.InstalledPlugin, release verifiedRelease, provider reviewedintegration.HomeProvider) installedHomeProvider {
	if release.source == "" || release.version == "" || entry.Format != provider.SourceFormat {
		return installedHomeProviderUnrelated
	}
	if entry.Source == release.source && entry.Version == release.version {
		return installedHomeProviderCurrent
	}
	// An older release lives at another exact commit; a record naming this
	// release's commit with another version is inconsistent, not older.
	if order, comparable := reviewedintegration.CompareVersions(release.version, entry.Version); comparable && order > 0 &&
		entry.Source != release.source && provider.ReleaseEntry().IsPinnedSource(entry.Source) {
		return installedHomeProviderOlder
	}
	return installedHomeProviderUnrelated
}

func (s folderHomeProviderSetup) Install(ctx context.Context, key, reviewedVersion string) (personalassistanthttp.FolderHomeProviderPreview, error) {
	preview, err := s.Preview(ctx, key)
	if err != nil || preview.Ready {
		return preview, err
	}
	manager := s.builder.pluginHandler.Manager()
	var sourceFormat plugin.SourceFormat
	for _, entry := range reviewedintegration.HomeProviders() {
		if entry.Key == key {
			sourceFormat = entry.SourceFormat
			break
		}
	}
	if preview.Installed && preview.Update {
		if preview.Version == "" || preview.Version != reviewedVersion || preview.Source == "" {
			return personalassistanthttp.FolderHomeProviderPreview{}, personalassistant.ErrFolderWorkspaceRefused
		}
		if _, err := manager.UpdateFromSource(preview.PluginID, preview.Source, sourceFormat, func(report plugin.TrustReport) bool {
			return report.Name == preview.PluginID && report.Format == sourceFormat
		}); err != nil {
			if errors.Is(err, plugin.ErrHomeUpgradeRequired) {
				return personalassistanthttp.FolderHomeProviderPreview{}, err
			}
			return personalassistanthttp.FolderHomeProviderPreview{}, personalassistant.ErrFolderOutcomeUnavailable
		}
		if err := manager.SetEnabled(preview.PluginID, true); err != nil {
			return personalassistanthttp.FolderHomeProviderPreview{}, personalassistant.ErrFolderOutcomeUnavailable
		}
		verified, err := s.Preview(ctx, key)
		if err != nil || !verified.Ready {
			return personalassistanthttp.FolderHomeProviderPreview{}, errors.New("reviewed Home provider was not ready after the update")
		}
		return verified, nil
	}
	if preview.Installed {
		if reviewedVersion != preview.Version {
			return personalassistanthttp.FolderHomeProviderPreview{}, personalassistant.ErrFolderWorkspaceRefused
		}
		if err := manager.SetEnabled(preview.PluginID, true); err != nil {
			return personalassistanthttp.FolderHomeProviderPreview{}, personalassistant.ErrFolderOutcomeUnavailable
		}
		preview.Ready = true
		return preview, nil
	}
	if preview.Version == "" || preview.Version != reviewedVersion || preview.Source == "" {
		return personalassistanthttp.FolderHomeProviderPreview{}, personalassistant.ErrFolderWorkspaceRefused
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
