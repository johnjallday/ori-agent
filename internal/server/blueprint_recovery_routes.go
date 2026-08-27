package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/blueprintreadiness"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// Blueprint dependency recovery.
//
// The wizard can repair a blueprint's plugin dependency without leaving the
// Create Workspace flow. This endpoint is what makes that safe:
//
//   - The client sends an action ENUM and a plugin NAME. It never sends a
//     source, a path, or a command. The source comes from the blueprint the
//     user selected, resolved here against the trusted template record.
//   - Nothing happens without an explicit confirmation. A request with
//     confirm=false only previews, and the preview is the existing trust
//     report — the same disclosure the Plugins page shows, unabridged.
//   - Install and enable are separate operations reported separately, so
//     "installed but still disabled" can be said out loud instead of being
//     rounded up to success or down to failure.
//   - The response carries the freshly derived readiness, so the client never
//     has to infer the new state from the fact that a call returned 200.
type blueprintRecoveryRequest struct {
	// Action must be one of the allowlisted recovery actions.
	Action string `json:"action"`
	// Plugin is the dependency to act on, matched case-insensitively against
	// what the blueprint declares or its plugin owner records.
	Plugin string `json:"plugin"`
	// Confirm distinguishes a preview from the act itself.
	Confirm bool `json:"confirm"`
	// Generation is the installed-plugin generation the client's projection was
	// derived from. A confirmation carrying a stale generation is refused: the
	// plugin changed after the disclosure the user actually read.
	Generation uint64 `json:"generation,omitempty"`
}

type blueprintRecoveryResponse struct {
	Readiness blueprintreadiness.Readiness `json:"readiness"`
	Outcome   *blueprintreadiness.Outcome  `json:"outcome,omitempty"`
	// Trust is the disclosure for a preview. It is the plugin manager's own
	// report, unmodified — abridging it here would mean the user confirmed
	// against something less than the truth.
	Trust *plugin.TrustReport `json:"trust,omitempty"`
	// Source is where the plugin will be installed from, disclosed ONLY
	// alongside a trust preview.
	//
	// The catalog withholds it on purpose: a template-declared source is an
	// untrusted hint, and echoing it into every card would let a manifest put
	// a URL of its choosing in front of the user with no context. Here it has
	// context — it sits above the list of commands that source will be allowed
	// to run, immediately before the user agrees. Withholding it here instead
	// would be the opposite failure: asking someone to trust a thing without
	// telling them where it came from.
	Source string `json:"source,omitempty"`
	// Changed reports whether an update alters the registered component set,
	// which is what decides whether trust must be re-confirmed.
	Changed bool `json:"changed,omitempty"`
	// BlueprintID is the blueprint's current qualified ID after the action.
	// A successful install or enable can move a blueprint from a stale
	// built-in to its plugin-owned replacement, so the client is told which
	// entry to select rather than guessing from display text.
	BlueprintID string `json:"blueprint_id,omitempty"`
}

// handleBlueprintPluginRecovery serves
// POST /api/project-templates/{templateID}/plugin-recovery.
func (s *Server) handleBlueprintPluginRecovery(w http.ResponseWriter, r *http.Request) {
	var req blueprintRecoveryRequest
	if !orihttp.ParseJSONBody(w, r, &req) {
		return
	}
	action, ok := blueprintreadiness.ParseAction(req.Action)
	if !ok {
		// An unrecognized action is refused outright rather than mapped onto a
		// nearby one: the set is small and fixed, so there is nothing to be
		// lenient about, and leniency here would mean guessing what to run.
		_ = orihttp.RespondBadRequest(w, "unsupported recovery action")
		return
	}
	if !isLifecycleAction(action) {
		// Routing actions (manage plugins, change blueprint, edit a manifest)
		// are the client's to perform. The server has nothing to do for them
		// and must not pretend otherwise.
		_ = orihttp.RespondBadRequest(w, "this action is not performed by the server")
		return
	}
	if s.Handlers == nil || s.Handlers.Plugin == nil {
		_ = orihttp.RespondError(w, http.StatusServiceUnavailable, "plugin management is unavailable")
		return
	}

	templateID := strings.TrimSpace(r.PathValue("templateID"))
	template, err := s.resolveRecoveryBlueprint(templateID)
	if err != nil {
		s.respondProjectTemplateError(w, err)
		return
	}

	dependency := strings.TrimSpace(req.Plugin)
	if !blueprintDeclaresPlugin(template, dependency) {
		// The blueprint is the authority on which plugin it needs. Acting on a
		// name the client supplied but the blueprint never mentions would let
		// the wizard install anything the user could type.
		_ = orihttp.RespondBadRequest(w, "this blueprint does not depend on that plugin")
		return
	}

	manager := s.Handlers.Plugin.Manager()
	list, err := manager.List()
	if err != nil {
		logger.Warn("Blueprint recovery could not read the plugin store", logger.Fields{"error": err.Error()})
		_ = orihttp.RespondInternalError(w, "Could not read installed plugins")
		return
	}
	var currentGeneration uint64
	for _, candidate := range list {
		if strings.EqualFold(strings.TrimSpace(candidate.Name), dependency) {
			currentGeneration = candidate.Generation
			break
		}
	}
	if req.Confirm && req.Generation != 0 && currentGeneration != req.Generation {
		// The plugin changed between the disclosure the user read and the
		// confirmation they gave. Re-deriving and refusing is the only honest
		// response: applying it would act on a consent that was given for a
		// different set of components.
		_ = orihttp.RespondJSON(w, http.StatusConflict, blueprintRecoveryResponse{
			Readiness: s.deriveRecoveryReadiness(template),
			Outcome: (&blueprintreadiness.Outcome{
				Action:  action,
				Summary: "This plugin changed while you were reviewing it.",
				Detail:  "Nothing was applied. Review the current details and confirm again.",
			}).NormalizePtr(),
		})
		return
	}

	switch action {
	case blueprintreadiness.ActionInstallPlugin:
		s.recoverByInstalling(w, template, dependency, req.Confirm)
	case blueprintreadiness.ActionEnablePlugin:
		s.recoverByEnabling(w, template, dependency)
	case blueprintreadiness.ActionReviewPluginUpdate:
		s.recoverByUpdating(w, template, dependency, req.Confirm)
	}
}

func isLifecycleAction(action blueprintreadiness.Action) bool {
	switch action {
	case blueprintreadiness.ActionInstallPlugin,
		blueprintreadiness.ActionEnablePlugin,
		blueprintreadiness.ActionReviewPluginUpdate:
		return true
	default:
		return false
	}
}

// resolveRecoveryBlueprint finds the blueprint the recovery is for, including
// candidates whose plugin is currently inert — those are exactly the ones with
// something to recover.
func (s *Server) resolveRecoveryBlueprint(templateID string) (projecttemplates.Template, error) {
	if templateID == "" {
		return projecttemplates.Template{}, fmt.Errorf("%w: %q", projecttemplates.ErrTemplateNotFound, templateID)
	}
	if strings.HasPrefix(templateID, "plugin:") {
		if s.Handlers == nil || s.Handlers.Plugin == nil {
			return projecttemplates.Template{}, fmt.Errorf("%w: %q", projecttemplates.ErrTemplateNotFound, templateID)
		}
		list, err := s.Handlers.Plugin.Manager().List()
		if err != nil {
			return projecttemplates.Template{}, err
		}
		for _, candidate := range candidatePluginBlueprintTemplates(list) {
			if candidate.Template.ID == templateID {
				return candidate.Template, nil
			}
		}
		return projecttemplates.Template{}, fmt.Errorf("%w: %q", projecttemplates.ErrTemplateNotFound, templateID)
	}
	root := resolveTemplatesRoot(s.Core.ConfigManager)
	if s.projectTemplateCatalog == nil {
		return projecttemplates.FindLibraryTemplate(root, templateID)
	}
	return projecttemplates.FindLibraryTemplateWithCatalog(root, templateID, s.projectTemplateCatalog)
}

// blueprintDeclaresPlugin reports whether the blueprint depends on this plugin
// — either by declaring it in tools.plugins, or by being contributed by it.
func blueprintDeclaresPlugin(template projecttemplates.Template, name string) bool {
	want := strings.ToLower(strings.TrimSpace(name))
	if want == "" {
		return false
	}
	if owner := template.PluginOwner; owner != nil {
		if strings.ToLower(strings.TrimSpace(owner.PluginID)) == want {
			return true
		}
	}
	for _, declared := range template.Tools.Plugins {
		if strings.ToLower(strings.TrimSpace(declared)) == want {
			return true
		}
	}
	return false
}

// declaredPluginSource returns the source the blueprint names for a plugin.
// It is read here and used here; it never travels to the client, because a
// template-supplied source is an untrusted hint and the trust preview is the
// one place it is disclosed before being acted on.
func declaredPluginSource(template projecttemplates.Template, name string) string {
	want := strings.ToLower(strings.TrimSpace(name))
	for declared, source := range template.Tools.PluginSources {
		if strings.ToLower(strings.TrimSpace(declared)) == want {
			return strings.TrimSpace(source)
		}
	}
	return ""
}

func (s *Server) deriveRecoveryReadiness(template projecttemplates.Template) blueprintreadiness.Readiness {
	sources := blueprintreadiness.Sources{Catalog: s.projectTemplateCatalog}
	if s.Handlers != nil && s.Handlers.Plugin != nil {
		if list, err := s.Handlers.Plugin.Manager().List(); err == nil {
			sources.Installed = list
		} else {
			sources.DependencyStateUnavailable = true
		}
	}
	return blueprintreadiness.Derive(template, sources)
}

// currentBlueprintID reports the qualified ID this blueprint now has in the
// catalog. After a successful install or enable, a plugin-owned blueprint can
// supersede the stale entry the user selected, so the client is told which one
// to select rather than matching on display text.
func (s *Server) currentBlueprintID(template projecttemplates.Template) string {
	if s.Handlers == nil || s.Handlers.Plugin == nil {
		return template.ID
	}
	owner := template.PluginOwner
	if owner == nil {
		return template.ID
	}
	list, err := s.Handlers.Plugin.Manager().List()
	if err != nil {
		return template.ID
	}
	for _, candidate := range candidatePluginBlueprintTemplates(list) {
		current := candidate.Template.PluginOwner
		if current == nil {
			continue
		}
		if strings.EqualFold(current.PluginID, owner.PluginID) &&
			strings.EqualFold(current.BlueprintID, owner.BlueprintID) {
			return candidate.Template.ID
		}
	}
	return template.ID
}

func (s *Server) respondRecovery(w http.ResponseWriter, status int, template projecttemplates.Template, outcome *blueprintreadiness.Outcome, trust *plugin.TrustReport, changed bool) {
	s.respondRecoveryWithSource(w, status, template, outcome, trust, changed, "")
}

func (s *Server) respondRecoveryWithSource(w http.ResponseWriter, status int, template projecttemplates.Template, outcome *blueprintreadiness.Outcome, trust *plugin.TrustReport, changed bool, source string) {
	// The source travels only with a disclosure. Without a trust report there
	// is no context to read it in, so it is dropped rather than echoed.
	if trust == nil {
		source = ""
	}
	_ = orihttp.RespondJSON(w, status, blueprintRecoveryResponse{
		Readiness:   s.deriveRecoveryReadiness(template),
		Outcome:     outcome,
		Trust:       trust,
		Source:      source,
		Changed:     changed,
		BlueprintID: s.currentBlueprintID(template),
	})
}

// recoverByInstalling previews, then — only on confirmation — installs and
// enables under that one clearly labelled intent.
//
// Enabling is a second operation on purpose. The button says "Install and
// enable", so both are what the user asked for; reporting them separately is
// what lets a half-finished recovery be described accurately instead of
// leaving an installed-but-disabled plugin looking like a failed install.
func (s *Server) recoverByInstalling(w http.ResponseWriter, template projecttemplates.Template, name string, confirm bool) {
	manager := s.Handlers.Plugin.Manager()
	source := declaredPluginSource(template, name)
	if source == "" {
		// Without a declared source there is nothing to install from that the
		// user has seen. Routing them to Manage Plugins is honest; guessing a
		// marketplace entry from the plugin's name is not.
		_ = orihttp.RespondJSON(w, http.StatusConflict, blueprintRecoveryResponse{
			Readiness: s.deriveRecoveryReadiness(template),
			Outcome: (&blueprintreadiness.Outcome{
				Action:  blueprintreadiness.ActionInstallPlugin,
				Summary: "This blueprint does not say where to install its plugin from.",
				Detail:  "Install it from the Plugins page, then come back to this blueprint.",
			}).NormalizePtr(),
		})
		return
	}

	if !confirm {
		report, err := manager.Preview(source, "")
		if err != nil {
			s.respondRecovery(w, http.StatusConflict, template, (&blueprintreadiness.Outcome{
				Action: blueprintreadiness.ActionInstallPlugin,
				Steps: []blueprintreadiness.OutcomeStep{{
					Name: blueprintreadiness.StepPreview, Succeeded: false,
					Message: recoveryFailureMessage(err),
				}},
				Summary: "Ori could not read this plugin.",
				Detail:  "Nothing was installed. You can try again, or install it from the Plugins page.",
			}).NormalizePtr(), nil, false)
			return
		}
		s.respondRecoveryWithSource(w, http.StatusOK, template, nil, &report, false, source)
		return
	}

	steps := []blueprintreadiness.OutcomeStep{}
	if _, err := manager.Install(source, "", func(plugin.TrustReport) bool { return true }); err != nil {
		steps = append(steps, blueprintreadiness.OutcomeStep{
			Name: blueprintreadiness.StepInstall, Succeeded: false, Message: recoveryFailureMessage(err),
		})
		s.respondRecovery(w, http.StatusConflict, template, (&blueprintreadiness.Outcome{
			Action: blueprintreadiness.ActionInstallPlugin, Steps: steps,
			Summary: "The plugin could not be installed.",
			Detail:  "Nothing was changed. You can try again, or install it from the Plugins page.",
		}).NormalizePtr(), nil, false)
		return
	}
	steps = append(steps, blueprintreadiness.OutcomeStep{Name: blueprintreadiness.StepInstall, Succeeded: true})

	if err := manager.SetEnabled(name, true); err != nil {
		steps = append(steps, blueprintreadiness.OutcomeStep{
			Name: blueprintreadiness.StepEnable, Succeeded: false, Message: recoveryFailureMessage(err),
		})
		s.respondRecovery(w, http.StatusOK, template, (&blueprintreadiness.Outcome{
			Action: blueprintreadiness.ActionInstallPlugin, Steps: steps,
			Summary: "Installed, still disabled.",
			Detail:  "The plugin is on this computer but could not be switched on. Try enabling it again.",
		}).NormalizePtr(), nil, false)
		return
	}
	steps = append(steps, blueprintreadiness.OutcomeStep{Name: blueprintreadiness.StepEnable, Succeeded: true})

	s.respondRecovery(w, http.StatusOK, template, (&blueprintreadiness.Outcome{
		Action: blueprintreadiness.ActionInstallPlugin, Completed: true, Steps: steps,
		Summary: "Installed and enabled.",
	}).NormalizePtr(), nil, false)
}

// recoverByEnabling switches on a plugin that is already installed. There is
// nothing to disclose: the components were disclosed when it was installed,
// and enabling registers exactly those.
func (s *Server) recoverByEnabling(w http.ResponseWriter, template projecttemplates.Template, name string) {
	if err := s.Handlers.Plugin.Manager().SetEnabled(name, true); err != nil {
		s.respondRecovery(w, http.StatusConflict, template, (&blueprintreadiness.Outcome{
			Action: blueprintreadiness.ActionEnablePlugin,
			Steps: []blueprintreadiness.OutcomeStep{{
				Name: blueprintreadiness.StepEnable, Succeeded: false, Message: recoveryFailureMessage(err),
			}},
			Summary: "The plugin could not be switched on.",
			Detail:  "It is still installed and still disabled. Try again, or manage it from the Plugins page.",
		}).NormalizePtr(), nil, false)
		return
	}
	s.respondRecovery(w, http.StatusOK, template, (&blueprintreadiness.Outcome{
		Action: blueprintreadiness.ActionEnablePlugin, Completed: true,
		Steps:   []blueprintreadiness.OutcomeStep{{Name: blueprintreadiness.StepEnable, Succeeded: true}},
		Summary: "Enabled.",
	}).NormalizePtr(), nil, false)
}

// recoverByUpdating previews an update and, on confirmation, applies it. The
// preview reports whether the registered component set changes, which is what
// decides whether the user is re-asked to trust it.
func (s *Server) recoverByUpdating(w http.ResponseWriter, template projecttemplates.Template, name string, confirm bool) {
	manager := s.Handlers.Plugin.Manager()
	if !confirm {
		report, changed, err := manager.UpdatePreview(name)
		if err != nil {
			s.respondRecovery(w, http.StatusConflict, template, (&blueprintreadiness.Outcome{
				Action: blueprintreadiness.ActionReviewPluginUpdate,
				Steps: []blueprintreadiness.OutcomeStep{{
					Name: blueprintreadiness.StepPreview, Succeeded: false, Message: recoveryFailureMessage(err),
				}},
				Summary: "Ori could not check for an update.",
				Detail:  "The plugin is unchanged. You can try again, or update it from the Plugins page.",
			}).NormalizePtr(), nil, false)
			return
		}
		s.respondRecovery(w, http.StatusOK, template, nil, &report, changed)
		return
	}

	if _, err := manager.Update(name, func(plugin.TrustReport) bool { return true }); err != nil {
		// The lifecycle restores the previous generation on failure, so the
		// plugin the user had is the plugin they still have.
		s.respondRecovery(w, http.StatusConflict, template, (&blueprintreadiness.Outcome{
			Action: blueprintreadiness.ActionReviewPluginUpdate,
			Steps: []blueprintreadiness.OutcomeStep{{
				Name: blueprintreadiness.StepUpdate, Succeeded: false, Message: recoveryFailureMessage(err),
			}},
			Summary: "The update could not be applied.",
			Detail:  "The version you already had is still installed and unchanged.",
		}).NormalizePtr(), nil, false)
		return
	}
	s.respondRecovery(w, http.StatusOK, template, (&blueprintreadiness.Outcome{
		Action: blueprintreadiness.ActionReviewPluginUpdate, Completed: true,
		Steps:   []blueprintreadiness.OutcomeStep{{Name: blueprintreadiness.StepUpdate, Succeeded: true}},
		Summary: "Updated.",
	}).NormalizePtr(), nil, false)
}

// recoveryFailureMessage turns a plugin manager error into display copy.
//
// Only recognized sentinels get specific wording. Everything else becomes one
// generic sentence, and the real error is logged instead.
//
// This is deliberately stricter than redaction. A manager error is a log line:
// it wraps git output, filesystem paths, and command lines written by code
// that never expected a user to read them. `Could not resolve host:
// evil.example` carries the untrusted source the template supplied — the exact
// thing the trust preview exists to be the only discloser of — and it does so
// as a bare hostname that no locator heuristic would catch. Passing arbitrary
// third-party error text through a filter and hoping is the wrong shape; not
// passing it through at all is the right one.
func recoveryFailureMessage(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, plugin.ErrInstallDeclined):
		return "You cancelled before anything was changed."
	case errors.Is(err, plugin.ErrArtifactUnsupported):
		return "This plugin ships nothing that runs on this computer."
	case errors.Is(err, plugin.ErrArtifactInvalid):
		return "The downloaded files did not match what the plugin published."
	}
	logger.Warn("Blueprint plugin recovery failed", logger.Fields{"error": err.Error()})
	return "Ori could not complete this step. The Plugins page has the full details."
}
