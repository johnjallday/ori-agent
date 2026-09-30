package server

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/johnjallday/ori-agent/internal/homeupgrade"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/plugin"
)

// homeUpgradeSlot hands the upgrade service to the replacement guard. The
// guard is wired with the plugin handler (Phase 17), before the service
// exists (Phase 22.6); it reads the slot at call time.
type homeUpgradeSlot struct {
	service atomic.Pointer[homeupgrade.Service]
}

func (slot *homeUpgradeSlot) allows(current plugin.InstalledPlugin, nextVersion, nextFingerprint string) bool {
	if slot == nil {
		return false
	}
	service := slot.service.Load()
	return service != nil && service.AllowsReplacement(current, nextVersion, nextFingerprint)
}

// homeUpgradePlugins resolves and installs exactly what the Plugins page's
// Update would: the newest reviewed release for a reviewed install, otherwise
// the plugin's recorded source.
type homeUpgradePlugins struct {
	manager    *plugin.Manager
	reviewed   *reviewedIntegrationUpdates
	invalidate func(name string)
}

func (p homeUpgradePlugins) List() ([]plugin.InstalledPlugin, error) { return p.manager.List() }

func (p homeUpgradePlugins) Target(ctx context.Context, installed plugin.InstalledPlugin) (homeupgrade.Target, error) {
	if p.reviewed != nil {
		update := p.reviewed.replacement(ctx, installed)
		if update.Refuse {
			return homeupgrade.Target{}, homeupgrade.ErrCurrent
		}
		if update.Source != "" {
			inspection, err := p.manager.InspectReplacementTarget(installed.Name, update.Source, update.Format)
			if err != nil {
				return homeupgrade.Target{}, err
			}
			return homeupgrade.Target{Source: update.Source, Format: update.Format, Inspection: inspection}, nil
		}
	}
	inspection, err := p.manager.InspectUpdateTarget(installed.Name)
	if err != nil {
		return homeupgrade.Target{}, err
	}
	return homeupgrade.Target{Inspection: inspection}, nil
}

// Replace is the owner's confirmation of the reviewed upgrade, which disclosed
// the target's trust report; it installs through the ordinary replacement path
// and its guard.
func (p homeUpgradePlugins) Replace(_ context.Context, name string, target homeupgrade.Target) error {
	confirm := func(plugin.TrustReport) bool { return true }
	var err error
	if target.Source != "" {
		_, err = p.manager.UpdateFromSource(name, target.Source, target.Format, confirm)
	} else {
		_, err = p.manager.Update(name, confirm)
	}
	if p.invalidate != nil {
		p.invalidate(name)
	}
	return err
}

// initializeHomePackageUpgrades wires the reviewed Home package upgrade and
// settles any operation a crash interrupted (independent-program-homes.md §6.1).
func (b *ServerBuilder) initializeHomePackageUpgrades() {
	if b == nil || b.pluginHandler == nil || b.sessionStore == nil || b.sessionStore.DB() == nil || b.workspaceStore == nil || b.st == nil {
		return
	}
	adapter := homeUpgradePlugins{manager: b.pluginHandler.Manager(), invalidate: b.pluginHandler.UpdateChecker().Invalidate}
	if b.server != nil {
		adapter.reviewed = b.server.reviewedReleases
	}
	service := homeupgrade.New(b.sessionStore.DB(), b.workspaceStore, adapter, b.st)
	b.homeUpgrades = service
	if b.homeUpgradeSlot != nil {
		b.homeUpgradeSlot.service.Store(service)
	}
	if b.sessionHandler != nil {
		b.sessionHandler.SetHomePackageUpgrades(service, b.pluginHandler.UpdateChecker().Snapshot)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := service.Recover(ctx); err != nil {
		logger.Warn("Home package upgrade recovery did not finish; it retries when the upgrade is read", logger.Fields{"error": err.Error()})
	}
}
