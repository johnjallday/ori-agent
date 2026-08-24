// Package pluginworkspace centralizes the one idempotent path that binds an
// installed plugin's recorded components (skills + MCP servers) onto a workspace.
//
// Before this package, three paths inferred and mutated workspace plugin state
// independently and disagreed:
//
//   - Template application (internal/server/template_tools.go) read installed
//     plugins from a hard-coded plugins/ store, ignored the plugin's Enabled
//     flag, and bound components directly. A globally-disabled plugin could end
//     up "In this workspace" and "Globally disabled" at once.
//   - The Plugins tab (internal/web/static/js/modules/workspace-detail-plugins.js)
//     decided "attached" purely from workspace skill/MCP bindings, so an
//     agent-level skill made the plugin look detached.
//   - Manual "Add to workspace" and (new) repair need to optionally enable a
//     disabled plugin, but only after explicit confirmation.
//
// Reconcile is that single service. It reads installed-plugin state through the
// configured plugin Manager/store used by the Plugins API (not a test-only or
// hard-coded registry), and writes bindings through the synchronized workspace
// store so attachment is immediately visible on the read path and survives
// restart. Binding is case-normalized and idempotent: repeating it never
// duplicates a binding and never disturbs unrelated bindings. Save failures are
// reported without leaving a contradictory "applied" success state.
package pluginworkspace

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacecapability"
)

// PluginManager is the read/enable surface the reconciler needs from the
// configured plugin manager (satisfied by *plugin.Manager).
type PluginManager interface {
	List() ([]plugin.InstalledPlugin, error)
	SetEnabled(name string, enabled bool) error
}

// WorkspaceStore is the workspace read/write surface (satisfied by the SyncStore
// via workspace.Store).
type WorkspaceStore interface {
	Get(id string) (*workspace.Workspace, error)
	Save(ws *workspace.Workspace) error
}

// ComponentKind distinguishes a plugin's bindable component types.
type ComponentKind string

const (
	ComponentSkill ComponentKind = "skill"
	ComponentMCP   ComponentKind = "mcp"
)

// Component is one bindable unit recorded by an installed plugin.
type Component struct {
	Kind ComponentKind `json:"kind"`
	Name string        `json:"name"`
}

// String renders a component as "skill:name" / "mcp:name" for warnings and logs.
func (c Component) String() string { return string(c.Kind) + ":" + c.Name }

// PluginState is the reconciled state of one requested plugin.
type PluginState string

const (
	// PluginStateMissing: the plugin is not installed globally.
	PluginStateMissing PluginState = "missing"
	// PluginStateDisabled: installed but its global record is disabled, and the
	// caller did not permit enabling it, so no components were attached.
	PluginStateDisabled PluginState = "disabled"
	// PluginStateDetached: installed and enabled, but one or more recorded
	// components remain unattached (typically because a Save failed).
	PluginStateDetached PluginState = "detached"
	// PluginStateAttached: installed, enabled, and every recorded component is
	// attached to the workspace.
	PluginStateAttached PluginState = "attached"
)

// PluginResult reports the outcome of reconciling one plugin.
type PluginResult struct {
	Name       string      `json:"name"`
	Installed  bool        `json:"installed"`
	Enabled    bool        `json:"enabled"`
	State      PluginState `json:"state"`
	Components []Component `json:"components"`         // all components the plugin records
	Attached   []Component `json:"attached"`           // components attached after reconcile
	Missing    []Component `json:"missing,omitempty"`  // recorded but still unattached
	Applied    []Component `json:"applied,omitempty"`  // newly attached in this pass
	Warnings   []string    `json:"warnings,omitempty"` // non-fatal notes for this plugin
}

// Request describes a reconciliation over one workspace.
type Request struct {
	WorkspaceID string
	// Plugins are the plugin names whose components should be attached.
	Plugins []string
	// AllowEnable permits enabling a globally-disabled plugin before attaching
	// its components. Template application passes false (report, don't enable);
	// manual "Add to workspace" and repair pass true only after explicit user
	// confirmation.
	AllowEnable bool
}

// Result aggregates a reconciliation pass.
type Result struct {
	Plugins  []PluginResult `json:"plugins"`
	Applied  []string       `json:"applied,omitempty"`  // aggregate "skill:x"/"mcp:y" newly bound
	Warnings []string       `json:"warnings,omitempty"` // aggregate non-fatal notes
	// SaveErr is non-nil when persisting the new bindings failed. When set, no
	// component is reported as Applied and affected plugins stay Detached.
	SaveErr error `json:"-"`
}

// Reconciler binds plugin components onto workspaces idempotently.
type Reconciler struct {
	plugins PluginManager
	store   WorkspaceStore
}

// New builds a Reconciler over the configured plugin manager and workspace store.
func New(plugins PluginManager, store WorkspaceStore) *Reconciler {
	return &Reconciler{plugins: plugins, store: store}
}

// AttachCapability reconciles only the owning plugin's portable components for
// one already-persisted owner-aware capability record. Private surface services
// are deliberately not exposed as native MCP bindings.
func (r *Reconciler) AttachCapability(workspaceID string, definition workspacecapability.Definition) error {
	if r == nil || r.store == nil || definition.Owner == nil || !definition.Owner.Valid() {
		return fmt.Errorf("plugin capability owner is unavailable")
	}
	ws, err := r.store.Get(workspaceID)
	if err != nil || ws == nil {
		return err
	}
	record, ok := ws.GetInstalledCapability(definition.ID)
	if !ok || record.Owner == nil || !record.Owner.MatchesPlugin(definition.Owner.PluginID) {
		return fmt.Errorf("workspace capability owner does not match")
	}
	installed, err := r.installedPlugin(definition.Owner.PluginID)
	if err != nil {
		return err
	}
	if !installed.Enabled || !pluginDeclaresCapability(installed, definition.ID) {
		return fmt.Errorf("owning plugin capability is unavailable")
	}

	boundSkills := make(map[string]workspace.SkillBinding)
	for _, binding := range ws.GetSkillBindings() {
		boundSkills[strings.ToLower(strings.TrimSpace(binding.SkillName))] = binding
	}
	boundMCP := make(map[string]workspace.MCPBinding)
	for _, binding := range ws.GetMCPBindings() {
		boundMCP[strings.ToLower(strings.TrimSpace(binding.ServerName))] = binding
	}
	now := time.Now()
	for _, component := range componentsOf(installed) {
		key := strings.ToLower(strings.TrimSpace(component.Name))
		switch component.Kind {
		case ComponentSkill:
			binding, exists := boundSkills[key]
			shared := true
			if !exists {
				binding = workspace.SkillBinding{
					ID: uuid.NewString(), SkillName: component.Name, Enabled: true, CreatedAt: now, UpdatedAt: now,
				}
				if err := ws.UpsertSkillBinding(binding); err != nil {
					return err
				}
				boundSkills[key] = binding
				shared = false
			} else if pluginOwnsResource(ws, definition.Owner.PluginID, workspace.ResourceSkillBinding, binding.ID) {
				shared = false
			}
			ws.RecordCapabilityResource(definition.ID, workspace.CapabilityResource{
				Kind: workspace.ResourceSkillBinding, ID: binding.ID, Shared: shared,
			})
		case ComponentMCP:
			binding, exists := boundMCP[key]
			shared := true
			if !exists {
				binding = workspace.MCPBinding{
					ID: uuid.NewString(), ServerName: component.Name, Enabled: true, CreatedAt: now, UpdatedAt: now,
				}
				if err := ws.UpsertMCPBinding(binding); err != nil {
					return err
				}
				boundMCP[key] = binding
				shared = false
			} else if pluginOwnsResource(ws, definition.Owner.PluginID, workspace.ResourceMCPBinding, binding.ID) {
				shared = false
			}
			ws.RecordCapabilityResource(definition.ID, workspace.CapabilityResource{
				Kind: workspace.ResourceMCPBinding, ID: binding.ID, Shared: shared,
			})
		}
	}
	return r.store.Save(ws)
}

// DetachCapability removes only binding IDs recorded by this capability. A
// user-owned pre-existing binding is preserved, as is a plugin-owned binding
// still referenced by another capability from the same owner.
func (r *Reconciler) DetachCapability(workspaceID string, definition workspacecapability.Definition) error {
	if r == nil || r.store == nil || definition.Owner == nil || !definition.Owner.Valid() {
		return fmt.Errorf("plugin capability owner is unavailable")
	}
	ws, err := r.store.Get(workspaceID)
	if err != nil || ws == nil {
		return err
	}
	record, ok := ws.GetInstalledCapability(definition.ID)
	if !ok {
		return nil
	}
	if record.Owner == nil || !record.Owner.MatchesPlugin(definition.Owner.PluginID) {
		return fmt.Errorf("workspace capability owner does not match")
	}
	for _, resource := range record.OwnedResources {
		if resource.Shared || anotherCapabilityOwnsResource(ws, definition.ID, definition.Owner.PluginID, resource.Kind, resource.ID) {
			continue
		}
		switch resource.Kind {
		case workspace.ResourceSkillBinding:
			if err := ws.DeleteSkillBinding(resource.ID); err != nil {
				return err
			}
		case workspace.ResourceMCPBinding:
			if err := ws.DeleteMCPBinding(resource.ID); err != nil {
				return err
			}
		}
	}
	return r.store.Save(ws)
}

func (r *Reconciler) installedPlugin(pluginID string) (plugin.InstalledPlugin, error) {
	if r.plugins == nil {
		return plugin.InstalledPlugin{}, fmt.Errorf("plugin registry is unavailable")
	}
	plugins, err := r.plugins.List()
	if err != nil {
		return plugin.InstalledPlugin{}, err
	}
	for _, installed := range plugins {
		if strings.EqualFold(strings.TrimSpace(installed.Name), strings.TrimSpace(pluginID)) {
			return installed, nil
		}
	}
	return plugin.InstalledPlugin{}, fmt.Errorf("owning plugin is not installed")
}

func pluginDeclaresCapability(installed plugin.InstalledPlugin, capabilityID string) bool {
	if installed.WorkspaceSurfaces == nil {
		return false
	}
	for _, capability := range installed.WorkspaceSurfaces.Capabilities {
		if strings.EqualFold(strings.TrimSpace(capability.ID), strings.TrimSpace(capabilityID)) {
			return true
		}
	}
	return false
}

func pluginOwnsResource(ws *workspace.Workspace, pluginID, kind, resourceID string) bool {
	for _, record := range ws.GetInstalledCapabilities() {
		if record.Owner == nil || !record.Owner.MatchesPlugin(pluginID) {
			continue
		}
		if _, owned := record.Owns(kind, resourceID); owned {
			return true
		}
	}
	return false
}

func anotherCapabilityOwnsResource(ws *workspace.Workspace, capabilityID, pluginID, kind, resourceID string) bool {
	for _, record := range ws.GetInstalledCapabilities() {
		if strings.EqualFold(record.ID, capabilityID) || record.Owner == nil || !record.Owner.MatchesPlugin(pluginID) {
			continue
		}
		if _, owned := record.Owns(kind, resourceID); owned {
			return true
		}
	}
	return false
}

// Reconcile attaches the requested plugins' recorded components to the workspace.
// It is idempotent, honors the plugin enabled flag, preserves unrelated
// bindings, and reports (rather than hides) missing plugins, disabled plugins,
// and save failures.
func (r *Reconciler) Reconcile(req Request) (Result, error) {
	var res Result
	if r.store == nil {
		return res, nil
	}
	ws, err := r.store.Get(req.WorkspaceID)
	if err != nil {
		return res, err
	}
	if ws == nil {
		return res, nil
	}

	installed := map[string]plugin.InstalledPlugin{}
	if r.plugins != nil {
		list, err := r.plugins.List()
		if err != nil {
			return res, err
		}
		for _, p := range list {
			installed[strings.ToLower(strings.TrimSpace(p.Name))] = p
		}
	}

	// Case-normalized sets of what is already bound, so attachment is idempotent
	// and preserves unrelated bindings.
	boundSkills := map[string]bool{}
	for _, b := range ws.GetSkillBindings() {
		boundSkills[strings.ToLower(strings.TrimSpace(b.SkillName))] = true
	}
	boundMCP := map[string]bool{}
	for _, b := range ws.GetMCPBindings() {
		boundMCP[strings.ToLower(strings.TrimSpace(b.ServerName))] = true
	}

	now := time.Now()
	var pending []Component // planned bindings not yet persisted (cleared on save failure)

	attach := func(pr *PluginResult, comps []Component) {
		for _, c := range comps {
			key := strings.ToLower(c.Name)
			switch c.Kind {
			case ComponentSkill:
				if boundSkills[key] {
					pr.Attached = append(pr.Attached, c)
					continue
				}
				if err := ws.UpsertSkillBinding(workspace.SkillBinding{
					ID: uuid.NewString(), SkillName: c.Name, Enabled: true, CreatedAt: now, UpdatedAt: now,
				}); err != nil {
					pr.Missing = append(pr.Missing, c)
					pr.Warnings = append(pr.Warnings, "could not bind "+c.String()+": "+err.Error())
					continue
				}
				boundSkills[key] = true
				pr.Attached = append(pr.Attached, c)
				pr.Applied = append(pr.Applied, c)
				pending = append(pending, c)
			case ComponentMCP:
				if boundMCP[key] {
					pr.Attached = append(pr.Attached, c)
					continue
				}
				if err := ws.UpsertMCPBinding(workspace.MCPBinding{
					ID: uuid.NewString(), ServerName: c.Name, Enabled: true, CreatedAt: now, UpdatedAt: now,
				}); err != nil {
					pr.Missing = append(pr.Missing, c)
					pr.Warnings = append(pr.Warnings, "could not bind "+c.String()+": "+err.Error())
					continue
				}
				boundMCP[key] = true
				pr.Attached = append(pr.Attached, c)
				pr.Applied = append(pr.Applied, c)
				pending = append(pending, c)
			}
		}
	}

	for _, name := range req.Plugins {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		pr := PluginResult{Name: name}
		p, ok := installed[strings.ToLower(name)]
		if !ok {
			pr.State = PluginStateMissing
			pr.Warnings = append(pr.Warnings, "plugin "+name+" is not installed")
			res.Plugins = append(res.Plugins, pr)
			continue
		}
		pr.Installed = true
		pr.Enabled = p.Enabled
		pr.Components = componentsOf(p)

		if !p.Enabled {
			if !req.AllowEnable {
				pr.State = PluginStateDisabled
				pr.Missing = pr.Components
				pr.Warnings = append(pr.Warnings, "plugin "+name+" is installed but globally disabled")
				res.Plugins = append(res.Plugins, pr)
				continue
			}
			if err := r.plugins.SetEnabled(p.Name, true); err != nil {
				pr.State = PluginStateDisabled
				pr.Missing = pr.Components
				pr.Warnings = append(pr.Warnings, "could not enable plugin "+name+": "+err.Error())
				res.Plugins = append(res.Plugins, pr)
				continue
			}
			pr.Enabled = true
		}

		attach(&pr, pr.Components)
		if len(pr.Missing) == 0 {
			pr.State = PluginStateAttached
		} else {
			pr.State = PluginStateDetached
		}
		res.Plugins = append(res.Plugins, pr)
	}

	if len(pending) == 0 {
		res.aggregate()
		return res, nil
	}

	if err := r.store.Save(ws); err != nil {
		// Persist failed: nothing actually attached. Roll the reported state back
		// so we never claim success we didn't persist.
		res.SaveErr = err
		for i := range res.Plugins {
			pr := &res.Plugins[i]
			if len(pr.Applied) == 0 {
				continue
			}
			// Applied components were never saved: demote them to missing.
			pr.Missing = append(pr.Missing, pr.Applied...)
			pr.Attached = subtractComponents(pr.Attached, pr.Applied)
			pr.Applied = nil
			pr.State = PluginStateDetached
			pr.Warnings = append(pr.Warnings, "workspace save failed; components not attached: "+err.Error())
		}
		res.aggregate()
		return res, nil
	}

	res.aggregate()
	return res, nil
}

// Inspect reports each requested plugin's state (missing / disabled / detached /
// attached) and which recorded components are attached vs. missing, without
// mutating the workspace or plugin store. Readiness uses it to describe state
// truthfully; it never enables a plugin or writes bindings.
func (r *Reconciler) Inspect(workspaceID string, plugins []string) ([]PluginResult, error) {
	if r.store == nil {
		return nil, nil
	}
	ws, err := r.store.Get(workspaceID)
	if err != nil {
		return nil, err
	}
	if ws == nil {
		return nil, nil
	}
	installed := map[string]plugin.InstalledPlugin{}
	if r.plugins != nil {
		list, err := r.plugins.List()
		if err != nil {
			return nil, err
		}
		for _, p := range list {
			installed[strings.ToLower(strings.TrimSpace(p.Name))] = p
		}
	}
	boundSkills := map[string]bool{}
	for _, b := range ws.GetSkillBindings() {
		boundSkills[strings.ToLower(strings.TrimSpace(b.SkillName))] = true
	}
	boundMCP := map[string]bool{}
	for _, b := range ws.GetMCPBindings() {
		boundMCP[strings.ToLower(strings.TrimSpace(b.ServerName))] = true
	}

	out := make([]PluginResult, 0, len(plugins))
	for _, name := range plugins {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		pr := PluginResult{Name: name}
		p, ok := installed[strings.ToLower(name)]
		if !ok {
			pr.State = PluginStateMissing
			out = append(out, pr)
			continue
		}
		pr.Installed = true
		pr.Enabled = p.Enabled
		pr.Components = componentsOf(p)
		for _, c := range pr.Components {
			bound := false
			switch c.Kind {
			case ComponentSkill:
				bound = boundSkills[strings.ToLower(c.Name)]
			case ComponentMCP:
				bound = boundMCP[strings.ToLower(c.Name)]
			}
			if bound {
				pr.Attached = append(pr.Attached, c)
			} else {
				pr.Missing = append(pr.Missing, c)
			}
		}
		switch {
		case !p.Enabled:
			pr.State = PluginStateDisabled
		case len(pr.Missing) == 0:
			pr.State = PluginStateAttached
		default:
			pr.State = PluginStateDetached
		}
		out = append(out, pr)
	}
	return out, nil
}

// aggregate fills the Result-level Applied and Warnings from per-plugin results.
func (res *Result) aggregate() {
	for _, pr := range res.Plugins {
		for _, c := range pr.Applied {
			res.Applied = append(res.Applied, c.String())
		}
		res.Warnings = append(res.Warnings, pr.Warnings...)
	}
}

// componentsOf returns a plugin's recorded skills then MCP servers as Components.
func componentsOf(p plugin.InstalledPlugin) []Component {
	out := make([]Component, 0, len(p.Skills)+len(p.MCPServers))
	for _, s := range p.Skills {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, Component{Kind: ComponentSkill, Name: s})
		}
	}
	for _, s := range p.MCPServers {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, Component{Kind: ComponentMCP, Name: s})
		}
	}
	return out
}

// subtractComponents returns from minus any component (kind+name) in remove.
func subtractComponents(from, remove []Component) []Component {
	if len(remove) == 0 {
		return from
	}
	drop := make(map[string]bool, len(remove))
	for _, c := range remove {
		drop[string(c.Kind)+"\x00"+strings.ToLower(c.Name)] = true
	}
	out := from[:0]
	for _, c := range from {
		if drop[string(c.Kind)+"\x00"+strings.ToLower(c.Name)] {
			continue
		}
		out = append(out, c)
	}
	return out
}
