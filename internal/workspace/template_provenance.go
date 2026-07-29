package workspace

import (
	"maps"
	"strings"
	"time"
)

// TemplateProvenance records the built-in template a workspace was created from,
// in portable workspace metadata (persisted to workspace.json). Features such as
// REAPER readiness and repair identify a workspace's origin from this rather than
// scanning task descriptions or project filenames, which are brittle and can be
// renamed by the user. It carries no executable hooks — it is pure provenance.
type TemplateProvenance struct {
	// TemplateID is the stable built-in identifier, e.g. "reaper-song".
	TemplateID string `json:"template_id,omitempty"`
	// TemplateName is the human-facing template name at creation time.
	TemplateName string `json:"template_name,omitempty"`
	// Builtin is true when the origin was a shipped built-in template.
	Builtin bool `json:"builtin,omitempty"`
	// Version is the template's builtin_version at creation time.
	Version int `json:"version,omitempty"`
	// AppliedAt records when the template was applied to the workspace.
	AppliedAt time.Time `json:"applied_at,omitempty"`
	// DirectoryRequirements are the local folders the template asked the user to
	// choose. They are carried unresolved: creation records what the template
	// requested, and guided setup — not this record — resolves, validates, and
	// stores the user's confirmed selection.
	DirectoryRequirements []DirectoryRequirement `json:"directory_requirements,omitempty"`
	// AutomationRecipes are the watchers/daily runs the template asked Ori to
	// install once the matching directory is confirmed. Recording one installs
	// nothing: no watcher is registered and no schedule is enabled here.
	AutomationRecipes []AutomationRecipe `json:"automation_recipes,omitempty"`
	// CapabilityRequirements are the abstract capabilities (e.g. "calendar")
	// the template needs connected. Recorded unresolved: no connector is
	// chosen, authorized, or bound here.
	CapabilityRequirements []CapabilityRequirement `json:"capability_requirements,omitempty"`
	// Plugins are the plugin names the template declared, and PluginSources the
	// install source it declared for each (see projecttemplates.ToolDefaults).
	// Recorded so a setup step can name and install exactly what the blueprint
	// asked for; recording installs, enables, and attaches nothing.
	Plugins       []string          `json:"plugins,omitempty"`
	PluginSources map[string]string `json:"plugin_sources,omitempty"`
	// SetupWizard is the normalized setup flow the template declared, captured
	// as it read at creation time. It is a snapshot, not a reference: editing
	// or updating the source blueprint afterwards must not change what an
	// existing workspace is being asked to do, nor invalidate progress the user
	// has already made. Nil when the blueprint declares no wizard.
	SetupWizard *SetupWizard `json:"setup_wizard,omitempty"`
}

// cloneCapabilityRequirements returns a defensive copy, including each
// requirement's operation lists.
func cloneCapabilityRequirements(reqs []CapabilityRequirement) []CapabilityRequirement {
	if len(reqs) == 0 {
		return nil
	}
	out := make([]CapabilityRequirement, 0, len(reqs))
	for _, req := range reqs {
		cp := req
		cp.RequiredOperations = append([]string(nil), req.RequiredOperations...)
		cp.OptionalOperations = append([]string(nil), req.OptionalOperations...)
		out = append(out, cp)
	}
	return out
}

// clonePluginSources returns a defensive copy of the declared install sources.
func clonePluginSources(sources map[string]string) map[string]string {
	if len(sources) == 0 {
		return nil
	}
	out := make(map[string]string, len(sources))
	maps.Copy(out, sources)
	return out
}

// cloneTemplateProvenanceInto deep-copies every reference-typed field from src
// into dst. Every read and every write of provenance goes through it, so a
// caller can neither observe nor mutate the workspace's stored snapshot through
// a shared slice, map, or pointer.
func cloneTemplateProvenanceInto(dst *TemplateProvenance, src *TemplateProvenance) {
	dst.DirectoryRequirements = cloneDirectoryRequirements(src.DirectoryRequirements)
	dst.AutomationRecipes = cloneAutomationRecipes(src.AutomationRecipes)
	dst.CapabilityRequirements = cloneCapabilityRequirements(src.CapabilityRequirements)
	dst.Plugins = append([]string(nil), src.Plugins...)
	dst.PluginSources = clonePluginSources(src.PluginSources)
	dst.SetupWizard = CloneSetupWizard(src.SetupWizard)
}

// SetupWizardSnapshot returns a copy of the setup wizard recorded for the
// workspace at creation time, or nil when its blueprint declared none.
func (w *Workspace) SetupWizardSnapshot() *SetupWizard {
	p := w.GetTemplateProvenance()
	if p == nil {
		return nil
	}
	return p.SetupWizard
}

// HasSetupWizard reports whether the workspace carries a usable setup-wizard
// snapshot.
func (w *Workspace) HasSetupWizard() bool {
	return !w.SetupWizardSnapshot().IsEmpty()
}

// TemplateCapabilityRequirement returns the capability the originating template
// declared under the given key, if any.
func (w *Workspace) TemplateCapabilityRequirement(key string) (CapabilityRequirement, bool) {
	p := w.GetTemplateProvenance()
	if p == nil {
		return CapabilityRequirement{}, false
	}
	key = strings.ToLower(strings.TrimSpace(key))
	for _, req := range p.CapabilityRequirements {
		if strings.ToLower(strings.TrimSpace(req.Key)) == key {
			return req, true
		}
	}
	return CapabilityRequirement{}, false
}

// GetTemplateProvenance returns a copy of the workspace's template provenance, if
// any.
func (w *Workspace) GetTemplateProvenance() *TemplateProvenance {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.TemplateProvenance == nil {
		return nil
	}
	cp := *w.TemplateProvenance
	cloneTemplateProvenanceInto(&cp, w.TemplateProvenance)
	return &cp
}

// SetTemplateProvenance records the originating template. Passing nil clears it.
func (w *Workspace) SetTemplateProvenance(p *TemplateProvenance) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if p == nil {
		w.TemplateProvenance = nil
		w.UpdatedAt = time.Now()
		return
	}
	cp := *p
	cp.TemplateID = strings.TrimSpace(cp.TemplateID)
	cloneTemplateProvenanceInto(&cp, p)
	if cp.AppliedAt.IsZero() {
		cp.AppliedAt = time.Now()
	}
	w.TemplateProvenance = &cp
	w.UpdatedAt = time.Now()
}

// IsFromTemplate reports whether the workspace was created from the given
// built-in template ID (case-insensitive).
func (w *Workspace) IsFromTemplate(templateID string) bool {
	p := w.GetTemplateProvenance()
	if p == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(p.TemplateID), strings.TrimSpace(templateID))
}
