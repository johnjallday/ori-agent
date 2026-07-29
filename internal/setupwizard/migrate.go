package setupwizard

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Blueprint is everything a backfill needs from a blueprint that has since
// gained a setup wizard: the wizard itself, plus the requirement lists its
// steps reference by key.
//
// It is a value, not a live template handle, because the point of the backfill
// is to take one snapshot. Re-reading the blueprint on every load is what this
// avoids: a user who edits or deletes a template must not thereby change what
// an already-created workspace is being asked to finish.
type Blueprint struct {
	ID      string
	Name    string
	Version int
	Wizard  *workspace.SetupWizard

	DirectoryRequirements  []workspace.DirectoryRequirement
	AutomationRecipes      []workspace.AutomationRecipe
	CapabilityRequirements []workspace.CapabilityRequirement
	Plugins                []string
	PluginSources          map[string]string
}

// BlueprintLookup resolves the blueprint a workspace recorded in its
// provenance. Returning false means "no wizard for this ID in this build",
// which leaves the workspace exactly as it is.
type BlueprintLookup func(templateID string) (Blueprint, bool)

// SetBlueprintLookup installs the lookup used to backfill workspaces created
// before their blueprint declared a wizard. Without one, no workspace is ever
// migrated — the service simply reports the wizards that workspaces already
// carry.
func (s *Service) SetBlueprintLookup(lookup BlueprintLookup) {
	if s == nil {
		return
	}
	s.blueprints = lookup
}

// migrateIfNeeded backfills one workspace's wizard snapshot, once.
//
// What it may do is deliberately narrow: copy the blueprint's wizard and the
// requirement lists its steps reference into the workspace's own provenance,
// and stamp the progress record as migrated. That is the whole of it.
//
// What it must never do is anything a user would recognize as setup — choose a
// folder, install or attach a plugin, grant a permission, authorize a
// connector, link an account, register a watcher or schedule, seed or start a
// task, or touch a project file. None of those are reachable from here: this
// function has no domain services, and the only fields it writes are the
// snapshot and its marker. The workspace's own recorded requirements always
// win, so running it twice cannot duplicate or overwrite anything.
//
// It returns true when the workspace was changed, in which case the caller
// re-reads it before evaluating.
func (s *Service) migrateIfNeeded(ws *workspace.Workspace) bool {
	if s == nil || s.blueprints == nil || s.store == nil || ws == nil {
		return false
	}
	blueprint, ok := s.eligible(ws)
	if !ok {
		return false
	}
	// Read from the folder-loaded workspace, never from the copy Update hands
	// back: the primary store is SQLite-backed and its copy has no provenance
	// and no progress at all. Deciding eligibility in there would see "nothing
	// recorded" every time and migrate nothing, forever.
	provenance := ws.GetTemplateProvenance()
	progress := ws.GetSetupWizardProgress()
	if provenance == nil {
		return false
	}

	migrated := false
	err := s.store.Update(ws.ID, func(current *workspace.Workspace) error {
		provenance.SetupWizard = blueprint.Wizard
		if blueprint.Version > provenance.Version {
			provenance.Version = blueprint.Version
		}
		if strings.TrimSpace(provenance.TemplateName) == "" {
			provenance.TemplateName = blueprint.Name
		}
		// Only absent lists are filled in. A workspace that already recorded its
		// own requirements keeps them: they are what its existing setup was done
		// against, and replacing them with today's blueprint would silently
		// re-point a folder or a capability the user already satisfied.
		if len(provenance.DirectoryRequirements) == 0 {
			provenance.DirectoryRequirements = blueprint.DirectoryRequirements
		}
		if len(provenance.AutomationRecipes) == 0 {
			provenance.AutomationRecipes = blueprint.AutomationRecipes
		}
		if len(provenance.CapabilityRequirements) == 0 {
			provenance.CapabilityRequirements = blueprint.CapabilityRequirements
		}
		if len(provenance.Plugins) == 0 {
			provenance.Plugins = blueprint.Plugins
		}
		if len(provenance.PluginSources) == 0 {
			provenance.PluginSources = blueprint.PluginSources
		}
		current.SetTemplateProvenance(provenance)

		if progress == nil {
			progress = &workspace.SetupWizardProgress{}
		}
		now := s.timestamp()
		if progress.CreatedAt.IsZero() {
			progress.CreatedAt = now
		}
		if progress.MigratedAt == nil {
			stamp := now
			progress.MigratedAt = &stamp
		}
		progress.UpdatedAt = now
		current.SetSetupWizardProgress(progress)
		migrated = true
		return nil
	})
	if err != nil {
		// A failed backfill is not fatal: the workspace keeps working exactly as
		// it did before, and the next load tries again.
		return false
	}
	return migrated
}

// eligible reports the blueprint to backfill this workspace with, if any.
//
// Identification is provenance-only on purpose. A workspace's recorded
// template ID is durable and unambiguous; a name, a folder layout, or the prose
// of a starter task is neither, and guessing from those is how an unrelated
// workspace acquires someone else's setup.
func (s *Service) eligible(ws *workspace.Workspace) (Blueprint, bool) {
	provenance := ws.GetTemplateProvenance()
	if provenance == nil {
		return Blueprint{}, false
	}
	templateID := strings.TrimSpace(provenance.TemplateID)
	if templateID == "" {
		return Blueprint{}, false
	}
	// Already carries a wizard: never re-snapshot. Progress is recorded against
	// the snapshot, so replacing it with a newer one would reinterpret steps the
	// user has already answered.
	if !provenance.SetupWizard.IsEmpty() {
		return Blueprint{}, false
	}
	blueprint, ok := s.blueprints(templateID)
	if !ok || blueprint.Wizard.IsEmpty() {
		return Blueprint{}, false
	}
	return blueprint, true
}

// configuredBefore reports whether a just-migrated workspace shows evidence of
// having been set up by hand before the wizard existed.
//
// The distinction it draws is between "never done" and "was done and is now
// broken", which is the difference between asking a user to finish setup and
// telling them something they already had has stopped working. Only categories
// that cannot arise from an untouched workspace count: a revoked permission or
// a failing domain call each imply something was there to revoke or to fail.
// not_configured explicitly does not count — it is the signature of a workspace
// where nothing was ever set up.
func configuredBefore(steps []workspace.SetupWizardStep, readiness map[string]StepReadiness) bool {
	for _, step := range steps {
		if !step.Required {
			continue
		}
		verdict, evaluated := readiness[step.ID]
		if !evaluated || verdict.Ready {
			continue
		}
		switch verdict.ErrorCategory {
		case ErrorCategoryPermissionRequired, ErrorCategoryDomainError:
			return true
		}
	}
	return false
}
