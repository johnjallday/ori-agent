package workspace

import (
	"strings"
	"time"
)

// This file holds the workspace-facing vocabulary for a blueprint's Setup
// Wizard: the ordered list of typed steps a template asks the user to satisfy
// after its workspace exists. Like DirectoryRequirement and AutomationRecipe
// (template_setup.go), the types live here rather than in
// internal/projecttemplates because a created workspace persists a normalized
// snapshot of the wizard as part of its template provenance, and both the
// template layer and the setup service must read the same vocabulary.
// internal/projecttemplates aliases these types so a manifest and the workspace
// snapshot it produces can never drift apart; the authoring rules (validation
// and normalization) live there, not here.
//
// Everything here is inert data. A wizard declaration selects nothing and runs
// nothing: it names an allowlisted step kind, an optional reference to a
// requirement the same manifest already declares, and an optional adapter *key*
// that the server resolves against a compiled registry. Deliberately absent —
// and never to be added — are fields that would let a manifest choose behavior
// rather than describe a requirement:
//
//   - no HTML, markdown, template, or custom-component fields
//   - no script, module path, command, or shell fragment
//   - no URL of any kind (remote component, webhook, or API endpoint)
//   - no request method, payload, or header
//
// Text fields are user-facing labels only. They are untrusted author input and
// must be rendered as text, never as markup or as instructions to an agent.

// SetupWizardSchemaVersion is the only setup-wizard schema this build
// understands. A manifest declaring a different version is rejected rather than
// coerced: a future incompatible schema gets a new version and an explicit
// migration.
const SetupWizardSchemaVersion = 1

// The version 1 allowlist of typed step kinds. A kind is a lookup key into
// server-owned rendering and behavior — never a code path a manifest supplies.
const (
	// SetupStepKindDirectory asks the user to choose one local folder declared
	// in directory_requirements, through the native picker.
	SetupStepKindDirectory = "directory"
	// SetupStepKindAutomationReview discloses the watcher and daily run declared
	// in automation_recipes for a directory, before either is activated.
	SetupStepKindAutomationReview = "automation_review"
	// SetupStepKindCapabilityConnect connects a declared abstract capability
	// (e.g. "calendar") to a connector the user chooses.
	SetupStepKindCapabilityConnect = "capability_connect"
	// SetupStepKindCapabilityConfigure configures an already-connected
	// capability (operation mapping, visibility, preferences).
	SetupStepKindCapabilityConfigure = "capability_configure"
	// SetupStepKindAccountLink links an existing connected account to this
	// workspace for a declared capability (e.g. a mailbox for "email").
	SetupStepKindAccountLink = "account_link"
	// SetupStepKindPluginReadiness resolves a declared plugin's install, enable,
	// and attachment state through explicit user actions.
	SetupStepKindPluginReadiness = "plugin_readiness"
	// SetupStepKindReadiness is a server-evaluated check that the blueprint's
	// prerequisites currently hold. It carries no reference of its own; the
	// adapter owns the question.
	SetupStepKindReadiness = "readiness"
	// SetupStepKindSummary recaps what was configured and what was deliberately
	// left out. It commits nothing.
	SetupStepKindSummary = "summary"
)

// SetupStepReferenceScope names which of a manifest's own declarations a step's
// requirement_key is resolved against. The scope is derived from the step kind,
// never sent by a client, so a reference can only ever point at data the same
// manifest already declared (see SetupStepReference).
type SetupStepReferenceScope string

const (
	// SetupStepReferenceNone marks a kind that references no requirement.
	SetupStepReferenceNone SetupStepReferenceScope = ""
	// SetupStepReferenceDirectory resolves against directory_requirements keys
	// (and, for automation_review, the automation_recipes entry keyed by that
	// same directory).
	SetupStepReferenceDirectory SetupStepReferenceScope = "directory"
	// SetupStepReferenceCapability resolves against capability_requirements keys.
	SetupStepReferenceCapability SetupStepReferenceScope = "capability"
	// SetupStepReferencePlugin resolves against the template's declared plugin
	// names (tools.plugins).
	SetupStepReferencePlugin SetupStepReferenceScope = "plugin"
)

// SetupStepKindSpec states what a step kind may and must reference. It is the
// single table both the authoring validator (internal/projecttemplates) and the
// runtime service consult, so "which references are legal for this kind" is
// answered in exactly one place.
type SetupStepKindSpec struct {
	// Kind is the normalized kind name.
	Kind string
	// ReferenceScope is the namespace requirement_key is resolved in.
	// SetupStepReferenceNone means the kind takes no requirement_key at all, and
	// declaring one is an authoring error rather than ignored data.
	ReferenceScope SetupStepReferenceScope
	// RequiresReference reports whether requirement_key must be present.
	RequiresReference bool
	// RequiresAdapter reports whether the step must name a registered adapter.
	// Kinds backed by a generic service (a folder picker, an automation
	// disclosure, a summary) may omit one; kinds that ask a domain whether it is
	// ready cannot.
	RequiresAdapter bool
}

// setupStepKindSpecs is the version 1 kind allowlist, in declaration order.
// Adding a kind here is the only way to add one: unknown kinds fail closed.
var setupStepKindSpecs = []SetupStepKindSpec{
	{Kind: SetupStepKindDirectory, ReferenceScope: SetupStepReferenceDirectory, RequiresReference: true},
	{Kind: SetupStepKindAutomationReview, ReferenceScope: SetupStepReferenceDirectory, RequiresReference: true},
	{Kind: SetupStepKindCapabilityConnect, ReferenceScope: SetupStepReferenceCapability, RequiresReference: true, RequiresAdapter: true},
	{Kind: SetupStepKindCapabilityConfigure, ReferenceScope: SetupStepReferenceCapability, RequiresReference: true, RequiresAdapter: true},
	{Kind: SetupStepKindAccountLink, ReferenceScope: SetupStepReferenceCapability, RequiresReference: true, RequiresAdapter: true},
	{Kind: SetupStepKindPluginReadiness, ReferenceScope: SetupStepReferencePlugin, RequiresReference: true, RequiresAdapter: true},
	{Kind: SetupStepKindReadiness, ReferenceScope: SetupStepReferenceNone, RequiresAdapter: true},
	{Kind: SetupStepKindSummary, ReferenceScope: SetupStepReferenceNone},
}

// ValidSetupStepKinds lists the version 1 step kinds, in declaration order.
func ValidSetupStepKinds() []string {
	out := make([]string, 0, len(setupStepKindSpecs))
	for _, spec := range setupStepKindSpecs {
		out = append(out, spec.Kind)
	}
	return out
}

// LookupSetupStepKind resolves a raw kind string against the allowlist. It
// trims and lower-cases, and reports false for anything unrecognized — the one
// place an unknown kind is turned away, so no caller can accidentally treat a
// hand-edited kind as a near-miss for a real one.
func LookupSetupStepKind(kind string) (SetupStepKindSpec, bool) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return SetupStepKindSpec{}, false
	}
	for _, spec := range setupStepKindSpecs {
		if spec.Kind == kind {
			return spec, true
		}
	}
	return SetupStepKindSpec{}, false
}

// SetupWizard is the normalized, portable definition of a blueprint's setup
// flow: an ordered list of typed steps. A workspace persists a snapshot of one
// (see TemplateProvenance) so later edits to the source template cannot change
// what an existing workspace is being asked to do.
type SetupWizard struct {
	// Version is the schema version of this declaration. Only
	// SetupWizardSchemaVersion is understood.
	Version int `json:"version"`
	// Title is the user-facing name of the flow (e.g. "Set up Downloads
	// Janitor"). Untrusted author text.
	Title string `json:"title"`
	// Steps are the wizard's steps in presentation order. Order is the
	// declaration order — there is no branching, no condition, and no jump
	// target a manifest can express.
	Steps []SetupWizardStep `json:"steps"`
}

// SetupWizardStep is one typed step. Its whole surface is: an identity, an
// allowlisted kind, whether it blocks completion, plain-language text, and at
// most one reference plus one adapter key. There is no field through which a
// manifest could supply markup, code, a command, or an endpoint.
type SetupWizardStep struct {
	// ID identifies the step within its wizard and is the key persisted
	// progress is recorded against. It must be unique within the wizard and
	// stable across compatible blueprint versions: changing it orphans a user's
	// recorded progress for that step.
	ID string `json:"id"`
	// Kind is one of the version 1 allowlisted kinds. It selects a server-owned
	// renderer and behavior; it is never itself behavior.
	Kind string `json:"kind"`
	// Required reports whether the step must pass server-evaluated readiness
	// before the wizard can complete. Authors must state it explicitly — the
	// authoring layer rejects an omitted value rather than defaulting a
	// permission-bearing step to optional.
	Required bool `json:"required"`
	// Title is the step heading. Optional; the shell falls back to a built-in
	// label for the kind. Untrusted author text.
	Title string `json:"title,omitempty"`
	// Description is a short explanation of what the step asks for. Optional.
	// Untrusted author text.
	Description string `json:"description,omitempty"`
	// Disclosure is the plain-language statement of what the user is granting or
	// enabling, shown immediately before the action that commits it. Optional
	// (a directory step's disclosure normally lives on the DirectoryRequirement
	// itself). Untrusted author text.
	Disclosure string `json:"disclosure,omitempty"`
	// RequirementKey references a requirement the same manifest already
	// declares. Which namespace it is resolved in is fixed by Kind — see
	// SetupStepKindSpec.ReferenceScope — so a step can never reach outside the
	// manifest's own declarations.
	RequirementKey string `json:"requirement_key,omitempty"`
	// Adapter names a domain adapter in Ori's compiled server-side registry. It
	// is a lookup key and nothing more: never a package path, module, binary,
	// URL, or anything else that could select code at runtime. An unregistered
	// name is rejected, not attempted.
	Adapter string `json:"adapter,omitempty"`
}

// SetupStepReference is a step's resolved pointer into its manifest's own
// declarations: the namespace (fixed by the step kind) plus the key. Callers
// resolve a reference instead of reading RequirementKey directly, so the
// namespace can never be chosen by the data — or by a client.
type SetupStepReference struct {
	Scope SetupStepReferenceScope
	Key   string
}

// Reference returns the step's resolved requirement reference. ok is false when
// the step's kind takes no reference, or when the kind is unknown — an
// unrecognized kind resolves to nothing rather than to a guess.
func (s SetupWizardStep) Reference() (SetupStepReference, bool) {
	spec, known := LookupSetupStepKind(s.Kind)
	if !known || spec.ReferenceScope == SetupStepReferenceNone {
		return SetupStepReference{}, false
	}
	key := strings.ToLower(strings.TrimSpace(s.RequirementKey))
	if key == "" {
		return SetupStepReference{}, false
	}
	return SetupStepReference{Scope: spec.ReferenceScope, Key: key}, true
}

// KindSpec resolves the step's kind against the allowlist.
func (s SetupWizardStep) KindSpec() (SetupStepKindSpec, bool) {
	return LookupSetupStepKind(s.Kind)
}

// Step returns the step with the given ID, if the wizard declares one.
func (w *SetupWizard) Step(id string) (SetupWizardStep, bool) {
	if w == nil {
		return SetupWizardStep{}, false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return SetupWizardStep{}, false
	}
	for _, step := range w.Steps {
		if step.ID == id {
			return step, true
		}
	}
	return SetupWizardStep{}, false
}

// RequiredStepIDs returns the IDs of the steps that block completion, in
// declaration order.
func (w *SetupWizard) RequiredStepIDs() []string {
	if w == nil {
		return nil
	}
	var out []string
	for _, step := range w.Steps {
		if step.Required {
			out = append(out, step.ID)
		}
	}
	return out
}

// IsEmpty reports whether the wizard declares no steps. An empty wizard is not
// a valid declaration — a workspace with one would be asked to complete nothing
// — so callers treat it as absent.
func (w *SetupWizard) IsEmpty() bool {
	return w == nil || len(w.Steps) == 0
}

// Setup lifecycle states. A workspace's setup status is workspace state, not
// dialog state: closing the wizard changes none of these.
const (
	// SetupWizardStateNotApplicable is a workspace whose blueprint declares no
	// wizard. It is derived, never persisted.
	SetupWizardStateNotApplicable = "not_applicable"
	// SetupWizardStateNotStarted is a wizard-enabled workspace the user has not
	// opened setup for yet.
	SetupWizardStateNotStarted = "not_started"
	// SetupWizardStateInProgress is setup begun but not finished. Dismissing the
	// dialog leaves the workspace here — never in a ready state.
	SetupWizardStateInProgress = "in_progress"
	// SetupWizardStateReady is every required step passing server-evaluated
	// readiness as of the last evaluation.
	SetupWizardStateReady = "ready"
	// SetupWizardStateNeedsAttention is setup that completed before but whose
	// live requirements have since regressed.
	SetupWizardStateNeedsAttention = "needs_attention"
)

// Per-step states.
const (
	// SetupStepStatusPending is a step not yet reached or acted on.
	SetupStepStatusPending = "pending"
	// SetupStepStatusActive is the step the wizard is currently on.
	SetupStepStatusActive = "active"
	// SetupStepStatusComplete is a step whose requirement the server confirmed.
	SetupStepStatusComplete = "complete"
	// SetupStepStatusBlocked is a step that was attempted and cannot pass yet.
	SetupStepStatusBlocked = "blocked"
	// SetupStepStatusOptionalSkipped is an optional step the user deliberately
	// skipped. It stays available later.
	SetupStepStatusOptionalSkipped = "optional_skipped"
)

// NormalizeSetupWizardState canonicalizes a persisted lifecycle state. A blank
// value means nothing has been recorded yet; anything unrecognized resolves to
// in_progress, never to ready. A workspace.json this build cannot read must err
// towards "setup is unfinished", because the opposite error would present
// unconfigured setup as done.
func NormalizeSetupWizardState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "":
		return SetupWizardStateNotStarted
	case SetupWizardStateNotApplicable:
		return SetupWizardStateNotApplicable
	case SetupWizardStateNotStarted:
		return SetupWizardStateNotStarted
	case SetupWizardStateReady:
		return SetupWizardStateReady
	case SetupWizardStateNeedsAttention:
		return SetupWizardStateNeedsAttention
	default:
		return SetupWizardStateInProgress
	}
}

// NormalizeSetupStepStatus canonicalizes a persisted step status. Anything
// unrecognized resolves to pending — an unreadable status must never read as
// complete.
func NormalizeSetupStepStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case SetupStepStatusActive:
		return SetupStepStatusActive
	case SetupStepStatusComplete:
		return SetupStepStatusComplete
	case SetupStepStatusBlocked:
		return SetupStepStatusBlocked
	case SetupStepStatusOptionalSkipped:
		return SetupStepStatusOptionalSkipped
	default:
		return SetupStepStatusPending
	}
}

// SetupStepProgress is the persisted state of one step, keyed by the step ID
// from the workspace's wizard snapshot.
type SetupStepProgress struct {
	// StepID matches SetupWizardStep.ID in the workspace's snapshot. A progress
	// entry whose ID is no longer declared is ignored, not guessed at.
	StepID string `json:"step_id"`
	// Status is one of the per-step states above.
	Status string `json:"status"`
	// UpdatedAt is when this step's status last changed.
	UpdatedAt time.Time `json:"updated_at,omitzero"`
	// CompletedAt is when the server first confirmed this step, or when the user
	// skipped it. Nil while it has never passed.
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// SelectedOption is the choice the user made on a step that offered one
	// (e.g. which of two supported modes a blueprint should run in). It is an
	// adapter-declared token, recorded because the choice outlives the click:
	// re-deriving it from whatever the domain happens to look like afterwards is
	// how "the user chose the simpler path" becomes indistinguishable from "the
	// user never finished".
	SelectedOption string `json:"selected_option,omitempty"`
}

// SetupWizardProgress is the workspace's authoritative setup state. It records
// what happened, never what the browser claimed: readiness is decided by
// re-evaluating the domain, and this is the durable result of that decision.
type SetupWizardProgress struct {
	// WizardVersion is the schema version of the snapshot this progress was
	// recorded against. Progress recorded against a different version is
	// reconciled explicitly rather than reinterpreted.
	WizardVersion int `json:"wizard_version"`
	// State is the lifecycle state (never not_applicable — a workspace with no
	// wizard has no progress record at all).
	State string `json:"state"`
	// CurrentStepID is the step to resume at: the first unresolved or failed
	// required step. Empty once the wizard is ready.
	CurrentStepID string `json:"current_step_id,omitempty"`
	// Steps is the per-step state, in the snapshot's declaration order.
	Steps []SetupStepProgress `json:"steps,omitempty"`
	// CreatedAt is when setup state was first recorded for this workspace.
	CreatedAt time.Time `json:"created_at,omitzero"`
	// UpdatedAt is the last change to any part of this record.
	UpdatedAt time.Time `json:"updated_at,omitzero"`
	// FirstOpenedAt is when the wizard was first opened. Nil means it has never
	// been shown — the one condition that lets it auto-open.
	FirstOpenedAt *time.Time `json:"first_opened_at,omitempty"`
	// DismissedAt is when the user last closed an unfinished wizard. Recorded
	// separately from readiness on purpose: dismissal suppresses auto-open and
	// nothing else. It can never make an incomplete workspace look ready.
	DismissedAt *time.Time `json:"dismissed_at,omitempty"`
	// CompletedAt is when every required step first passed. It survives a later
	// regression to needs_attention, which is why repair does not reopen the
	// blueprint's setup help task.
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// Step returns the persisted state of the given step ID.
func (p *SetupWizardProgress) Step(stepID string) (SetupStepProgress, bool) {
	if p == nil {
		return SetupStepProgress{}, false
	}
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return SetupStepProgress{}, false
	}
	for _, step := range p.Steps {
		if step.StepID == stepID {
			return step, true
		}
	}
	return SetupStepProgress{}, false
}

// StepStatus returns a step's normalized status, or pending when the step has
// no recorded state.
func (p *SetupWizardProgress) StepStatus(stepID string) string {
	step, ok := p.Step(stepID)
	if !ok {
		return SetupStepStatusPending
	}
	return NormalizeSetupStepStatus(step.Status)
}

// IsDismissed reports whether the user has closed an unfinished wizard, which
// suppresses auto-open. It says nothing about readiness.
func (p *SetupWizardProgress) IsDismissed() bool {
	return p != nil && p.DismissedAt != nil
}

// HasBeenOpened reports whether the wizard has ever been shown to the user.
func (p *SetupWizardProgress) HasBeenOpened() bool {
	return p != nil && p.FirstOpenedAt != nil
}

// CloneSetupWizardProgress returns a deep copy.
func CloneSetupWizardProgress(p *SetupWizardProgress) *SetupWizardProgress {
	if p == nil {
		return nil
	}
	cp := *p
	if len(p.Steps) > 0 {
		cp.Steps = make([]SetupStepProgress, len(p.Steps))
		for i, step := range p.Steps {
			cp.Steps[i] = step
			cp.Steps[i].CompletedAt = cloneTime(step.CompletedAt)
		}
	} else {
		cp.Steps = nil
	}
	cp.FirstOpenedAt = cloneTime(p.FirstOpenedAt)
	cp.DismissedAt = cloneTime(p.DismissedAt)
	cp.CompletedAt = cloneTime(p.CompletedAt)
	return &cp
}

func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	cp := *t
	return &cp
}

// GetSetupWizardProgress returns a copy of the workspace's persisted setup
// progress, or nil when none has been recorded.
func (w *Workspace) GetSetupWizardProgress() *SetupWizardProgress {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return CloneSetupWizardProgress(w.SetupWizardProgress)
}

// SetSetupWizardProgress records setup progress. Passing nil clears it.
func (w *Workspace) SetSetupWizardProgress(p *SetupWizardProgress) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if p == nil {
		w.SetupWizardProgress = nil
		w.UpdatedAt = time.Now()
		return
	}
	cp := CloneSetupWizardProgress(p)
	cp.State = NormalizeSetupWizardState(cp.State)
	for i := range cp.Steps {
		cp.Steps[i].Status = NormalizeSetupStepStatus(cp.Steps[i].Status)
	}
	now := time.Now()
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = now
	}
	cp.UpdatedAt = now
	w.SetupWizardProgress = cp
	w.UpdatedAt = now
}

// SetupWizardState returns the workspace's setup lifecycle state.
//
// It is derived from two independent facts — does the blueprint declare a
// wizard, and what has been recorded — so a workspace can never report `ready`
// on the strength of a progress record alone. A blueprint with no wizard is
// not_applicable; one with a wizard and no recorded progress is not_started.
func (w *Workspace) SetupWizardState() string {
	if !w.HasSetupWizard() {
		return SetupWizardStateNotApplicable
	}
	progress := w.GetSetupWizardProgress()
	if progress == nil {
		return SetupWizardStateNotStarted
	}
	return NormalizeSetupWizardState(progress.State)
}

// IsSetupWizardReady reports whether the last server evaluation found every
// required step passing.
func (w *Workspace) IsSetupWizardReady() bool {
	return w.SetupWizardState() == SetupWizardStateReady
}

// CloneSetupWizard returns a deep copy, so a caller holding a wizard cannot
// mutate a persisted snapshot through a shared slice. Steps are value types
// with no pointers or slices of their own, which is deliberate: a defensive
// copy of a wizard is exactly one slice copy, and it stays that way as long as
// no step gains a reference-typed field.
func CloneSetupWizard(w *SetupWizard) *SetupWizard {
	if w == nil {
		return nil
	}
	cp := *w
	if len(w.Steps) > 0 {
		cp.Steps = make([]SetupWizardStep, len(w.Steps))
		copy(cp.Steps, w.Steps)
	} else {
		cp.Steps = nil
	}
	return &cp
}
