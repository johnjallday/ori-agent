package server

import (
	"context"
	"errors"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/personalassistanthttp"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
)

// homeProviderSetup is folderHomeProviderSetup as the build sees it.
type homeProviderSetup interface {
	Preview(ctx context.Context, key string) (personalassistanthttp.FolderHomeProviderPreview, error)
	Install(ctx context.Context, key, reviewedVersion string) (personalassistanthttp.FolderHomeProviderPreview, error)
}

// buildHomeProviders lets a build install the reviewed plugin its recognized
// folder's Home comes from, through Home's own reviewed-release service: the
// same preview, the same release check, the same install and enable.
// installed is told after an install, so cached plans are read again.
type buildHomeProviders struct {
	setup     homeProviderSetup
	installed func()
}

func (p buildHomeProviders) PreviewHomeProvider(ctx context.Context, key string) (sessionhttp.BuildHomeProvider, error) {
	preview, err := p.setup.Preview(ctx, key)
	if err != nil {
		return sessionhttp.BuildHomeProvider{}, buildHomeProviderError(err)
	}
	return buildHomeProviderView(preview), nil
}

func (p buildHomeProviders) InstallHomeProvider(ctx context.Context, key, reviewedVersion string) (sessionhttp.BuildHomeProvider, error) {
	installed, err := p.setup.Install(ctx, key, reviewedVersion)
	if err != nil {
		return sessionhttp.BuildHomeProvider{}, buildHomeProviderError(err)
	}
	if p.installed != nil {
		p.installed()
	}
	return buildHomeProviderView(installed), nil
}

func buildHomeProviderView(preview personalassistanthttp.FolderHomeProviderPreview) sessionhttp.BuildHomeProvider {
	return sessionhttp.BuildHomeProvider{
		PluginID: preview.PluginID, Version: preview.Version, Source: preview.Source, Disclosure: preview.Disclosure,
		Ready: preview.Ready, Installed: preview.Installed, Update: preview.Update,
		InstalledVersion: preview.InstalledVersion, DevelopmentCopy: preview.DevelopmentCopy,
	}
}

// buildHomeProviderError says the service's refusals in the build's terms:
// a confirmation for a release that is no longer the one the host would
// install is a changed release, a pinned Home asks for its own upgrade, and
// everything else is unavailable.
func buildHomeProviderError(err error) error {
	var upgrade *plugin.HomeUpgradeRequiredError
	switch {
	case errors.As(err, &upgrade) || errors.Is(err, plugin.ErrHomeUpgradeRequired):
		return errors.Join(sessionhttp.ErrBuildProviderHomeUpgrade, err)
	case errors.Is(err, personalassistant.ErrFolderWorkspaceRefused):
		return errors.Join(sessionhttp.ErrBuildProviderChanged, err)
	default:
		return errors.Join(sessionhttp.ErrBuildProviderUnavailable, err)
	}
}
