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
	reg      MCPRegistrar
	skills   SkillInstaller
	store    *Store
	cloneDir string
}

// NewManager builds a plugin manager. cloneDir is where git sources are cloned.
func NewManager(reg MCPRegistrar, skills SkillInstaller, store *Store, cloneDir string) *Manager {
	return &Manager{reg: reg, skills: skills, store: store, cloneDir: cloneDir}
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
