package plugin

import (
	"errors"
	"fmt"
	"time"
)

// ErrInstallDeclined is returned when the trust prompt is declined.
var ErrInstallDeclined = errors.New("plugin: installation declined at trust prompt")

// Manager performs plugin install/uninstall against injected component
// registrars and the installed-plugins store. The concrete registrar/installer
// adapters over Ori's live MCP and skills managers are wired during server
// setup (task 4.x).
type Manager struct {
	reg          MCPRegistrar
	skills       SkillInstaller
	store        *Store
	marketplaces *MarketplaceStore
	cloneDir     string
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
		cloneDir:     cloneDir,
	}
}

// Install resolves, discloses, confirms, and registers a plugin. Declining the
// trust prompt makes no changes. On success the plugin is recorded in the store,
// disabled — enabling happens per workspace (task 4.x).
func (m *Manager) Install(source string, prefer SourceFormat, confirm ConfirmFunc) (InstalledPlugin, error) {
	d, err := Load(source, m.cloneDir, prefer)
	if err != nil {
		return InstalledPlugin{}, err
	}

	if confirm != nil && !confirm(BuildTrustReport(d)) {
		return InstalledPlugin{}, ErrInstallDeclined
	}

	res, err := Register(d, m.reg, m.skills)
	if err != nil {
		return InstalledPlugin{}, err
	}

	p := InstalledPlugin{
		Name:        d.Name,
		Version:     d.Version,
		Description: d.Description,
		Source:      source,
		Format:      d.SourceFormat,
		InstallDir:  d.InstallDir,
		MCPServers:  res.MCPServers,
		Skills:      res.Skills,
		Enabled:     false,
		InstalledAt: time.Now().UTC(),
	}
	if err := m.store.Put(p); err != nil {
		// Couldn't record the install — undo the registration so we don't leave
		// orphaned components the store doesn't know about.
		rollback(m.reg, m.skills, d.Name, RegisterResult{MCPServers: res.MCPServers, Skills: res.Skills})
		return InstalledPlugin{}, fmt.Errorf("plugin: record install: %w", err)
	}
	return p, nil
}

// Preview resolves a source and returns its trust report without installing or
// registering anything — used to render the disclosure before confirmation.
func (m *Manager) Preview(source string, prefer SourceFormat) (TrustReport, error) {
	d, err := Load(source, m.cloneDir, prefer)
	if err != nil {
		return TrustReport{}, err
	}
	return BuildTrustReport(d), nil
}

// SetEnabled toggles a plugin's enabled state in the store.
func (m *Manager) SetEnabled(name string, enabled bool) error {
	return m.store.SetEnabled(name, enabled)
}

// List returns installed plugins.
func (m *Manager) List() ([]InstalledPlugin, error) {
	return m.store.List()
}

// Uninstall removes a plugin's registered components and its store entry,
// reversing the install exactly via the recorded component IDs.
func (m *Manager) Uninstall(name string) error {
	p, ok, err := m.store.Get(name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("plugin: %q not installed", name)
	}
	for _, srv := range p.MCPServers {
		if err := m.reg.RemoveServer(srv); err != nil {
			return fmt.Errorf("plugin %q: remove server %q: %w", name, srv, err)
		}
	}
	for _, sk := range p.Skills {
		if err := m.skills.RemoveSkill(name, sk); err != nil {
			return fmt.Errorf("plugin %q: remove skill %q: %w", name, sk, err)
		}
	}
	return m.store.Delete(name)
}

// UpdatePreview re-resolves an installed plugin from its source and returns the
// trust report plus whether the set of registered components changed.
func (m *Manager) UpdatePreview(name string) (TrustReport, bool, error) {
	existing, ok, err := m.store.Get(name)
	if err != nil {
		return TrustReport{}, false, err
	}
	if !ok {
		return TrustReport{}, false, fmt.Errorf("plugin: %q not installed", name)
	}
	d, err := m.reload(existing)
	if err != nil {
		return TrustReport{}, false, err
	}
	return BuildTrustReport(d), componentsChanged(existing, d), nil
}

// Update reinstalls a plugin from its recorded source (re-pulling git sources),
// re-running the trust prompt when the registered component set changed.
func (m *Manager) Update(name string, confirm ConfirmFunc) (InstalledPlugin, error) {
	existing, ok, err := m.store.Get(name)
	if err != nil {
		return InstalledPlugin{}, err
	}
	if !ok {
		return InstalledPlugin{}, fmt.Errorf("plugin: %q not installed", name)
	}
	d, err := m.reload(existing)
	if err != nil {
		return InstalledPlugin{}, err
	}
	if componentsChanged(existing, d) && confirm != nil && !confirm(BuildTrustReport(d)) {
		return InstalledPlugin{}, ErrInstallDeclined
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
		return InstalledPlugin{}, err
	}

	updated := InstalledPlugin{
		Name:        d.Name,
		Version:     d.Version,
		Description: d.Description,
		Source:      existing.Source,
		Format:      d.SourceFormat,
		InstallDir:  d.InstallDir,
		MCPServers:  res.MCPServers,
		Skills:      res.Skills,
		Enabled:     existing.Enabled,
		InstalledAt: existing.InstalledAt,
	}
	if err := m.store.Put(updated); err != nil {
		return InstalledPlugin{}, fmt.Errorf("plugin: record update: %w", err)
	}
	return updated, nil
}

// reload re-resolves a descriptor for an installed plugin, refreshing git clones.
func (m *Manager) reload(existing InstalledPlugin) (PluginDescriptor, error) {
	if g, ok := parseGitSubdir(existing.Source); ok {
		// Composite git repo + subdirectory. Pinned commits are immutable, so
		// re-resolving is idempotent; unpinned subdir sources pull for latest.
		if g.Sha == "" && g.Ref == "" {
			if err := pullGit(existing.InstallDir); err != nil {
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
		if err := pullGit(existing.InstallDir); err != nil {
			return PluginDescriptor{}, err
		}
	}
	mfst, err := DetectManifest(existing.InstallDir, existing.Format)
	if err != nil {
		return PluginDescriptor{}, err
	}
	return Normalize(mfst, existing.Source)
}

// componentsChanged reports whether the descriptor's registered component set
// differs from what the plugin previously registered.
func componentsChanged(existing InstalledPlugin, d PluginDescriptor) bool {
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
