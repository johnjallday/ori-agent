package plugin

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// ErrInstallDeclined is returned when the trust prompt is declined.
var ErrInstallDeclined = errors.New("plugin: installation declined at trust prompt")

// Manager performs plugin install/uninstall against injected component
// registrars and the installed-plugins store. The concrete registrar/installer
// adapters over Ori's live MCP and skills managers are wired during server
// setup (task 4.x).
type contributionLifecycle interface {
	RegisterInstalled(InstalledPlugin) error
	Replace(InstalledPlugin, InstalledPlugin) error
	Unregister(string, uint64) error
	DeleteState(string) error
}

// PluginChangeKind is a completed change to what is installed.
type PluginChangeKind string

const (
	PluginInstalled   PluginChangeKind = "installed"
	PluginUpdated     PluginChangeKind = "updated"
	PluginUninstalled PluginChangeKind = "uninstalled"
)

// PluginChange reports one completed install, update, or uninstall. Enabling
// and disabling are not changes: that setting belongs to each machine.
type PluginChange struct {
	Kind   PluginChangeKind
	Plugin InstalledPlugin
}

type Manager struct {
	operationMu    sync.Mutex
	reg            MCPRegistrar
	skillNameGuard SkillNameGuard
	changeObserver func(PluginChange)
	store          *Store
	marketplaces   *MarketplaceStore
	artifacts      *ArtifactInstaller
	surfaces       contributionLifecycle
	pluginsDir     string
	cloneDir       string
	previewDir     string
	skillDirs      skillDirCache
}

// NewManager builds a plugin manager backed by the managed pluginsDir (which
// holds the installed-plugins registry and marketplace records). cloneDir is
// where git sources are cloned. A plugin's skills are never copied: they are
// read in place from its install folder (see EnabledSkills).
func NewManager(reg MCPRegistrar, pluginsDir, cloneDir string) *Manager {
	return &Manager{
		reg:          reg,
		store:        NewStore(pluginsDir),
		marketplaces: NewMarketplaceStore(pluginsDir),
		artifacts:    NewArtifactInstaller(pluginsDir),
		pluginsDir:   pluginsDir,
		cloneDir:     cloneDir,
		previewDir:   filepath.Join(pluginsDir, "preview"),
	}
}

// FreshPersistencePaths reports only managed plugin state. Linked source paths
// are deliberately absent and therefore survive Start Fresh.
func (m *Manager) FreshPersistencePaths() map[string]string {
	if m == nil {
		return nil
	}
	return map[string]string{
		"plugin_registry":     filepath.Join(m.pluginsDir, "installed.json"),
		"plugin_marketplaces": filepath.Join(m.pluginsDir, "marketplaces.json"),
		"plugin_clones":       m.cloneDir,
		"plugin_state":        filepath.Join(m.pluginsDir, "state"),
		"plugin_artifacts":    filepath.Join(m.pluginsDir, "artifacts"),
		"plugin_preview":      m.previewDir,
	}
}

// SetChangeObserver is told about every completed install, update, and
// uninstall, whichever path made it. It runs while the operation still holds
// the manager, so it must not call back into it; use Installed to read.
func (m *Manager) SetChangeObserver(observer func(PluginChange)) {
	if m == nil {
		return
	}
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	m.changeObserver = observer
}

func (m *Manager) notifyChange(kind PluginChangeKind, record InstalledPlugin) {
	if m.changeObserver != nil {
		m.changeObserver(PluginChange{Kind: kind, Plugin: record})
	}
}

// SetSkillNameGuard refuses installs and updates whose skill names are already
// used outside the plugin registry (the Workspace Directory's Skills folder).
func (m *Manager) SetSkillNameGuard(guard SkillNameGuard) {
	if m == nil {
		return
	}
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	m.skillNameGuard = guard
}

func (m *Manager) SetSurfaceLifecycle(lifecycle contributionLifecycle) {
	if m == nil {
		return
	}
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	m.surfaces = lifecycle
}

func prepareTrustedBlueprints(descriptor *PluginDescriptor) error {
	if descriptor == nil {
		return nil
	}
	resolved, err := ResolvePluginBlueprints(*descriptor)
	if err != nil {
		return err
	}
	descriptor.ResolvedBlueprints = resolved
	assetDigest, err := resolvePluginAssetDigest(*descriptor)
	if err != nil {
		return err
	}
	descriptor.TrustedAssetDigest = assetDigest
	return nil
}

// Install resolves, discloses, confirms, and registers a plugin. Declining the
// trust prompt makes no changes. On success the plugin is recorded in the store,
// disabled — enabling happens per workspace (task 4.x).
func (m *Manager) Install(source string, prefer SourceFormat, confirm ConfirmFunc) (InstalledPlugin, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	return m.install(source, prefer, confirm)
}

func (m *Manager) install(source string, prefer SourceFormat, confirm ConfirmFunc) (InstalledPlugin, error) {
	d, err := Load(source, m.cloneDir, prefer)
	if err != nil {
		return InstalledPlugin{}, err
	}
	if err := prepareTrustedBlueprints(&d); err != nil {
		return InstalledPlugin{}, err
	}
	componentFingerprint := trustedComponentFingerprint(d)
	if err := m.checkSkillNames(d); err != nil {
		return InstalledPlugin{}, err
	}

	if confirm != nil && !confirm(BuildTrustReport(d)) {
		return InstalledPlugin{}, ErrInstallDeclined
	}

	resolvedArtifacts, err := m.artifacts.Install(context.Background(), d)
	if err != nil {
		return InstalledPlugin{}, err
	}
	res, err := Register(d, m.reg)
	if err != nil {
		return InstalledPlugin{}, err
	}
	if trustedComponentFingerprint(d) != componentFingerprint {
		rollback(m.reg, res)
		return InstalledPlugin{}, ErrSourceChanged
	}

	p := InstalledPlugin{
		Name:                 d.Name,
		Version:              d.Version,
		Description:          d.Description,
		Source:               source,
		Format:               d.SourceFormat,
		InstallDir:           d.InstallDir,
		MCPServers:           res.MCPServers,
		Skills:               res.Skills,
		SkillPaths:           skillPathsOf(d),
		WorkspaceSurfaces:    d.WorkspaceSurfaces,
		ResolvedArtifacts:    resolvedArtifacts,
		ResolvedBlueprints:   append([]ResolvedBlueprint(nil), d.ResolvedBlueprints...),
		ComponentFingerprint: componentFingerprint,
		Generation:           1,
		ContentGeneration:    1,
		Enabled:              false,
		InstalledAt:          time.Now().UTC(),
	}
	if err := m.store.Put(p); err != nil {
		// Couldn't record the install — undo the registration so we don't leave
		// orphaned components the store doesn't know about.
		rollback(m.reg, res)
		return InstalledPlugin{}, fmt.Errorf("plugin: record install: %w", err)
	}
	if m.surfaces != nil {
		if err := m.surfaces.RegisterInstalled(p); err != nil {
			_ = m.store.Delete(p.Name)
			rollback(m.reg, res)
			return InstalledPlugin{}, fmt.Errorf("plugin: register workspace surfaces: %w", err)
		}
	}
	m.notifyChange(PluginInstalled, p)
	return p, nil
}

// Preview resolves a source and returns its trust report without installing or
// registering anything — used to render the disclosure before confirmation.
func (m *Manager) Preview(source string, prefer SourceFormat) (TrustReport, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	return m.preview(source, prefer)
}

func (m *Manager) preview(source string, prefer SourceFormat) (TrustReport, error) {
	_, report, err := m.inspect(source, prefer)
	return report, err
}

// Inspect resolves the exact candidate descriptor and complete trust report
// without installing or registering anything. It exists for trusted host
// adapters that must validate version, contribution, blueprint, and program
// identity in addition to rendering Preview's complete disclosure.
func (m *Manager) Inspect(source string, prefer SourceFormat) (PluginDescriptor, TrustReport, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	return m.inspect(source, prefer)
}

func (m *Manager) inspect(source string, prefer SourceFormat) (PluginDescriptor, TrustReport, error) {
	d, err := Load(source, m.cloneDir, prefer)
	if err != nil {
		return PluginDescriptor{}, TrustReport{}, err
	}
	if err := prepareTrustedBlueprints(&d); err != nil {
		return PluginDescriptor{}, TrustReport{}, err
	}
	return d, BuildTrustReport(d), nil
}

// SetEnabled invalidates/stops/replaces the trusted contribution before the
// enabled generation is committed to the store.
func (m *Manager) SetEnabled(name string, enabled bool) error {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	existing, ok, err := m.store.Get(name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("plugin: %q not installed", name)
	}
	if existing.Enabled == enabled {
		return nil
	}
	updated := existing
	updated.Enabled = enabled
	if updated.ContentGeneration == 0 {
		updated.ContentGeneration = existing.EvidenceGeneration()
	}
	updated.Generation = nextPluginGeneration(existing.Generation)
	if m.surfaces != nil {
		if err := m.surfaces.Replace(existing, updated); err != nil {
			return fmt.Errorf("plugin: change workspace surface lifecycle: %w", err)
		}
	}
	if err := m.store.Put(updated); err != nil {
		if m.surfaces != nil {
			_ = m.surfaces.Replace(updated, existing)
		}
		return err
	}
	return nil
}

// List returns installed plugins.
func (m *Manager) List() ([]InstalledPlugin, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	return m.store.List()
}

// Uninstall removes a plugin's registered components and its store entry,
// reversing the install exactly via the recorded component IDs.
func (m *Manager) Uninstall(name string) error {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	p, ok, err := m.store.Get(name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("plugin: %q not installed", name)
	}
	surfaceRemoved := false
	if m.surfaces != nil && p.WorkspaceSurfaces != nil {
		if err := m.surfaces.Unregister(p.Name, p.Generation); err != nil {
			return fmt.Errorf("plugin %q: stop workspace surfaces: %w", name, err)
		}
		surfaceRemoved = true
	}
	restoreSurface := func() {
		if surfaceRemoved {
			_ = m.surfaces.RegisterInstalled(p)
		}
	}
	for _, srv := range p.MCPServers {
		if err := m.reg.RemoveServer(srv); err != nil {
			restoreSurface()
			return fmt.Errorf("plugin %q: remove server %q: %w", name, srv, err)
		}
	}
	// The plugin's skills live in its install folder and leave with it.
	if m.surfaces != nil {
		if err := m.surfaces.DeleteState(p.Name); err != nil {
			restoreSurface()
			return fmt.Errorf("plugin %q: delete namespaced state: %w", name, err)
		}
	}
	if err := m.store.Delete(name); err != nil {
		return err
	}
	m.notifyChange(PluginUninstalled, p)
	return nil
}

type updatePreviewResolution struct {
	sourceVersion     string
	trustReport       TrustReport
	componentsChanged bool
}

// UpdateAvailability is the notification-safe result of checking one installed
// plugin against its recorded source. Available is based on source differences,
// not semver ordering: a version or trusted-component change is enough.
//
// ReviewedRelease marks an answer that came from the host's reviewed release
// resolver rather than from the recorded source, so AvailableVersion names a
// published release and not whatever the source currently declares. CheckUpdate
// never sets it; only the host's availability override does.
type UpdateAvailability struct {
	Name              string `json:"name"`
	InstalledVersion  string `json:"installed_version,omitempty"`
	AvailableVersion  string `json:"available_version,omitempty"`
	ComponentsChanged bool   `json:"components_changed"`
	Available         bool   `json:"available"`
	ReviewedRelease   bool   `json:"reviewed_release,omitempty"`
}

// CheckUpdate resolves one installed plugin's source and derives the small
// availability result used by background notification checks.
func (m *Manager) CheckUpdate(name string) (UpdateAvailability, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	existing, ok, err := m.store.Get(name)
	if err != nil {
		return UpdateAvailability{}, err
	}
	if !ok {
		return UpdateAvailability{}, fmt.Errorf("plugin: %q not installed", name)
	}
	preview, err := m.resolveUpdatePreview(existing)
	if err != nil {
		return UpdateAvailability{}, err
	}
	return UpdateAvailability{
		Name:              existing.Name,
		InstalledVersion:  existing.Version,
		AvailableVersion:  preview.sourceVersion,
		ComponentsChanged: preview.componentsChanged,
		Available:         existing.Version != preview.sourceVersion || preview.componentsChanged,
	}, nil
}

// UpdatePreview re-resolves an installed plugin from its source and returns the
// trust report plus whether the set of registered components changed.
func (m *Manager) UpdatePreview(name string) (TrustReport, bool, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	existing, ok, err := m.store.Get(name)
	if err != nil {
		return TrustReport{}, false, err
	}
	if !ok {
		return TrustReport{}, false, fmt.Errorf("plugin: %q not installed", name)
	}
	preview, err := m.resolveUpdatePreview(existing)
	if err != nil {
		return TrustReport{}, false, err
	}
	return preview.trustReport, preview.componentsChanged, nil
}

// PreviewReplacement resolves an explicit replacement source for an installed
// plugin and returns its trust report plus whether the registered component set
// would change. It is the preview counterpart of UpdateFromSource and installs
// nothing.
func (m *Manager) PreviewReplacement(name, source string, prefer SourceFormat) (TrustReport, bool, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	existing, ok, err := m.store.Get(name)
	if err != nil {
		return TrustReport{}, false, err
	}
	if !ok {
		return TrustReport{}, false, fmt.Errorf("plugin: %q not installed", name)
	}
	candidate, report, err := m.inspect(source, prefer)
	if err != nil {
		return TrustReport{}, false, err
	}
	if candidate.Name != existing.Name {
		return TrustReport{}, false, fmt.Errorf("plugin: reviewed replacement identity mismatch")
	}
	return report, componentsChanged(existing, candidate), nil
}

// resolveUpdatePreview performs the canonical one-pass source resolution used
// by both the manual trust preview and proactive availability checks. Keeping
// the resolved version beside the disclosure and footprint comparison prevents
// callers from reloading the source to discover notification metadata.
func (m *Manager) resolveUpdatePreview(existing InstalledPlugin) (updatePreviewResolution, error) {
	d, err := m.previewReload(existing)
	if err != nil {
		return updatePreviewResolution{}, err
	}
	if err := prepareTrustedBlueprints(&d); err != nil {
		return updatePreviewResolution{}, err
	}
	return updatePreviewResolution{
		sourceVersion:     d.Version,
		trustReport:       BuildTrustReport(d),
		componentsChanged: componentsChanged(existing, d),
	}, nil
}

// Update reinstalls a plugin from its recorded source (re-pulling git sources),
// re-running the trust prompt when the registered component set changed.
func (m *Manager) Update(name string, confirm ConfirmFunc) (InstalledPlugin, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	existing, ok, err := m.store.Get(name)
	if err != nil {
		return InstalledPlugin{}, err
	}
	if !ok {
		return InstalledPlugin{}, fmt.Errorf("plugin: %q not installed", name)
	}
	oldDescriptor, err := m.installedDescriptor(existing)
	if err != nil {
		return InstalledPlugin{}, fmt.Errorf("plugin: read existing generation before update: %w", err)
	}

	// Stop the old generation before a git refresh or artifact replacement can
	// change any file the long-lived service is executing.
	surfaceStopped := false
	if m.surfaces != nil && existing.WorkspaceSurfaces != nil {
		if err := m.surfaces.Unregister(existing.Name, existing.Generation); err != nil {
			return InstalledPlugin{}, fmt.Errorf("plugin: stop workspace surfaces before update: %w", err)
		}
		surfaceStopped = true
	}
	restoreSurface := func() {
		if surfaceStopped {
			_ = m.surfaces.RegisterInstalled(existing)
			surfaceStopped = false
		}
	}
	removedServers := make([]string, 0, len(existing.MCPServers))
	restoreExisting := func() error {
		if restoreErr := restoreRecordedComponents(m.reg, oldDescriptor, removedServers); restoreErr != nil {
			return restoreErr
		}
		if surfaceStopped && m.surfaces != nil {
			if restoreErr := m.surfaces.RegisterInstalled(existing); restoreErr != nil {
				return restoreErr
			}
			surfaceStopped = false
		}
		return nil
	}

	d, err := m.reload(existing)
	if err != nil {
		restoreSurface()
		return InstalledPlugin{}, err
	}
	if err := prepareTrustedBlueprints(&d); err != nil {
		restoreSurface()
		return InstalledPlugin{}, err
	}
	if err := m.checkSkillNames(d); err != nil {
		restoreSurface()
		return InstalledPlugin{}, err
	}
	componentFingerprint := trustedComponentFingerprint(d)
	if (existing.ComponentFingerprint != componentFingerprint || componentsChanged(existing, d)) && confirm != nil && !confirm(BuildTrustReport(d)) {
		restoreSurface()
		return InstalledPlugin{}, ErrInstallDeclined
	}
	resolvedArtifacts, err := m.artifacts.Install(context.Background(), d)
	if err != nil {
		restoreSurface()
		return InstalledPlugin{}, err
	}

	// Reinstall the refreshed set. Every failure after the first removal restores
	// the previously recorded generation rather than leaving mixed components.
	for _, srv := range existing.MCPServers {
		if err := m.reg.RemoveServer(srv); err != nil {
			if restoreErr := restoreExisting(); restoreErr != nil {
				return InstalledPlugin{}, fmt.Errorf("plugin: remove existing server for update: %v; existing generation could not be restored: %w", err, restoreErr)
			}
			return InstalledPlugin{}, fmt.Errorf("plugin: remove existing server for update: %w", err)
		}
		removedServers = append(removedServers, srv)
	}
	res, err := Register(d, m.reg)
	if err != nil {
		if restoreErr := restoreExisting(); restoreErr != nil {
			return InstalledPlugin{}, fmt.Errorf("plugin: update failed and existing generation could not be restored: %w", restoreErr)
		}
		return InstalledPlugin{}, err
	}
	if trustedComponentFingerprint(d) != componentFingerprint {
		rollback(m.reg, res)
		if restoreErr := restoreExisting(); restoreErr != nil {
			return InstalledPlugin{}, fmt.Errorf("plugin: source changed during update and existing generation could not be restored: %w", restoreErr)
		}
		return InstalledPlugin{}, ErrSourceChanged
	}

	updated := InstalledPlugin{
		Name:                 d.Name,
		Version:              d.Version,
		Description:          d.Description,
		Source:               existing.Source,
		Format:               d.SourceFormat,
		InstallDir:           d.InstallDir,
		MCPServers:           res.MCPServers,
		Skills:               res.Skills,
		SkillPaths:           skillPathsOf(d),
		WorkspaceSurfaces:    d.WorkspaceSurfaces,
		ResolvedArtifacts:    resolvedArtifacts,
		ResolvedBlueprints:   append([]ResolvedBlueprint(nil), d.ResolvedBlueprints...),
		ComponentFingerprint: componentFingerprint,
		Generation:           nextPluginGeneration(existing.Generation),
		ContentGeneration:    nextPluginContentGeneration(existing, componentFingerprint),
		Enabled:              existing.Enabled,
		InstalledAt:          existing.InstalledAt,
	}
	if m.surfaces != nil {
		if err := m.surfaces.RegisterInstalled(updated); err != nil {
			rollback(m.reg, res)
			if restoreErr := restoreExisting(); restoreErr != nil {
				return InstalledPlugin{}, fmt.Errorf("plugin: updated surface failed and existing generation could not be restored: %w", restoreErr)
			}
			return InstalledPlugin{}, fmt.Errorf("plugin: register updated workspace surfaces: %w", err)
		}
		surfaceStopped = false
	}
	if err := m.store.Put(updated); err != nil {
		if m.surfaces != nil {
			_ = m.surfaces.Unregister(updated.Name, updated.Generation)
			surfaceStopped = existing.WorkspaceSurfaces != nil
		}
		rollback(m.reg, res)
		if restoreErr := restoreExisting(); restoreErr != nil {
			return InstalledPlugin{}, fmt.Errorf("plugin: record update failed and existing generation could not be restored: %w", restoreErr)
		}
		return InstalledPlugin{}, fmt.Errorf("plugin: record update: %w", err)
	}
	m.notifyChange(PluginUpdated, updated)
	return updated, nil
}

// UpdateFromSource replaces an installed plugin from an explicit trusted
// source rather than following the source recorded by an older install. It is
// used by host-owned reviewed-integration adapters; generic clients still use
// Update. The candidate is fully resolved, disclosed, confirmed, and artifact-
// verified before the old generation is stopped.
func (m *Manager) UpdateFromSource(name, source string, prefer SourceFormat, confirm ConfirmFunc) (InstalledPlugin, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	existing, ok, err := m.store.Get(name)
	if err != nil {
		return InstalledPlugin{}, err
	}
	if !ok {
		return InstalledPlugin{}, fmt.Errorf("plugin: %q not installed", name)
	}
	candidate, err := Load(source, m.cloneDir, prefer)
	if err != nil {
		return InstalledPlugin{}, err
	}
	if err := prepareTrustedBlueprints(&candidate); err != nil {
		return InstalledPlugin{}, err
	}
	if candidate.Name != existing.Name {
		return InstalledPlugin{}, fmt.Errorf("plugin: reviewed replacement identity mismatch")
	}
	if err := m.checkSkillNames(candidate); err != nil {
		return InstalledPlugin{}, err
	}
	componentFingerprint := trustedComponentFingerprint(candidate)
	report := BuildTrustReport(candidate)
	if confirm == nil || !confirm(report) {
		return InstalledPlugin{}, ErrInstallDeclined
	}
	resolvedArtifacts, err := m.artifacts.Install(context.Background(), candidate)
	if err != nil {
		return InstalledPlugin{}, err
	}
	oldDescriptor, err := m.installedDescriptor(existing)
	if err != nil {
		return InstalledPlugin{}, fmt.Errorf("plugin: read existing generation before replacement: %w", err)
	}

	surfaceStopped := false
	if m.surfaces != nil && existing.WorkspaceSurfaces != nil {
		if err := m.surfaces.Unregister(existing.Name, existing.Generation); err != nil {
			return InstalledPlugin{}, fmt.Errorf("plugin: stop workspace surfaces before replacement: %w", err)
		}
		surfaceStopped = true
	}
	removedServers := make([]string, 0, len(existing.MCPServers))
	restoreExisting := func() error {
		if restoreErr := restoreRecordedComponents(m.reg, oldDescriptor, removedServers); restoreErr != nil {
			return restoreErr
		}
		if surfaceStopped && m.surfaces != nil {
			if restoreErr := m.surfaces.RegisterInstalled(existing); restoreErr != nil {
				return restoreErr
			}
			surfaceStopped = false
		}
		return nil
	}
	for _, server := range existing.MCPServers {
		if err := m.reg.RemoveServer(server); err != nil {
			if restoreErr := restoreExisting(); restoreErr != nil {
				return InstalledPlugin{}, fmt.Errorf("plugin: remove existing server for replacement: %v; existing generation could not be restored: %w", err, restoreErr)
			}
			return InstalledPlugin{}, fmt.Errorf("plugin: remove existing server for replacement: %w", err)
		}
		removedServers = append(removedServers, server)
	}
	registered, err := Register(candidate, m.reg)
	if err != nil {
		if restoreErr := restoreExisting(); restoreErr != nil {
			return InstalledPlugin{}, fmt.Errorf("plugin: replacement failed and existing generation could not be restored: %w", restoreErr)
		}
		return InstalledPlugin{}, err
	}
	if trustedComponentFingerprint(candidate) != componentFingerprint {
		rollback(m.reg, registered)
		if restoreErr := restoreExisting(); restoreErr != nil {
			return InstalledPlugin{}, fmt.Errorf("plugin: replacement source changed and existing generation could not be restored: %w", restoreErr)
		}
		return InstalledPlugin{}, ErrSourceChanged
	}
	updated := InstalledPlugin{
		Name: candidate.Name, Version: candidate.Version, Description: candidate.Description,
		Source: source, Format: candidate.SourceFormat, InstallDir: candidate.InstallDir,
		MCPServers: registered.MCPServers, Skills: registered.Skills,
		SkillPaths:        skillPathsOf(candidate),
		WorkspaceSurfaces: candidate.WorkspaceSurfaces, ResolvedArtifacts: resolvedArtifacts,
		ResolvedBlueprints:   append([]ResolvedBlueprint(nil), candidate.ResolvedBlueprints...),
		ComponentFingerprint: componentFingerprint,
		Generation:           nextPluginGeneration(existing.Generation),
		ContentGeneration:    nextPluginContentGeneration(existing, componentFingerprint), Enabled: existing.Enabled,
		InstalledAt: existing.InstalledAt,
	}
	if m.surfaces != nil {
		if err := m.surfaces.RegisterInstalled(updated); err != nil {
			rollback(m.reg, registered)
			if restoreErr := restoreExisting(); restoreErr != nil {
				return InstalledPlugin{}, fmt.Errorf("plugin: replacement surface failed and existing generation could not be restored: %w", restoreErr)
			}
			return InstalledPlugin{}, fmt.Errorf("plugin: register replacement workspace surfaces: %w", err)
		}
		surfaceStopped = false
	}
	if err := m.store.Put(updated); err != nil {
		if m.surfaces != nil {
			_ = m.surfaces.Unregister(updated.Name, updated.Generation)
			surfaceStopped = existing.WorkspaceSurfaces != nil
		}
		rollback(m.reg, registered)
		if restoreErr := restoreExisting(); restoreErr != nil {
			return InstalledPlugin{}, fmt.Errorf("plugin: record replacement failed and existing generation could not be restored: %w", restoreErr)
		}
		return InstalledPlugin{}, fmt.Errorf("plugin: record replacement: %w", err)
	}
	m.notifyChange(PluginUpdated, updated)
	return updated, nil
}

// restoreRecordedComponents re-registers the MCP servers an update removed
// before it failed. Skills need no restoring: they were never copied.
func restoreRecordedComponents(reg MCPRegistrar, descriptor PluginDescriptor, serverNames []string) error {
	servers := make(map[string]struct{}, len(serverNames))
	for _, name := range serverNames {
		servers[name] = struct{}{}
	}
	descriptor.MCPServers = slices.DeleteFunc(descriptor.MCPServers, func(server MCPServerSpec) bool {
		_, restore := servers[NamespacedServerName(descriptor.Name, server.Name)]
		return !restore
	})
	if len(descriptor.MCPServers) != len(servers) {
		return errors.New("recorded server rollback source is incomplete")
	}
	descriptor.Skills = nil
	_, err := Register(descriptor, reg)
	return err
}

func (m *Manager) installedDescriptor(existing InstalledPlugin) (PluginDescriptor, error) {
	root, err := canonicalInstallRoot(existing.InstallDir, existing.Source, m.cloneDir)
	if err != nil {
		return PluginDescriptor{}, err
	}
	manifest, err := DetectManifest(root, existing.Format)
	if err != nil {
		return PluginDescriptor{}, err
	}
	descriptor, err := Normalize(manifest, existing.Source)
	if err != nil {
		return PluginDescriptor{}, err
	}
	if err := prepareTrustedBlueprints(&descriptor); err != nil {
		return PluginDescriptor{}, err
	}
	// A mutable local source may already describe the candidate by the time an
	// update starts. Reconstruct the previously registered footprint from the
	// durable record so newly added components can never enter rollback.
	servers := make(map[string]struct{}, len(existing.MCPServers))
	for _, name := range existing.MCPServers {
		servers[name] = struct{}{}
	}
	filteredServers := make([]MCPServerSpec, 0, len(existing.MCPServers))
	for _, server := range descriptor.MCPServers {
		if _, recorded := servers[NamespacedServerName(existing.Name, server.Name)]; recorded {
			filteredServers = append(filteredServers, server)
		}
	}
	if len(filteredServers) != len(existing.MCPServers) {
		return PluginDescriptor{}, fmt.Errorf("plugin: recorded generation source no longer contains its registered components")
	}
	descriptor.Name = existing.Name
	descriptor.Version = existing.Version
	descriptor.Description = existing.Description
	descriptor.MCPServers = filteredServers
	descriptor.Skills = nil
	descriptor.WorkspaceSurfaces = existing.WorkspaceSurfaces
	descriptor.ResolvedBlueprints = append([]ResolvedBlueprint(nil), existing.ResolvedBlueprints...)
	return descriptor, nil
}

// previewReload re-resolves a descriptor without changing the checkout used by
// the installed plugin. Mutable git sources are refreshed in a separate managed
// preview checkout; immutable pins and local paths are only read.
func (m *Manager) previewReload(existing InstalledPlugin) (PluginDescriptor, error) {
	var root string
	if g, ok := parseGitSubdir(existing.Source); ok {
		var err error
		root, err = ResolveSource(existing.Source, m.previewDir)
		if err != nil {
			return PluginDescriptor{}, err
		}
		if g.Sha == "" && g.Ref == "" {
			if err := pullGit(root); err != nil {
				return PluginDescriptor{}, err
			}
		}
	} else if isGitURL(existing.Source) {
		var err error
		root, err = ResolveSource(existing.Source, m.previewDir)
		if err != nil {
			return PluginDescriptor{}, err
		}
		if err := pullGit(root); err != nil {
			return PluginDescriptor{}, err
		}
	} else {
		var err error
		root, err = canonicalInstallRoot(existing.InstallDir, existing.Source, m.cloneDir)
		if err != nil {
			return PluginDescriptor{}, err
		}
	}

	mfst, err := DetectManifest(root, existing.Format)
	if err != nil {
		return PluginDescriptor{}, err
	}
	return Normalize(mfst, existing.Source)
}

// reload re-resolves a descriptor for an installed plugin, refreshing the live
// git checkout as part of an explicitly confirmed update.
//
// The install root is canonicalized first, so a record written before roots
// were absolutized is refreshed, validated, and re-fingerprinted against the
// bundle that is actually installed. Doing this before the trust comparison
// matters: a root that resolved to the wrong place would produce a descriptor
// whose components differ from the recorded ones for a reason that has nothing
// to do with the plugin changing, and the user would be asked to re-approve
// access on the strength of a path bug.
func (m *Manager) reload(existing InstalledPlugin) (PluginDescriptor, error) {
	installRoot, err := canonicalInstallRoot(existing.InstallDir, existing.Source, m.cloneDir)
	if err != nil {
		return PluginDescriptor{}, err
	}
	if g, ok := parseGitSubdir(existing.Source); ok {
		// Composite git repo + subdirectory. Pinned commits are immutable, so
		// re-resolving is idempotent; unpinned subdir sources pull for latest.
		if g.Sha == "" && g.Ref == "" {
			if err := pullGit(installRoot); err != nil {
				return PluginDescriptor{}, err
			}
		}
		root, err := ResolveSource(existing.Source, m.cloneDir)
		if err != nil {
			return PluginDescriptor{}, err
		}
		mfst, err := DetectManifest(root, existing.Format)
		if err != nil {
			return PluginDescriptor{}, err
		}
		return Normalize(mfst, existing.Source)
	}
	if isGitURL(existing.Source) {
		if err := pullGit(installRoot); err != nil {
			return PluginDescriptor{}, err
		}
	}
	mfst, err := DetectManifest(installRoot, existing.Format)
	if err != nil {
		return PluginDescriptor{}, err
	}
	return Normalize(mfst, existing.Source)
}

// componentsChanged reports whether the descriptor's registered component set
// differs from what the plugin previously registered.
func componentsChanged(existing InstalledPlugin, d PluginDescriptor) bool {
	fingerprint := trustedComponentFingerprint(d)
	if existing.ComponentFingerprint != "" {
		return fingerprint == "" || existing.ComponentFingerprint != fingerprint
	}
	// Records written before trusted footprint fingerprints existed retain the
	// old MCP/skill comparison. Any newly contributed Surface is a changed
	// executable footprint and therefore always requires preview/confirmation.
	if d.WorkspaceSurfaces != nil {
		return true
	}
	servers := make([]string, 0, len(d.MCPServers))
	for _, s := range d.MCPServers {
		servers = append(servers, NamespacedServerName(d.Name, s.Name))
	}
	skills := make([]string, 0, len(d.Skills))
	for _, s := range d.Skills {
		skills = append(skills, s.Name)
	}
	return !sameStringSet(existing.MCPServers, servers) || !sameStringSet(existing.Skills, skills)
}

func nextPluginContentGeneration(existing InstalledPlugin, nextFingerprint string) uint64 {
	current := existing.EvidenceGeneration()
	if current > 0 && existing.ComponentFingerprint == nextFingerprint {
		return current
	}
	return nextPluginGeneration(current)
}

func nextPluginGeneration(current uint64) uint64 {
	if current == ^uint64(0) {
		return current
	}
	return current + 1
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, x := range a {
		set[x] = true
	}
	for _, x := range b {
		if !set[x] {
			return false
		}
	}
	return true
}
