package sessionhttp

import (
	"fmt"
	"net/http"

	"github.com/johnjallday/ori-agent/internal/blueprintreadiness"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// blueprintReadinessSources reads the authoritative dependency state at the
// moment of creation, not the moment the catalog was drawn.
//
// The distinction between "the store says nothing is installed" and "the store
// could not be read" is preserved deliberately: the first is a fact creation
// may act on, the second is not, and collapsing them is how a required plugin
// gets skipped because a file was briefly locked.
func (h *Handler) blueprintReadinessSources() blueprintreadiness.Sources {
	if h == nil || h.installedPluginLister == nil {
		// No plugin subsystem is wired at all. There is no authority to consult
		// and nothing installed to consult it about.
		return blueprintreadiness.Sources{}
	}
	installed, err := h.installedPluginLister.List()
	if err != nil {
		return blueprintreadiness.Sources{DependencyStateUnavailable: true}
	}
	return blueprintreadiness.Sources{Installed: installed}
}

// revalidateBlueprintReadiness re-derives readiness from current state. The
// catalog is deliberately nil here: the template arrives already normalized by
// the resolver against the host's registries, so re-checking references would
// only repeat a decision made with the same data.
func (h *Handler) revalidateBlueprintReadiness(template projecttemplates.Template) blueprintreadiness.Readiness {
	return blueprintreadiness.Derive(template, h.blueprintReadinessSources())
}

// blueprintCreationBlocked reports whether readiness must stop creation.
//
// One exemption survives from the original required-plugin gate: a blueprint
// carrying a setup wizard may offer an operating mode that does not need its
// declared plugin, so the wizard — not this gate — decides. The exemption is
// narrow on purpose. It covers only plugin lifecycle states; a manifest that
// cannot be read, a retired blueprint, or a plugin that cannot run on this
// machine is refused whether a wizard is declared or not.
func blueprintCreationBlocked(template projecttemplates.Template, readiness blueprintreadiness.Readiness) bool {
	if readiness.Creatable() {
		return false
	}
	if !template.HasSetupWizard() {
		return true
	}
	switch readiness.Reason {
	case blueprintreadiness.ReasonPluginInstallRequired,
		blueprintreadiness.ReasonPluginEnableRequired,
		blueprintreadiness.ReasonPluginUpdateRequired:
		return false
	default:
		return true
	}
}

// respondBlueprintReadinessConflict refuses creation with the current state.
//
// The body carries the full readiness projection — state, reason, ownership,
// dependency, and the allowlisted recovery actions — so the wizard can render
// the same recovery panel it would have shown at selection time instead of
// reducing a missing dependency to a toast. The pre-existing diagnostic keys
// are still emitted alongside it so a client that has not been updated keeps
// working — but only for a manifest the user actually owns. The contract
// withholds a parser diagnostic from a shipped or plugin-owned blueprint on
// both sides deliberately (Normalize drops it, and there is no author for the
// user to be one); a legacy compatibility key must not reopen that door.
func respondBlueprintReadinessConflict(w http.ResponseWriter, template projecttemplates.Template, readiness blueprintreadiness.Readiness) {
	body := map[string]any{
		"error":     conflictMessage(template, readiness),
		"readiness": readiness,
	}
	switch readiness.Reason {
	case blueprintreadiness.ReasonManifestInvalid:
		if readiness.Ownership == blueprintreadiness.OwnershipUser {
			if template.HasInvalidRuntimeRequirements() {
				body["runtime_requirements_error"] = template.RuntimeRequirementsError
			}
			if template.HasInvalidSetupWizard() {
				body["setup_wizard_error"] = template.SetupWizardError
			}
		}
	case blueprintreadiness.ReasonPluginInstallRequired:
		body["missing_plugins"] = dependencyNames(readiness)
		body["disabled_plugins"] = []string{}
	case blueprintreadiness.ReasonPluginEnableRequired:
		body["missing_plugins"] = []string{}
		body["disabled_plugins"] = dependencyNames(readiness)
	}
	_ = orihttp.RespondJSON(w, http.StatusConflict, body)
}

// conflictMessage keeps the long-standing author-facing wording for a manifest
// the user owns, and uses the readiness copy for every other case — where
// instructing someone to edit template.json names a file they cannot change.
func conflictMessage(template projecttemplates.Template, readiness blueprintreadiness.Readiness) string {
	if readiness.Reason == blueprintreadiness.ReasonManifestInvalid && readiness.Ownership == blueprintreadiness.OwnershipUser {
		if template.HasInvalidRuntimeRequirements() {
			return fmt.Sprintf("This blueprint's runtime requirements are unusable, so no workspace was created. Fix its template.json: %s", template.RuntimeRequirementsError)
		}
		if template.HasInvalidSetupWizard() {
			return fmt.Sprintf("This blueprint's setup wizard is unusable, so no workspace was created. Fix its template.json: %s", template.SetupWizardError)
		}
	}
	if readiness.Reason == blueprintreadiness.ReasonPluginInstallRequired || readiness.Reason == blueprintreadiness.ReasonPluginEnableRequired {
		// Preserved verbatim: existing clients match on this string.
		return "required plugins are not ready"
	}
	if readiness.Summary != "" {
		return readiness.Summary + " No workspace was created."
	}
	return "This blueprint cannot create a workspace right now."
}

func dependencyNames(readiness blueprintreadiness.Readiness) []string {
	if readiness.Dependency == nil || readiness.Dependency.PluginName == "" {
		return []string{}
	}
	return []string{readiness.Dependency.PluginName}
}
