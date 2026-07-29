package setupwizard

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// resolvedWizard is a workspace's setup wizard proven runnable by this build:
// a supported schema version, every step an allowlisted kind, and every
// reference resolving inside the workspace's own snapshot.
//
// Resolution is the only path from persisted data to domain behavior, and it
// takes nothing from the caller but a workspace ID. A client cannot name an
// adapter, a folder, a capability, or a plugin here — it can only name a step
// the workspace already recorded, whose kind and references were fixed at
// creation time.
type resolvedWizard struct {
	wizard     *workspace.SetupWizard
	provenance *workspace.TemplateProvenance
}

// resolve validates a workspace's persisted snapshot.
//
// It fails closed. A snapshot from a newer schema, naming a kind this build
// does not implement, or referencing a requirement that is no longer recorded
// is refused whole — no step of it runs — because a wizard half-understood is
// exactly the state where Ori would grant some access and skip the disclosure
// that explained it. A valid manifest cannot produce any of these; a
// hand-edited workspace.json or a downgraded build can.
func (s *Service) resolve(ws *workspace.Workspace) (resolvedWizard, error) {
	if ws == nil {
		return resolvedWizard{}, ErrNoWizard
	}
	provenance := ws.GetTemplateProvenance()
	if provenance == nil || provenance.SetupWizard.IsEmpty() {
		return resolvedWizard{}, ErrNoWizard
	}
	wizard := provenance.SetupWizard
	if wizard.Version != workspace.SetupWizardSchemaVersion {
		return resolvedWizard{}, fmt.Errorf("%w: recorded schema version %d, this build runs version %d",
			ErrUnsupportedSnapshot, wizard.Version, workspace.SetupWizardSchemaVersion)
	}

	seen := make(map[string]bool, len(wizard.Steps))
	for _, step := range wizard.Steps {
		if strings.TrimSpace(step.ID) == "" {
			return resolvedWizard{}, fmt.Errorf("%w: a recorded step has no id", ErrUnsupportedSnapshot)
		}
		if seen[step.ID] {
			return resolvedWizard{}, fmt.Errorf("%w: duplicate step id %q", ErrUnsupportedSnapshot, step.ID)
		}
		seen[step.ID] = true

		spec, known := step.KindSpec()
		if !known {
			return resolvedWizard{}, fmt.Errorf("%w: step %q has kind %q, which this build does not implement",
				ErrUnsupportedSnapshot, step.ID, step.Kind)
		}
		ref, hasRef := step.Reference()
		if spec.RequiresReference && !hasRef {
			return resolvedWizard{}, fmt.Errorf("%w: step %q references nothing, but kind %q requires a reference",
				ErrUnsupportedSnapshot, step.ID, spec.Kind)
		}
		if hasRef && !referenceResolves(provenance, ref) {
			return resolvedWizard{}, fmt.Errorf("%w: step %q references %q, which the workspace does not record",
				ErrUnsupportedSnapshot, step.ID, ref.Key)
		}
	}
	return resolvedWizard{wizard: wizard, provenance: provenance}, nil
}

// referenceResolves reports whether the workspace's own snapshot declares what
// a step points at. The namespace comes from the step's kind, so a reference
// can only ever be checked against the one list it belongs to.
func referenceResolves(provenance *workspace.TemplateProvenance, ref workspace.SetupStepReference) bool {
	switch ref.Scope {
	case workspace.SetupStepReferenceDirectory:
		for _, dir := range provenance.DirectoryRequirements {
			if strings.EqualFold(strings.TrimSpace(dir.Key), ref.Key) {
				return true
			}
		}
	case workspace.SetupStepReferenceCapability:
		for _, req := range provenance.CapabilityRequirements {
			if strings.EqualFold(strings.TrimSpace(req.Key), ref.Key) {
				return true
			}
		}
	case workspace.SetupStepReferencePlugin:
		for _, name := range provenance.Plugins {
			if strings.EqualFold(strings.TrimSpace(name), ref.Key) {
				return true
			}
		}
	}
	return false
}

// request assembles everything an adapter is given for a step: the step as
// recorded, plus the requirement it references, read from the workspace's own
// snapshot rather than from the (possibly since-edited) template.
func (r resolvedWizard) request(workspaceID string, step workspace.SetupWizardStep) StepRequest {
	req := StepRequest{WorkspaceID: workspaceID, Step: step}
	ref, hasRef := step.Reference()
	if !hasRef {
		return req
	}
	switch ref.Scope {
	case workspace.SetupStepReferenceDirectory:
		for _, dir := range r.provenance.DirectoryRequirements {
			if strings.EqualFold(strings.TrimSpace(dir.Key), ref.Key) {
				directory := dir
				req.Directory = &directory
				break
			}
		}
		for _, recipe := range r.provenance.AutomationRecipes {
			if strings.EqualFold(strings.TrimSpace(recipe.DirectoryKey), ref.Key) {
				automation := recipe
				req.Automation = &automation
				break
			}
		}
	case workspace.SetupStepReferenceCapability:
		for _, capability := range r.provenance.CapabilityRequirements {
			if strings.EqualFold(strings.TrimSpace(capability.Key), ref.Key) {
				requirement := capability
				req.Capability = &requirement
				break
			}
		}
	case workspace.SetupStepReferencePlugin:
		for _, name := range r.provenance.Plugins {
			if strings.EqualFold(strings.TrimSpace(name), ref.Key) {
				req.Plugin = name
				break
			}
		}
		for name, source := range r.provenance.PluginSources {
			if strings.EqualFold(strings.TrimSpace(name), ref.Key) {
				req.PluginSource = source
				break
			}
		}
	}
	return req
}

// adapterFor resolves the adapter a step names, if it names one.
//
// Three outcomes, deliberately distinct: no adapter (a generic step the service
// itself handles), an adapter (run it), or an unregistered name. The last one
// is not treated as a corrupt snapshot — a valid blueprint can name an adapter
// that this particular build has not wired — so the step is blocked with a safe
// category instead of invalidating the whole workspace's setup.
func (s *Service) adapterFor(step workspace.SetupWizardStep) (Adapter, error) {
	name := strings.TrimSpace(step.Adapter)
	if name == "" {
		spec, known := step.KindSpec()
		if known && spec.RequiresAdapter {
			return nil, fmt.Errorf("%w: step %q of kind %q records no adapter", ErrUnknownAdapter, step.ID, spec.Kind)
		}
		return nil, nil
	}
	adapter, ok := s.registry.Lookup(name)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAdapter, name)
	}
	return adapter, nil
}
