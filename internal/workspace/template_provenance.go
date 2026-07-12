package workspace

import (
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
