package plugin

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
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

type Manager struct {
	operationMu  sync.Mutex
	reg          MCPRegistrar
	skills       SkillInstaller
	store        *Store
	marketplaces *MarketplaceStore
	artifacts    *ArtifactInstaller
	surfaces     contributionLifecycle
	cloneDir     string
	previewDir   string
}

// NewManager builds a plugin manager backed by the managed pluginsDir (which
// holds the installed-plugins registry and marketplace records). cloneDir is
// where git sources are cloned.
func NewManager(reg MCPRegistrar, skills SkillInstaller, pluginsDir, cloneDir string) *Manager {
	return &Manager{
		reg:          reg,
		skills:       skills,
		store:        NewStore(pluginsDir),
		marketplaces: NewMarketplaceStore(pluginsDir),
		artifacts:    NewArtifactInstaller(pluginsDir),
		cloneDir:     cloneDir,
		previewDir:   filepath.Join(pluginsDir, "preview"),
	}
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

	if confirm != nil && !confirm(BuildTrustReport(d)) {
		return InstalledPlugin{}, ErrInstallDeclined
	}

	resolvedArtifacts, err := m.artifacts.Install(context.Background(), d)
	if err != nil {
		return InstalledPlugin{}, err
	}
	res, err := Register(d, m.reg, m.skills)
	if err != nil {
		return InstalledPlugin{}, err
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
		WorkspaceSurfaces:    d.WorkspaceSurfaces,
		ResolvedArtifacts:    resolvedArtifacts,
		ResolvedBlueprints:   append([]ResolvedBlueprint(nil), d.ResolvedBlueprints...),
		ComponentFingerprint: trustedComponentFingerprint(d),
		Generation:           1,
		Enabled:              false,
		InstalledAt:          time.Now().UTC(),
	}
	if err := m.store.Put(p); err != nil {
		// Couldn't record the install — undo the registration so we don't leave
		// orphaned components the store doesn't know about.
		rollback(m.reg, m.skills, d.Name, RegisterResult{MCPServers: res.MCPServers, Skills: res.Skills})
		return InstalledPlugin{}, fmt.Errorf("plugin: record install: %w", err)
	}
	if m.surfaces != nil {
		if err := m.surfaces.RegisterInstalled(p); err != nil {
			_ = m.store.Delete(p.Name)
			rollback(m.reg, m.skills, d.Name, RegisterResult{MCPServers: res.MCPServers, Skills: res.Skills})
			return InstalledPlugin{}, fmt.Errorf("plugin: register workspace surfaces: %w", err)
		}
	}
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
	d, err := Load(source, m.cloneDir, prefer)
	if err != nil {
		return TrustReport{}, err
	}
	if err := prepareTrustedBlueprints(&d); err != nil {
		return TrustReport{}, err
	}
	return BuildTrustReport(d), nil
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
	for _, sk := range p.Skills {
		if err := m.skills.RemoveSkill(name, sk); err != nil {
			restoreSurface()
			return fmt.Errorf("plugin %q: remove skill %q: %w", name, sk, err)
		}
	}
	if m.surfaces != nil {
		if err := m.surfaces.DeleteState(p.Name); err != nil {
			restoreSurface()
			return fmt.Errorf("plugin %q: delete namespaced state: %w", name, err)
		}
	}
	return m.store.Delete(name)
}

type updatePreviewResolution struct {
	sourceVersion     string
	trustReport       TrustReport
	componentsChanged bool
}

// UpdateAvailability is the notification-safe result of checking one installed
// plugin against its recorded source. Available is based on source differences,
// not semver ordering: a version or trusted-component change is enough.
type UpdateAvailability struct {
	Name              string `json:"name"`
	InstalledVersion  string `json:"installed_version,omitempty"`
	AvailableVersion  string `json:"available_version,omitempty"`
	ComponentsChanged bool   `json:"components_changed"`
	Available         bool   `json:"available"`
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
		}
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
	if componentsChanged(existing, d) && confirm != nil && !confirm(BuildTrustReport(d)) {
		restoreSurface()
		return InstalledPlugin{}, ErrInstallDeclined
	}
	resolvedArtifacts, err := m.artifacts.Install(context.Background(), d)
	if err != nil {
		restoreSurface()
		return InstalledPlugin{}, err
	}

	// Reinstall: remove the previously-registered components, then register the
	// refreshed set.
	for _, srv := range existing.MCPServers {
		_ = m.reg.RemoveServer(srv)
	}
	for _, sk := range existing.Skills {
		_ = m.skills.RemoveSkill(name, sk)
	}
	res, err := Register(d, m.reg, m.skills)
	if err != nil {
		restoreSurface()
		return InstalledPlugin{}, err
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
		WorkspaceSurfaces:    d.WorkspaceSurfaces,
		ResolvedArtifacts:    resolvedArtifacts,
		ResolvedBlueprints:   append([]ResolvedBlueprint(nil), d.ResolvedBlueprints...),
		ComponentFingerprint: trustedComponentFingerprint(d),
		Generation:           nextPluginGeneration(existing.Generation),
		Enabled:              existing.Enabled,
		InstalledAt:          existing.InstalledAt,
	}
	if m.surfaces != nil {
		if err := m.surfaces.RegisterInstalled(updated); err != nil {
			rollback(m.reg, m.skills, d.Name, res)
			restoreSurface()
			return InstalledPlugin{}, fmt.Errorf("plugin: register updated workspace surfaces: %w", err)
		}
		surfaceStopped = false
	}
	if err := m.store.Put(updated); err != nil {
		if m.surfaces != nil {
			_ = m.surfaces.Unregister(updated.Name, updated.Generation)
			_ = m.surfaces.RegisterInstalled(existing)
		}
		return InstalledPlugin{}, fmt.Errorf("plugin: record update: %w", err)
	}
	return updated, nil
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
