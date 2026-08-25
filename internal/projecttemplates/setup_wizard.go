package projecttemplates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	workspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// This file owns the authoring rules for a blueprint's optional `setup_wizard`
// block: what a manifest may declare, and what happens to a declaration that
// breaks the rules. The portable types it produces live in internal/workspace
// (setup_wizard.go) so a manifest and the snapshot a created workspace persists
// cannot drift apart.
//
// One rule differs deliberately from the rest of this package. Capability,
// directory, and automation declarations normalize leniently: a hand-edited
// manifest degrades to less data rather than failing to load. A setup wizard
// fails closed instead. Its steps grant folder access, connect accounts, and
// change permissions, so "I could not understand this declaration" must never
// resolve to "run the part I did understand" — the whole wizard is rejected
// with a diagnostic and the blueprint offers no setup at all.

// ErrInvalidSetupWizard reports a `setup_wizard` declaration that cannot be
// used: an unsupported version, a blank or duplicate step ID, an unknown kind
// or adapter, a reference to something the manifest does not declare, or a
// reference on a kind that takes none.
var ErrInvalidSetupWizard = errors.New("invalid setup wizard")

// SetupWizard is the normalized wizard a template declares. See
// workspace.SetupWizard: inert data, no executable or custom-render fields.
type SetupWizard = workspace.SetupWizard

// SetupWizardStep is one typed step of a declared wizard.
type SetupWizardStep = workspace.SetupWizardStep

// ValidSetupWizardAdapters is the set of domain adapter IDs a manifest may
// name. It is the authoring half of the adapter contract: the compiled
// server-side registry (internal/setupwizard) is what actually resolves an
// adapter at runtime, and a parity test keeps the two lists identical. A name
// here is a lookup key and nothing more — never a package, path, or command.
// Both File Janitor keys are listed because both are authored today: the
// Downloads preset still names the legacy `downloads_janitor` (and every
// workspace mid-setup persisted it), while the generic File Janitor blueprint
// names the canonical `file_janitor`. One compiled adapter serves both — see
// downloadsjanitor.SetupAdapter.Aliases.
var ValidSetupWizardAdapters = []string{
	"downloads_janitor",
	"file_janitor",
	"calendar_ops",
	"email_ops",
	"github_ops",
}

// Bounds on author-supplied wizard text. A manifest is local and hand-written,
// so these exist to keep a persisted workspace snapshot and the setup dialog
// bounded, not to defend a trust boundary.
const (
	maxSetupWizardTitleLength = 200
	maxSetupWizardTextLength  = 2000
	maxSetupWizardSteps       = 24
	maxSetupStepIDLength      = 64
)

// setupStepIDPattern constrains a step ID to a stable, URL-safe slug. Progress
// is recorded against these IDs and they travel in API payloads, so they stay
// boring on purpose.
var setupStepIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// setupWizardDecl is the raw on-disk shape of the `setup_wizard` block. It
// exists separately from workspace.SetupWizard for one reason: `required` must
// be *stated*, and only a pointer can tell "the author wrote false" from "the
// author wrote nothing". A step that grants access must never default to
// optional because a field was forgotten.
type setupWizardDecl struct {
	Version int                   `json:"version"`
	Title   string                `json:"title"`
	Steps   []setupWizardStepDecl `json:"steps"`
}

type setupWizardStepDecl struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	Required       *bool  `json:"required"`
	Title          string `json:"title,omitempty"`
	Description    string `json:"description,omitempty"`
	Disclosure     string `json:"disclosure,omitempty"`
	RequirementKey string `json:"requirement_key,omitempty"`
	Adapter        string `json:"adapter,omitempty"`
}

// setupWizardScope is the set of declarations a wizard's steps may point at:
// everything the *same manifest* declares, and nothing else. A step cannot
// reach a directory, capability, or plugin the blueprint has not asked for.
type setupWizardScope struct {
	directories         map[string]bool
	automated           map[string]bool
	capabilities        map[string]bool
	plugins             map[string]bool
	runtimeRequirements map[string]bool
}

// templateSetupWizardScope builds the reference scope from a template's other
// declarations. Callers pass the *normalized* requirements so key casing
// matches what a step's reference resolves to.
func templateSetupWizardScope(dirs []DirectoryRequirement, recipes []AutomationRecipe, caps []CapabilityRequirement, plugins []string, runtimeContracts ...*RuntimeRequirementsContract) setupWizardScope {
	scope := setupWizardScope{
		directories:         make(map[string]bool, len(dirs)),
		automated:           make(map[string]bool, len(recipes)),
		capabilities:        make(map[string]bool, len(caps)),
		plugins:             make(map[string]bool, len(plugins)),
		runtimeRequirements: make(map[string]bool),
	}
	for _, dir := range dirs {
		scope.directories[strings.ToLower(strings.TrimSpace(dir.Key))] = true
	}
	for _, recipe := range recipes {
		scope.automated[strings.ToLower(strings.TrimSpace(recipe.DirectoryKey))] = true
	}
	for _, req := range caps {
		scope.capabilities[strings.ToLower(strings.TrimSpace(req.Key))] = true
	}
	for _, name := range plugins {
		scope.plugins[strings.ToLower(strings.TrimSpace(name))] = true
	}
	for _, contract := range runtimeContracts {
		if contract == nil {
			continue
		}
		for _, requirement := range contract.Requirements {
			key := workspace.NormalizeRuntimeIdentifier(requirement.Key)
			if key != "" {
				scope.runtimeRequirements[key] = true
			}
		}
	}
	return scope
}

// has reports whether the scope declares key in the given namespace.
func (s setupWizardScope) has(scope workspace.SetupStepReferenceScope, key string) bool {
	switch scope {
	case workspace.SetupStepReferenceDirectory:
		return s.directories[key]
	case workspace.SetupStepReferenceCapability:
		return s.capabilities[key]
	case workspace.SetupStepReferencePlugin:
		return s.plugins[key]
	case workspace.SetupStepReferenceRuntimeRequirement:
		return s.runtimeRequirements[key]
	default:
		return false
	}
}

// referenceNoun names a scope the way an author declared it, so a diagnostic
// points at the manifest key to fix.
func referenceNoun(scope workspace.SetupStepReferenceScope) string {
	switch scope {
	case workspace.SetupStepReferenceDirectory:
		return "directory_requirements"
	case workspace.SetupStepReferenceCapability:
		return "capability_requirements"
	case workspace.SetupStepReferencePlugin:
		return "tools.plugins"
	case workspace.SetupStepReferenceRuntimeRequirement:
		return "runtime_requirements.requirements"
	default:
		return "requirement"
	}
}

// isKnownSetupWizardAdapter reports whether name is an authorable adapter ID.
func isKnownSetupWizardAdapter(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	return slices.Contains(ValidSetupWizardAdapters, name)
}

// normalizeSetupWizard reads a manifest's raw `setup_wizard` value and returns
// the normalized, portable wizard — or an error and no wizard at all.
//
// Three things make this fail closed where the rest of the package heals:
//
//  1. Unknown fields are rejected, not ignored. A manifest carrying
//     `component_url`, `render_html`, or `command` is an author asking for
//     behavior this contract does not offer; silently dropping the field would
//     ship a wizard the author believes does something it does not.
//  2. A single bad step invalidates the whole wizard. Steps grant folder
//     access and connect accounts, so a partially-understood flow is worse
//     than none.
//  3. The raw value is decoded separately from the rest of the manifest, so a
//     malformed wizard produces an actionable wizard diagnostic instead of
//     discarding the template's name, tasks, and agents along with it.
//
// The returned text fields are trimmed author input and nothing more. They are
// untrusted: callers render them as text, and never as markup or as
// instructions to an agent.
func normalizeSetupWizard(raw json.RawMessage, scope setupWizardScope) (*SetupWizard, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}

	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var decl setupWizardDecl
	if err := dec.Decode(&decl); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSetupWizard, err)
	}
	if err := validateSetupWizard(&decl, scope); err != nil {
		return nil, err
	}

	wizard := &SetupWizard{
		Version: decl.Version,
		Title:   strings.TrimSpace(decl.Title),
		Steps:   make([]SetupWizardStep, 0, len(decl.Steps)),
	}
	for _, step := range decl.Steps {
		// Validation already resolved the kind, so the lookup cannot fail here.
		spec, _ := workspace.LookupSetupStepKind(step.Kind)
		wizard.Steps = append(wizard.Steps, SetupWizardStep{
			ID:             strings.ToLower(strings.TrimSpace(step.ID)),
			Kind:           spec.Kind,
			Required:       *step.Required,
			Title:          strings.TrimSpace(step.Title),
			Description:    strings.TrimSpace(step.Description),
			Disclosure:     strings.TrimSpace(step.Disclosure),
			RequirementKey: strings.ToLower(strings.TrimSpace(step.RequirementKey)),
			Adapter:        strings.ToLower(strings.TrimSpace(step.Adapter)),
		})
	}
	return wizard, nil
}

// validateSetupWizard enforces every authoring invariant on a raw
// (pre-normalization) declaration, checked against what the same manifest
// declares. A nil declaration is valid: `setup_wizard` is optional, and a
// blueprint without one keeps its existing behavior.
//
// Unlike the lenient normalizers elsewhere in this package, the result is
// all-or-nothing. The caller either gets a wizard every step of which is
// understood, or an error naming the first problem and no wizard at all.
func validateSetupWizard(decl *setupWizardDecl, scope setupWizardScope) error {
	if decl == nil {
		return nil
	}
	if decl.Version <= 0 {
		return fmt.Errorf("%w: version is required and must be a positive integer", ErrInvalidSetupWizard)
	}
	if decl.Version != workspace.SetupWizardSchemaVersion {
		return fmt.Errorf("%w: unsupported version %d (this build understands version %d)", ErrInvalidSetupWizard, decl.Version, workspace.SetupWizardSchemaVersion)
	}
	title := strings.TrimSpace(decl.Title)
	if title == "" {
		return fmt.Errorf("%w: title is required", ErrInvalidSetupWizard)
	}
	if err := validateSetupWizardText("title", title, maxSetupWizardTitleLength); err != nil {
		return err
	}
	if len(decl.Steps) == 0 {
		return fmt.Errorf("%w: a wizard must declare at least one step", ErrInvalidSetupWizard)
	}
	if len(decl.Steps) > maxSetupWizardSteps {
		return fmt.Errorf("%w: %d steps exceeds the maximum of %d", ErrInvalidSetupWizard, len(decl.Steps), maxSetupWizardSteps)
	}

	seen := make(map[string]bool, len(decl.Steps))
	for i, step := range decl.Steps {
		id, err := validateSetupStepID(i, step.ID)
		if err != nil {
			return err
		}
		if seen[id] {
			return fmt.Errorf("%w: duplicate step id %q; ids identify persisted progress and must be unique", ErrInvalidSetupWizard, id)
		}
		seen[id] = true
		if err := validateSetupStepDecl(id, step, scope); err != nil {
			return err
		}
	}
	return nil
}

// validateSetupStepID enforces a non-blank, stable, slug-shaped identifier.
// Progress is recorded against it and it must survive a blueprint update, so a
// path-like or whitespace-laden id is refused at authoring time rather than
// becoming an orphaned progress key later.
func validateSetupStepID(index int, raw string) (string, error) {
	id := strings.ToLower(strings.TrimSpace(raw))
	if id == "" {
		return "", fmt.Errorf("%w: step %d is missing an id", ErrInvalidSetupWizard, index+1)
	}
	if len(id) > maxSetupStepIDLength {
		return "", fmt.Errorf("%w: step id %q is longer than %d characters", ErrInvalidSetupWizard, id, maxSetupStepIDLength)
	}
	if !setupStepIDPattern.MatchString(id) {
		return "", fmt.Errorf("%w: step id %q must be lower-case letters, digits, %q, or %q and start with a letter or digit", ErrInvalidSetupWizard, raw, "-", "_")
	}
	return id, nil
}

// validateSetupStepDecl checks one step against the version 1 kind allowlist
// and the manifest's own declarations.
func validateSetupStepDecl(id string, step setupWizardStepDecl, scope setupWizardScope) error {
	spec, known := workspace.LookupSetupStepKind(step.Kind)
	if !known {
		return fmt.Errorf("%w: step %q has unknown kind %q; supported kinds are %s", ErrInvalidSetupWizard, id, strings.TrimSpace(step.Kind), strings.Join(workspace.ValidSetupStepKinds(), ", "))
	}
	if step.Required == nil {
		return fmt.Errorf("%w: step %q must state %q explicitly; a step that grants access cannot default to optional", ErrInvalidSetupWizard, id, "required")
	}

	for field, text := range map[string]string{"title": step.Title, "description": step.Description, "disclosure": step.Disclosure} {
		if err := validateSetupWizardText(fmt.Sprintf("step %q %s", id, field), text, maxSetupWizardTextLength); err != nil {
			return err
		}
	}

	key := strings.ToLower(strings.TrimSpace(step.RequirementKey))
	switch {
	case spec.ReferenceScope == workspace.SetupStepReferenceNone:
		// A reference on a kind that resolves none is an authoring mistake, not
		// ignorable noise: the author believes the step is scoped to something
		// it will never look at.
		if key != "" {
			return fmt.Errorf("%w: step %q of kind %q takes no requirement_key, but declares %q", ErrInvalidSetupWizard, id, spec.Kind, key)
		}
	case key == "":
		if spec.RequiresReference {
			return fmt.Errorf("%w: step %q of kind %q must name a %s key in requirement_key", ErrInvalidSetupWizard, id, spec.Kind, referenceNoun(spec.ReferenceScope))
		}
	case !scope.has(spec.ReferenceScope, key):
		return fmt.Errorf("%w: step %q references %q, which this template does not declare in %s", ErrInvalidSetupWizard, id, key, referenceNoun(spec.ReferenceScope))
	}

	// An automation review with nothing to review would ask the user to approve
	// a watcher and schedule the blueprint never requested.
	if spec.Kind == workspace.SetupStepKindAutomationReview && key != "" && !scope.automated[key] {
		return fmt.Errorf("%w: step %q reviews automation for %q, but the template declares no automation_recipes entry for it", ErrInvalidSetupWizard, id, key)
	}

	adapter := strings.ToLower(strings.TrimSpace(step.Adapter))
	if spec.ReferenceScope == workspace.SetupStepReferenceRuntimeRequirement && adapter != "" {
		return fmt.Errorf("%w: step %q of kind %q takes no adapter; its runtime requirement owns the compiled adapter key", ErrInvalidSetupWizard, id, spec.Kind)
	}
	if spec.Kind == workspace.SetupStepKindRuntimeMode && adapter != "" {
		return fmt.Errorf("%w: step %q of kind %q takes no adapter; the runtime service owns mode selection", ErrInvalidSetupWizard, id, spec.Kind)
	}
	if adapter == "" {
		if spec.RequiresAdapter {
			return fmt.Errorf("%w: step %q of kind %q must name a registered adapter", ErrInvalidSetupWizard, id, spec.Kind)
		}
		return nil
	}
	if !isKnownSetupWizardAdapter(adapter) {
		return fmt.Errorf("%w: step %q names unregistered adapter %q; registered adapters are %s", ErrInvalidSetupWizard, id, adapter, strings.Join(ValidSetupWizardAdapters, ", "))
	}
	return nil
}

// validateSetupWizardText rejects author text that cannot be displayed as
// written: embedded NUL or other control characters, and text long enough to
// bloat every workspace snapshot made from the blueprint. It deliberately does
// not inspect for markup or instructions — those are handled by rendering the
// text as text, never by trying to sanitize it here.
func validateSetupWizardText(field, text string, max int) error {
	if len(text) > max {
		return fmt.Errorf("%w: %s is longer than %d characters", ErrInvalidSetupWizard, field, max)
	}
	for _, r := range text {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: %s contains a control character", ErrInvalidSetupWizard, field)
		}
	}
	return nil
}
