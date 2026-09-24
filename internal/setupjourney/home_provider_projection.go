package setupjourney

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/blueprintreadiness"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// HomeProviderProjection describes the plugin that provides a split
// blueprint's Home when that plugin is not ready. Its copy and actions come
// from the same readiness derivation the Create Workspace card uses, so the
// quest and the card never drift. It carries no source, path, or command: the
// recovery endpoint resolves those from the trusted template.
type HomeProviderProjection struct {
	PluginID string `json:"plugin_id"`
	// DisplayName is the host-reviewed name, set only when Reviewed.
	DisplayName string `json:"display_name,omitempty"`
	// Reviewed reports that the plugin is on the host's reviewed Home-provider
	// list, which is what makes an in-quest install an offer.
	Reviewed bool `json:"reviewed"`
	// MinimumVersion is the lowest reviewed release, set only when Reviewed.
	MinimumVersion string `json:"minimum_version,omitempty"`
	// TemplateID is the blueprint's qualified ID the recovery endpoint takes.
	TemplateID string                      `json:"template_id"`
	Installed  bool                        `json:"installed"`
	Enabled    bool                        `json:"enabled"`
	Version    string                      `json:"version,omitempty"`
	Generation uint64                      `json:"generation,omitempty"`
	Reason     blueprintreadiness.Reason   `json:"reason"`
	Summary    string                      `json:"summary"`
	Detail     string                      `json:"detail,omitempty"`
	Actions    []blueprintreadiness.Action `json:"actions,omitempty"`
}

// HomeProviderState is one derivation of a split blueprint's readiness plus
// whether its Home provider is on the host's reviewed list.
type HomeProviderState struct {
	Readiness      blueprintreadiness.Readiness
	DisplayName    string
	MinimumVersion string
	Reviewed       bool
}

// HomeProviderSource derives a split blueprint's Home-provider state from the
// same sources as the Create Workspace card. It is read-only.
type HomeProviderSource interface {
	HomeProviderState(context.Context, projecttemplates.Template) (HomeProviderState, error)
}

// newHomeProviderProjection returns nil when the derivation has nothing to
// explain, so the caller falls back to the unexplainable state.
func newHomeProviderProjection(template projecttemplates.Template, state HomeProviderState) *HomeProviderProjection {
	readiness := state.Readiness.Normalize()
	if template.AssistantProject == nil || readiness.State == blueprintreadiness.StateReady || readiness.Summary == "" {
		return nil
	}
	provider := template.AssistantProject.Home.ProviderPluginID
	projection := &HomeProviderProjection{
		PluginID: provider, TemplateID: template.ID, Generation: readiness.Generation,
		Reason: readiness.Reason, Summary: readiness.Summary, Detail: readiness.Detail,
		Actions: append([]blueprintreadiness.Action(nil), readiness.Actions...),
	}
	if dependency := readiness.Dependency; dependency != nil && strings.TrimSpace(dependency.PluginName) != "" {
		// The state is about the plugin its actions act on, which is normally
		// the Home provider and otherwise the blueprint's own plugin.
		projection.PluginID = strings.TrimSpace(dependency.PluginName)
		projection.Installed, projection.Enabled = dependency.Installed, dependency.Enabled
		projection.Version = strings.TrimSpace(dependency.PluginVersion)
	}
	if state.Reviewed && strings.EqualFold(projection.PluginID, provider) {
		projection.Reviewed = true
		projection.DisplayName = blueprintreadiness.SanitizeCopy(state.DisplayName, blueprintreadiness.MaxDisplayNameLen)
		projection.MinimumVersion = strings.TrimSpace(state.MinimumVersion)
	}
	if !validHomeProviderProjection(projection) {
		return nil
	}
	return projection
}

func cloneHomeProviderProjection(source *HomeProviderProjection) *HomeProviderProjection {
	if source == nil {
		return nil
	}
	clone := *source
	clone.Actions = append([]blueprintreadiness.Action(nil), source.Actions...)
	return &clone
}

func validHomeProviderProjection(value *HomeProviderProjection) bool {
	if value == nil {
		return true
	}
	if strings.TrimSpace(value.PluginID) == "" || len(value.PluginID) > 128 ||
		value.TemplateID == "" || len(value.TemplateID) > 256 || len(value.Version) > 64 || len(value.MinimumVersion) > 64 ||
		(value.MinimumVersion != "" && !value.Reviewed) ||
		utf8.RuneCountInString(value.DisplayName) > blueprintreadiness.MaxDisplayNameLen ||
		value.Reviewed != (value.DisplayName != "") ||
		value.Summary == "" || utf8.RuneCountInString(value.Summary) > blueprintreadiness.MaxSummaryLen ||
		utf8.RuneCountInString(value.Detail) > blueprintreadiness.MaxDetailLen ||
		value.Reason == blueprintreadiness.ReasonNone || !blueprintreadiness.ValidReason(value.Reason) ||
		len(value.Actions) > blueprintreadiness.MaxActions {
		return false
	}
	seen := make(map[blueprintreadiness.Action]struct{}, len(value.Actions))
	for _, action := range value.Actions {
		if _, ok := blueprintreadiness.ParseAction(string(action)); !ok {
			return false
		}
		if _, duplicate := seen[action]; duplicate {
			return false
		}
		seen[action] = struct{}{}
	}
	return true
}
