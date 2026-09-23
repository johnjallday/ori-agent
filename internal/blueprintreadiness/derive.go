package blueprintreadiness

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Sources is the authoritative input set a projection may be derived from.
//
// Everything here is either host state or already-trusted installed-plugin
// state. Notably absent: anything a template contributed about a plugin beyond
// its name. A manifest may say "I need the plugin called X"; it may not say
// where X comes from, what version is acceptable, or that X is already
// trusted. Those answers come from the installed-plugin store or from the
// user, via the trust preview.
type Sources struct {
	// Installed is the installed-plugin store snapshot: the only authority on
	// what exists on this machine and whether it is enabled.
	Installed []plugin.InstalledPlugin
	// DependencyStateUnavailable records that the installed-plugin store could
	// not be read for this projection. It is deliberately separate from an
	// empty Installed list — "nothing is installed" and "we could not look"
	// must never collapse into the same answer, because the first is a reason
	// to offer Install and the second is a reason to offer Retry.
	DependencyStateUnavailable bool
	// Catalog is the running host's capability/runtime registry view. A
	// declaration may reference an ID in it; it can never supply one.
	Catalog projecttemplates.RuntimeCatalog
	// ShippedBuiltin reports whether a blueprint ID is still in the embedded
	// starter catalog. Injected rather than called directly so the retired
	// classification is testable without editing the embedded templates.
	ShippedBuiltin func(id string) bool
	// ReviewedHomeProvider reports the display name of a Home provider plugin
	// the host has reviewed, which is what makes installing it from the wizard
	// an offer rather than a guess. Injected for the same reason as
	// ShippedBuiltin; nil selects the built-in allowlist.
	ReviewedHomeProvider func(pluginID string) (displayName string, ok bool)
}

func (s Sources) shipped(id string) bool {
	if s.ShippedBuiltin == nil {
		return projecttemplates.IsBuiltinStarterID(id)
	}
	return s.ShippedBuiltin(id)
}

func (s Sources) reviewedHomeProvider(pluginID string) (string, bool) {
	if s.ReviewedHomeProvider != nil {
		return s.ReviewedHomeProvider(pluginID)
	}
	provider, ok := reviewedintegration.HomeProviderFor(pluginID)
	return provider.DisplayName, ok
}

func (s Sources) lookup(name string) (plugin.InstalledPlugin, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return plugin.InstalledPlugin{}, false
	}
	for _, candidate := range s.Installed {
		if strings.ToLower(strings.TrimSpace(candidate.Name)) == key {
			return candidate, true
		}
	}
	return plugin.InstalledPlugin{}, false
}

// Derive projects one catalog blueprint's readiness. The result is always
// normalized, so a caller may serialize it directly.
//
// The checks run most-fundamental first: who owns the blueprint, whether the
// app still ships it, whether its own manifest can be read, whether the host
// still provides what it references, and only then whether its plugin
// dependencies are satisfied. A blueprint whose manifest cannot be read is not
// additionally reported as needing a plugin — the first answer is the only one
// the user can act on.
func Derive(template projecttemplates.Template, sources Sources) Readiness {
	ownership := DeriveOwnership(template)

	if template.ProgramHomeOwner != nil {
		return deriveProgramHomeOwned(template, sources).Normalize()
	}
	if template.PluginOwner != nil {
		return derivePluginOwned(template, sources).Normalize()
	}

	// A manifest claiming built-in ownership under an ID this build no longer
	// ships is retired, not broken and not the user's to repair. Its files stay
	// exactly where they are; it simply stops being offered.
	if template.Builtin && !sources.shipped(template.ID) {
		return Readiness{
			State: StateUnavailable, Ownership: ownership, Reason: ReasonBlueprintRetired,
			Summary: "This blueprint is no longer included in Ori.",
			Detail:  "Its files are untouched on disk. Existing workspaces created from it keep working; new ones use a current blueprint instead.",
			Actions: []Action{ActionChangeBlueprint},
		}.Normalize()
	}

	if diagnostic := manifestDiagnostic(template); diagnostic != "" {
		return manifestInvalidReadiness(ownership, diagnostic).Normalize()
	}
	if template.TemplateVariant != nil && template.VariantSourceState != projecttemplates.VariantSourceReady {
		return variantSourceReadiness(template).Normalize()
	}

	if template.AssistantProject != nil && template.TemplateVariant != nil {
		if projectPlugin, present := sources.lookup(template.TemplateVariant.Source.PluginID); present {
			if independent := independentHomeReadiness(template, projectPlugin, sources); independent != nil {
				return independentHomeOutcome(template, *independent).Normalize()
			}
		}
	}

	if reference, ok := unsatisfiedHostReference(template, sources.Catalog); ok {
		return Readiness{
			State: StateUnavailable, Ownership: ownership, Reason: ReasonRuntimeProviderUnavailable,
			Summary: "This version of Ori does not provide something this blueprint needs.",
			Detail:  "The blueprint references " + reference + ", which this build does not register. Updating Ori may add it.",
			Actions: []Action{ActionChangeBlueprint},
		}.Normalize()
	}

	return deriveDeclaredPluginDependencies(template, sources, ownership).Normalize()
}

// DeriveOwnership answers who can act on this blueprint's problems. A retired
// on-disk built-in stays built-in owned: the user did not write it, so they
// must never be told to fix it.
func DeriveOwnership(template projecttemplates.Template) Ownership {
	switch {
	case template.ProgramHomeOwner != nil:
		return OwnershipPlugin
	case template.PluginOwner != nil:
		return OwnershipPlugin
	case template.Builtin:
		return OwnershipBuiltin
	default:
		return OwnershipUser
	}
}

func deriveProgramHomeOwned(template projecttemplates.Template, sources Sources) Readiness {
	owner := template.ProgramHomeOwner
	if owner == nil || !owner.Valid() || template.AssistantProgram == nil {
		return manifestInvalidReadiness(OwnershipPlugin, "independent Home provider evidence is invalid")
	}
	dependency := &Dependency{PluginName: owner.PluginID, PluginVersion: owner.PluginVersion}
	if sources.DependencyStateUnavailable {
		return dependencyStateUnknownReadiness(OwnershipPlugin, dependency)
	}
	installed, present := sources.lookup(owner.PluginID)
	if !present {
		return Readiness{State: StateUnavailable, Ownership: OwnershipPlugin, Reason: ReasonDependencyStateUnknown,
			Summary: "This Home provider is no longer installed.", Dependency: dependency, Actions: []Action{ActionRetry, ActionChangeBlueprint}}
	}
	dependency.Installed = true
	dependency.Enabled = installed.Enabled
	dependency.PluginVersion = installed.Version
	if reason, ok := pluginHardBlocker(installed); ok {
		return hardBlockerReadiness(reason, dependency, installed.Generation)
	}
	matches := 0
	if installed.WorkspaceSurfaces != nil {
		for _, home := range installed.WorkspaceSurfaces.AssistantProgramHomes {
			if home.ID == owner.ProgramID && home.SchemaVersion == owner.HomeSchemaVersion && home.Version == owner.HomeVersion &&
				projecttemplates.AssistantProgramHomeDigest(home) == owner.DeclarationDigest {
				matches++
			}
		}
	}
	if matches != 1 || installed.EvidenceGeneration() != owner.PluginGeneration || installed.ComponentFingerprint != owner.ComponentFingerprint ||
		!hasHostFeature(installed, plugin.HostFeatureIndependentProgramHomesV1) || !rolesPackaged(installed.Skills, template.AssistantProgram.Roles) {
		return Readiness{State: StateActionRequired, Ownership: OwnershipPlugin, Reason: ReasonPluginUpdateRequired,
			Summary: "This Home declaration changed and must be reviewed again.", Dependency: dependency,
			Actions: []Action{ActionReviewPluginUpdate, ActionManagePlugins}, Generation: installed.Generation}
	}
	if !installed.Enabled {
		return Readiness{State: StateActionRequired, Ownership: OwnershipPlugin, Reason: ReasonPluginEnableRequired,
			Summary: "This Home provider is installed but disabled.", Dependency: dependency,
			Actions: []Action{ActionEnablePlugin, ActionManagePlugins}, Generation: installed.Generation}
	}
	ready := Ready(OwnershipPlugin)
	ready.Dependency = dependency
	ready.Generation = installed.Generation
	return ready
}

// derivePluginOwned projects a blueprint contributed by an installed plugin.
// The candidate may be inert — included in the catalog so the correct
// plugin-owned manifest can supersede a stale matching built-in — so every
// non-ready outcome here still describes a blueprint the user can see.
func derivePluginOwned(template projecttemplates.Template, sources Sources) Readiness {
	owner := template.PluginOwner
	dependency := &Dependency{PluginName: owner.PluginID, PluginVersion: owner.PluginVersion}

	if sources.DependencyStateUnavailable {
		return dependencyStateUnknownReadiness(OwnershipPlugin, dependency)
	}

	installed, present := sources.lookup(owner.PluginID)
	if !present {
		// The blueprint exists only because a plugin contributed it, so a
		// missing owner means the store changed underneath this projection.
		return Readiness{
			State: StateUnavailable, Ownership: OwnershipPlugin, Reason: ReasonDependencyStateUnknown,
			Summary:    "This blueprint's plugin is no longer installed.",
			Detail:     "Reload the blueprint list to see what is currently available.",
			Dependency: dependency, Actions: []Action{ActionRetry, ActionChangeBlueprint},
		}
	}

	dependency.Installed = true
	dependency.Enabled = installed.Enabled
	if version := strings.TrimSpace(installed.Version); version != "" {
		dependency.PluginVersion = version
	}

	if reason, ok := pluginHardBlocker(installed); ok {
		return hardBlockerReadiness(reason, dependency, installed.Generation)
	}

	// An installed record that no longer resolves this blueprint predates the
	// contribution or was superseded by a narrower one. Updating is the only
	// way to get it back, and the update may change what the plugin asks for.
	if !contributesBlueprint(installed, owner.BlueprintID) {
		return Readiness{
			State: StateActionRequired, Ownership: OwnershipPlugin, Reason: ReasonPluginUpdateRequired,
			Summary:    "This blueprint needs a newer version of its plugin.",
			Detail:     "Reviewing the update shows exactly what the new version asks for before anything changes.",
			Dependency: dependency, Actions: []Action{ActionReviewPluginUpdate, ActionManagePlugins},
			Generation: installed.Generation,
		}
	}

	if !installed.Enabled {
		return Readiness{
			State: StateActionRequired, Ownership: OwnershipPlugin, Reason: ReasonPluginEnableRequired,
			Summary:    "This blueprint's plugin is installed but disabled.",
			Detail:     "Enabling it makes the blueprint usable. Nothing runs until you enable it.",
			Dependency: dependency, Actions: []Action{ActionEnablePlugin, ActionManagePlugins},
			Generation: installed.Generation,
		}
	}
	if len(template.IntakeRequirements) != 0 && !hasHostFeature(installed, plugin.HostFeatureBlueprintIntakeV1) {
		return Readiness{State: StateActionRequired, Ownership: OwnershipPlugin, Reason: ReasonPluginUpdateRequired,
			Summary:    "This blueprint's intake declaration needs a newer plugin version.",
			Detail:     "Review the plugin update before Ori accepts its intake sources or bundled skill.",
			Dependency: dependency, Actions: []Action{ActionReviewPluginUpdate, ActionManagePlugins}, Generation: installed.Generation}
	}
	if template.AssistantProject != nil {
		if independent := independentHomeReadiness(template, installed, sources); independent != nil {
			return independentHomeOutcome(template, *independent)
		}
	}

	ready := Ready(OwnershipPlugin)
	ready.Dependency = dependency
	ready.Generation = installed.Generation
	return ready
}

func independentHomeOutcome(template projecttemplates.Template, blocked Readiness) Readiness {
	if template.StandaloneComposition == nil || template.GroupRequirement == nil || template.GroupRequirement.Policy == projecttemplates.GroupPolicyRequired {
		return blocked
	}
	names := homeNamesFor(template, blocked.Dependency)
	return Readiness{
		State: StateReady, Ownership: OwnershipPlugin,
		Summary: "You can create this on its own now. Adding it to " + names.home + " needs " + names.provider + ".",
		Detail:  blocked.Summary, Dependency: blocked.Dependency, Generation: blocked.Generation,
	}
}

// homeNames is the wording for a split project's Home: the blueprint, the Home
// it joins, and the plugin that provides that Home. Each comes from a trusted
// declaration or the host's reviewed allowlist, with a plain fallback, so the
// copy stays domain-neutral here.
type homeNames struct {
	// blueprint starts a sentence; inBlueprint continues one.
	blueprint   string
	inBlueprint string
	home        string
	provider    string
}

func homeNamesFor(template projecttemplates.Template, dependency *Dependency) homeNames {
	names := homeNames{blueprint: "This blueprint", inBlueprint: "this blueprint", home: "its Home", provider: "the Home's plugin"}
	if name := strings.TrimSpace(template.Name); name != "" {
		names.blueprint, names.inBlueprint = name, name
	}
	if template.GroupRequirement != nil {
		if home := strings.TrimSpace(template.GroupRequirement.DefaultHomeName); home != "" {
			names.home = home
		}
	}
	if dependency != nil {
		if provider := strings.TrimSpace(dependency.DisplayName); provider != "" {
			names.provider = provider
		} else if provider := strings.TrimSpace(dependency.PluginName); provider != "" {
			names.provider = provider
		}
	}
	return names
}

// independentHomeReadiness verifies the second provider of a split project
// without installing, enabling, creating, or adopting anything. nil means the
// exact reciprocal declaration is currently available.
func independentHomeReadiness(template projecttemplates.Template, projectPlugin plugin.InstalledPlugin, sources Sources) *Readiness {
	project := template.AssistantProject
	if project == nil {
		return nil
	}
	dependency := &Dependency{PluginName: project.Home.ProviderPluginID}
	displayName, reviewed := sources.reviewedHomeProvider(project.Home.ProviderPluginID)
	if reviewed {
		dependency.DisplayName = displayName
	}
	names := homeNamesFor(template, dependency)
	homePlugin, present := sources.lookup(project.Home.ProviderPluginID)
	if !present {
		// The provider is a separate plugin the project plugin never installs.
		// Only a host-reviewed provider can be installed from here; any other is
		// named, and the user is sent to install it themselves.
		result := Readiness{State: StateActionRequired, Ownership: OwnershipPlugin, Reason: ReasonPluginInstallRequired,
			Summary:    names.blueprint + " needs " + names.home + ", which comes from a separate plugin.",
			Detail:     "Install and enable " + names.provider + " from the Plugins page, then come back. Installing this blueprint's plugin did not add it.",
			Dependency: dependency, Actions: []Action{ActionManagePlugins, ActionChangeBlueprint}}
		if reviewed {
			result.Detail = "Install " + names.provider + " to add it. Installing this blueprint's plugin did not add it."
			result.Actions = []Action{ActionInstallPlugin, ActionManagePlugins, ActionChangeBlueprint}
		}
		return &result
	}
	dependency.Installed = true
	dependency.Enabled = homePlugin.Enabled
	dependency.PluginVersion = strings.TrimSpace(homePlugin.Version)
	if reason, blocked := pluginHardBlocker(homePlugin); blocked {
		result := hardBlockerReadiness(reason, dependency, homePlugin.Generation)
		return &result
	}
	if !homePlugin.Enabled {
		result := Readiness{State: StateActionRequired, Ownership: OwnershipPlugin, Reason: ReasonPluginEnableRequired,
			Summary:    names.provider + " is installed but switched off.",
			Detail:     "Enable it so " + names.inBlueprint + " can use " + names.home + ".",
			Dependency: dependency, Actions: []Action{ActionEnablePlugin, ActionManagePlugins}, Generation: homePlugin.Generation}
		return &result
	}
	if !hasHostFeature(projectPlugin, plugin.HostFeatureIndependentProgramHomesV1) || !hasHostFeature(homePlugin, plugin.HostFeatureIndependentProgramHomesV1) ||
		!rolesPackaged(projectPlugin.Skills, project.ProgramRoles()) {
		result := Readiness{State: StateActionRequired, Ownership: OwnershipPlugin, Reason: ReasonPluginUpdateRequired,
			Summary:    names.provider + " and this blueprint's plugin do not work together yet.",
			Detail:     "One of them needs an update. Reviewing an update shows what it changes before anything is applied.",
			Dependency: dependency, Actions: []Action{ActionReviewPluginUpdate, ActionManagePlugins}, Generation: homePlugin.Generation}
		return &result
	}
	blueprintID := ""
	if template.PluginOwner != nil {
		blueprintID = template.PluginOwner.BlueprintID
	} else if template.TemplateVariant != nil {
		blueprintID = template.TemplateVariant.Source.BlueprintID
	}
	matches := 0
	for _, home := range homePlugin.WorkspaceSurfaces.AssistantProgramHomes {
		if home.ID != project.Home.ProgramID || home.SchemaVersion != project.Home.HomeSchemaVersion ||
			home.Version < project.Home.MinHomeVersion || home.Version > project.Home.MaxHomeVersion ||
			!rolesPackaged(homePlugin.Skills, home.AssistantProgram().Roles) || independentRoleCollision(home.AssistantProgram().Roles, project.ProgramRoles()) {
			continue
		}
		attachments := 0
		for _, allowed := range home.AllowedProjectAttachments {
			if allowed.ProviderPluginID == projectPlugin.Name && allowed.BlueprintID == blueprintID &&
				allowed.ProjectTeamID == project.ID && allowed.ProjectTeamSchemaVersion == project.SchemaVersion &&
				project.Version >= allowed.MinProjectTeamVersion && project.Version <= allowed.MaxProjectTeamVersion {
				attachments++
			}
		}
		if attachments == 1 {
			matches++
		}
	}
	if matches != 1 {
		result := Readiness{State: StateActionRequired, Ownership: OwnershipPlugin, Reason: ReasonPluginUpdateRequired,
			Summary:    names.provider + " does not accept " + names.inBlueprint + " projects yet.",
			Detail:     "Updating it or this blueprint's plugin may add that. Ori never assumes it from matching names.",
			Dependency: dependency, Actions: []Action{ActionReviewPluginUpdate, ActionManagePlugins}, Generation: homePlugin.Generation}
		return &result
	}
	return nil
}

func hasHostFeature(installed plugin.InstalledPlugin, feature string) bool {
	if installed.WorkspaceSurfaces == nil {
		return false
	}
	for _, candidate := range installed.WorkspaceSurfaces.RequiresHostFeatures {
		if candidate == feature {
			return true
		}
	}
	return false
}

func rolesPackaged(skills []string, roles []workspace.AssistantProgramRoleSpec) bool {
	packaged := make(map[string]struct{}, len(skills))
	for _, skill := range skills {
		packaged[strings.ToLower(strings.TrimSpace(skill))] = struct{}{}
	}
	for _, role := range roles {
		for _, skill := range role.Skills {
			if _, found := packaged[strings.ToLower(strings.TrimSpace(skill))]; !found {
				return false
			}
		}
	}
	return true
}

func independentRoleCollision(home, project []workspace.AssistantProgramRoleSpec) bool {
	seen := make(map[string]struct{}, len(home))
	for _, role := range home {
		seen[role.ID] = struct{}{}
	}
	for _, role := range project {
		if _, exists := seen[role.ID]; exists {
			return true
		}
	}
	return false
}

// deriveDeclaredPluginDependencies resolves the plugin names a built-in or
// user template declares against the installed-plugin store. The declared name
// is the only part of the declaration that is honored; a declared source is
// reported as present and nothing more.
func deriveDeclaredPluginDependencies(template projecttemplates.Template, sources Sources, ownership Ownership) Readiness {
	names := declaredPluginNames(template.Tools)
	if len(names) == 0 {
		return Ready(ownership)
	}

	if sources.DependencyStateUnavailable {
		return dependencyStateUnknownReadiness(ownership, &Dependency{
			PluginName:     names[0],
			SourceDeclared: sourceDeclared(template.Tools, names[0]),
		})
	}

	// Report the first unsatisfied dependency in declared order: one blueprint
	// state means one next action, and resolving it re-derives the rest.
	var firstMissing, firstDisabled, firstBlocked *Dependency
	var blockedReason Reason
	var blockedGeneration uint64

	for _, name := range names {
		dependency := &Dependency{PluginName: name, SourceDeclared: sourceDeclared(template.Tools, name)}
		installed, present := sources.lookup(name)
		if !present {
			if firstMissing == nil {
				firstMissing = dependency
			}
			continue
		}
		dependency.Installed = true
		dependency.Enabled = installed.Enabled
		dependency.PluginVersion = strings.TrimSpace(installed.Version)

		if reason, blocked := pluginHardBlocker(installed); blocked {
			if firstBlocked == nil {
				firstBlocked, blockedReason, blockedGeneration = dependency, reason, installed.Generation
			}
			continue
		}
		if !installed.Enabled && firstDisabled == nil {
			firstDisabled = dependency
		}
	}

	// A hard blocker outranks a recoverable one: offering Install or Enable for
	// a plugin that cannot run on this machine is a loop, not a recovery.
	if firstBlocked != nil {
		return hardBlockerReadiness(blockedReason, firstBlocked, blockedGeneration)
	}
	if firstMissing != nil {
		return Readiness{
			State: StateActionRequired, Ownership: ownership, Reason: ReasonPluginInstallRequired,
			Summary:    "This blueprint needs a plugin that is not installed yet.",
			Detail:     "Installing it shows what it asks for first; you can cancel without changing anything.",
			Dependency: firstMissing, Actions: []Action{ActionInstallPlugin, ActionManagePlugins},
		}
	}
	if firstDisabled != nil {
		return Readiness{
			State: StateActionRequired, Ownership: ownership, Reason: ReasonPluginEnableRequired,
			Summary:    "This blueprint's plugin is installed but disabled.",
			Detail:     "Enabling it makes the blueprint usable. Nothing runs until you enable it.",
			Dependency: firstDisabled, Actions: []Action{ActionEnablePlugin, ActionManagePlugins},
		}
	}

	return Ready(ownership)
}

// pluginHardBlocker reports a condition no in-wizard action can clear:
// the plugin ships nothing runnable on this platform, or its declared surface
// protocol range does not include the running host. Both are rechecked here
// against the recorded contribution rather than trusted from install time,
// because a host upgrade can invalidate a record that was valid when written.
func pluginHardBlocker(installed plugin.InstalledPlugin) (Reason, bool) {
	for _, artifact := range installed.ResolvedArtifacts {
		if !artifact.Available {
			return ReasonPlatformUnsupported, true
		}
	}
	if surfaces := installed.WorkspaceSurfaces; surfaces != nil {
		maximum := max(surfaces.Protocol.Max, surfaces.Protocol.Min)
		if surfaces.Protocol.Min > plugin.SurfaceProtocolVersion || maximum < plugin.SurfaceProtocolVersion {
			return ReasonProtocolIncompatible, true
		}
	}
	return ReasonNone, false
}

func hardBlockerReadiness(reason Reason, dependency *Dependency, generation uint64) Readiness {
	readiness := Readiness{
		State: StateUnavailable, Ownership: OwnershipPlugin, Reason: reason,
		Dependency: dependency, Generation: generation,
		Actions: []Action{ActionManagePlugins, ActionChangeBlueprint},
	}
	switch reason {
	case ReasonPlatformUnsupported:
		readiness.Summary = "This blueprint's plugin does not support this computer."
		readiness.Detail = "The plugin ships nothing for this operating system and processor, so there is nothing to install or enable here."
	case ReasonProtocolIncompatible:
		readiness.Summary = "This blueprint's plugin is not compatible with this version of Ori."
		readiness.Detail = "The plugin was built for a different Ori plugin protocol. A plugin or Ori update may resolve it."
	}
	return readiness
}

func dependencyStateUnknownReadiness(ownership Ownership, dependency *Dependency) Readiness {
	return Readiness{
		State: StateActionRequired, Ownership: ownership, Reason: ReasonDependencyStateUnknown,
		Summary:    "Ori could not check this blueprint's plugin requirements.",
		Detail:     "Nothing has changed. Try again to re-check.",
		Dependency: dependency, Actions: []Action{ActionRetry, ActionManagePlugins},
	}
}

func manifestInvalidReadiness(ownership Ownership, diagnostic string) Readiness {
	readiness := Readiness{
		State: StateUnavailable, Ownership: ownership, Reason: ReasonManifestInvalid,
		Summary: "This blueprint's definition could not be read, so it cannot create a workspace.",
	}
	if ownership == OwnershipUser {
		// The only case where the user owns the file and can act on the text.
		readiness.Detail = "Fix this template's template.json, then reload the blueprint list."
		readiness.Diagnostic = diagnostic
		readiness.Actions = []Action{ActionEditTemplateManifest, ActionChangeBlueprint}
		return readiness
	}
	readiness.Detail = "This is a problem with the blueprint itself, not with anything you did. Choose a different blueprint for now."
	readiness.Actions = []Action{ActionChangeBlueprint}
	return readiness
}

// manifestDiagnostic returns the author-facing detail for a manifest that
// could not be fully understood, or "" when it was.
func manifestDiagnostic(template projecttemplates.Template) string {
	if template.HasInvalidVariant() {
		return template.TemplateVariantError
	}
	if template.HasInvalidGroupRequirement() {
		return template.GroupRequirementError
	}
	if template.HasInvalidStandaloneComposition() {
		return template.StandaloneCompositionError
	}
	if template.HasInvalidRuntimeRequirements() {
		return template.RuntimeRequirementsError
	}
	if template.HasInvalidSetupWizard() {
		return template.SetupWizardError
	}
	if template.HasInvalidIntakeRequirements() {
		return template.IntakeRequirementsError
	}
	if template.HasInvalidBundledSkills() {
		return template.BundledSkillsError
	}
	if template.HasInvalidAssistantProgram() {
		return template.AssistantProgramError
	}
	return ""
}

// unsatisfiedHostReference rechecks a fully-understood manifest's references
// against the running host's registries and returns a generic description of
// the first one the host cannot satisfy.
//
// The template loader already drops unknown references when it normalizes a
// manifest, so this rarely fires. It exists because the loader and the running
// host can be given different registry views, and a blueprint whose runtime
// adapter disappeared must not be offered as ready on the strength of a
// decision made somewhere else.
func variantSourceReadiness(template projecttemplates.Template) Readiness {
	dependency := &Dependency{}
	if template.TemplateVariant != nil {
		dependency.PluginName = template.TemplateVariant.Source.PluginID
		dependency.PluginVersion = template.TemplateVariant.Source.PluginVersion
	}
	readiness := Readiness{State: StateUnavailable, Ownership: OwnershipUser, Dependency: dependency}
	switch template.VariantSourceState {
	case projecttemplates.VariantSourceMissing:
		readiness.Reason = ReasonVariantSourceMissing
		readiness.Summary = "This variant's exact source plugin is not available."
		readiness.Detail = "Install the reviewed source or customize its current blueprint; Ori will not substitute another template."
		readiness.Actions = []Action{ActionManagePlugins, ActionChangeBlueprint}
	case projecttemplates.VariantSourceDisabled:
		readiness.Dependency.Installed = true
		readiness.State = StateActionRequired
		readiness.Reason = ReasonVariantSourceDisabled
		readiness.Summary = "This variant's source plugin is disabled."
		readiness.Detail = "Enable the exact source plugin, then retry. The variant remains unchanged."
		readiness.Actions = []Action{ActionManagePlugins, ActionRetry, ActionChangeBlueprint}
	case projecttemplates.VariantSourceChanged:
		readiness.Dependency.Installed = true
		readiness.Reason = ReasonVariantSourceChanged
		readiness.Summary = "This variant is pinned to an older source definition."
		readiness.Detail = "Create a new customization from the current source. Ori will not silently rebase this variant."
		readiness.Actions = []Action{ActionCustomizeTemplate, ActionChangeBlueprint}
	default:
		readiness.Dependency.Installed = true
		readiness.Dependency.Enabled = true
		readiness.Reason = ReasonVariantSourceIncompatible
		readiness.Summary = "This variant's source is incompatible with this version of Ori."
		readiness.Detail = "Manage the source plugin or choose another template."
		readiness.Actions = []Action{ActionManagePlugins, ActionChangeBlueprint}
	}
	return readiness
}

func unsatisfiedHostReference(template projecttemplates.Template, catalog projecttemplates.RuntimeCatalog) (string, bool) {
	if catalog == nil {
		return "", false
	}
	for _, install := range template.Capabilities {
		if !catalog.HasCapability(install.ID) {
			return "a workspace capability", true
		}
	}
	if template.HasRuntimeRequirements() {
		for _, requirement := range template.RuntimeRequirements.Requirements {
			if adapter := strings.TrimSpace(requirement.Adapter); adapter != "" && !catalog.HasRuntimeAdapter(adapter) {
				return "a runtime provider", true
			}
		}
	}
	return "", false
}

func contributesBlueprint(installed plugin.InstalledPlugin, blueprintID string) bool {
	want := strings.TrimSpace(blueprintID)
	for _, blueprint := range installed.ResolvedBlueprints {
		if strings.TrimSpace(blueprint.ID) == want {
			return true
		}
	}
	return false
}

func declaredPluginNames(tools projecttemplates.ToolDefaults) []string {
	names := make([]string, 0, len(tools.Plugins))
	seen := make(map[string]struct{}, len(tools.Plugins))
	for _, raw := range tools.Plugins {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}
	return names
}

// sourceDeclared reports whether the manifest names a source for this plugin.
// The source itself is never read here: knowing one exists is what lets the UI
// offer Install without asking the user to paste anything, and the trust
// preview is where the source is finally shown.
func sourceDeclared(tools projecttemplates.ToolDefaults, name string) bool {
	key := strings.ToLower(strings.TrimSpace(name))
	for declared, source := range tools.PluginSources {
		if strings.ToLower(strings.TrimSpace(declared)) == key && strings.TrimSpace(source) != "" {
			return true
		}
	}
	return false
}
