package workspacecapability

import (
	"fmt"
	"sync"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Runtime is the compiled behavior behind a capability. It is bound to the
// registry by the server at startup — never named, supplied, or selected by a
// workspace file. A capability with no bound runtime stays fully visible as
// catalog metadata; it simply reports StatusUnavailable instead of executing.
//
// Capability-specific behavior beyond status is exposed through the narrow
// optional interfaces below, which callers type-assert. Keeping them separate
// means a capability implements only what it actually does.
type Runtime interface {
	// CapabilityStatus derives current health for one workspace (FR-6). It must
	// never consult a persisted status string.
	CapabilityStatus(workspaceID string) (Status, error)
}

// Installer is an optional Runtime capability invoked when an install record is
// created. File Janitor deliberately implements nothing here in v1: installing
// must not request folder access, start automation, or create resources
// (FR-20, FR-23).
type Installer interface {
	OnCapabilityInstall(workspaceID string) error
}

// Remover is an optional Runtime capability invoked during uninstall, after
// automation has been stopped and before the install record is dropped
// (FR-26).
type Remover interface {
	OnCapabilityRemove(workspaceID string) error
}

// AutomationController is an optional Runtime capability that can stop a
// capability's registered watcher and schedule. Removal and revoke must stop
// automation before releasing access so no event races the teardown (FR-26,
// FR-58).
type AutomationController interface {
	StopCapabilityAutomation(workspaceID string) error
}

// Resolved pairs one persisted install record with the compiled definition it
// names.
//
// This is the fail-closed boundary (FR-14, FR-145). A record whose ID is not in
// the compiled allowlist resolves with Available=false and a placeholder
// definition carrying nothing but the ID: it stays visible to the user as
// "installed but unavailable in this build" rather than disappearing silently,
// and no code path can execute on its behalf.
type Resolved struct {
	// Record is the persisted install record, normalized.
	Record workspace.InstalledCapability
	// Definition is the compiled definition, or a metadata-only placeholder
	// when Available is false.
	Definition Definition
	// Available reports whether the ID resolved to a compiled definition.
	Available bool
	// Unavailable explains why the record did not resolve. Empty when
	// Available is true.
	Unavailable string
	// NeedsMigration reports that the record's version differs from this
	// build's definition version, so capability-specific migration may be due
	// (FR-13). Always false when Available is false.
	NeedsMigration bool
}

// Registry is the server-owned allowlist of built-in capability definitions
// (FR-2). It is safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	defs     map[string]Definition
	order    []string
	runtimes map[string]Runtime
}

// NewRegistry builds a registry from compiled definitions. It rejects malformed
// and duplicate definitions so a build cannot start with an ambiguous
// allowlist.
//
// There is deliberately no constructor that accepts JSON, a file path, or a
// workspace record: definitions come from compiled Go values only.
func NewRegistry(defs ...Definition) (*Registry, error) {
	r := &Registry{
		defs:     make(map[string]Definition, len(defs)),
		runtimes: make(map[string]Runtime, len(defs)),
	}
	for _, def := range defs {
		if err := r.register(def); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// NewBuiltinRegistry builds the registry from BuiltinDefinitions. The error is
// only possible if a compiled definition is malformed, which is a programming
// error rather than a runtime condition.
func NewBuiltinRegistry() (*Registry, error) {
	return NewRegistry(BuiltinDefinitions()...)
}

func (r *Registry) register(def Definition) error {
	if err := def.Validate(); err != nil {
		return err
	}
	id := workspace.NormalizeCapabilityID(def.ID)
	if _, exists := r.defs[id]; exists {
		return fmt.Errorf("capability definition %q registered twice", id)
	}
	normalized := def.Clone()
	normalized.ID = id
	r.defs[id] = normalized
	r.order = append(r.order, id)
	return nil
}

// Definition returns the compiled definition for id, if this build has one.
func (r *Registry) Definition(id string) (Definition, bool) {
	key := workspace.NormalizeCapabilityID(id)
	if key == "" {
		return Definition{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.defs[key]
	if !ok {
		return Definition{}, false
	}
	return def.Clone(), true
}

// Definitions returns every compiled definition in registration order.
func (r *Registry) Definitions() []Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Definition, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.defs[id].Clone())
	}
	return out
}

// Has reports whether id is in the compiled allowlist.
func (r *Registry) Has(id string) bool {
	_, ok := r.Definition(id)
	return ok
}

// BindRuntime attaches compiled behavior to an already-registered definition.
// Called by the server during startup wiring.
//
// Binding is separate from registration on purpose: a capability whose runtime
// fails to construct still appears in the catalog with accurate metadata and an
// unavailable status, instead of taking the workspace or the server down with
// it (FR-145).
func (r *Registry) BindRuntime(id string, runtime Runtime) error {
	key := workspace.NormalizeCapabilityID(id)
	if key == "" {
		return fmt.Errorf("capability runtime: id is required")
	}
	if runtime == nil {
		return fmt.Errorf("capability runtime %q: runtime is required", key)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.defs[key]; !ok {
		return fmt.Errorf("capability runtime %q: no compiled definition with that id", key)
	}
	r.runtimes[key] = runtime
	return nil
}

// Runtime returns the bound runtime for id, if one has been wired.
func (r *Registry) Runtime(id string) (Runtime, bool) {
	key := workspace.NormalizeCapabilityID(id)
	if key == "" {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	runtime, ok := r.runtimes[key]
	return runtime, ok
}

// Resolve matches one persisted install record against the compiled allowlist.
// It never returns an error: an unresolvable record is reported as unavailable
// metadata, because a workspace must keep loading even when it carries an
// install this build does not know about (FR-145).
func (r *Registry) Resolve(record workspace.InstalledCapability) Resolved {
	id := workspace.NormalizeCapabilityID(record.ID)
	record.ID = id

	if id == "" {
		return Resolved{
			Record:      record,
			Definition:  unavailableDefinition(id),
			Unavailable: "install record has no capability id",
		}
	}

	def, ok := r.Definition(id)
	if !ok {
		return Resolved{
			Record:      record,
			Definition:  unavailableDefinition(id),
			Unavailable: fmt.Sprintf("capability %q is not available in this version of Ori", id),
		}
	}

	return Resolved{
		Record:         record,
		Definition:     def,
		Available:      true,
		NeedsMigration: record.Version != def.Version,
	}
}

// ResolveAll resolves every record on a workspace, preserving order. Records
// that do not resolve are included as unavailable rather than dropped, so the
// user can see what is installed even when this build cannot run it.
func (r *Registry) ResolveAll(records []workspace.InstalledCapability) []Resolved {
	if len(records) == 0 {
		return nil
	}
	out := make([]Resolved, 0, len(records))
	for _, record := range records {
		out = append(out, r.Resolve(record))
	}
	return out
}

// Status derives current health for an installed capability.
//
// It resolves fail-closed first, then consults the bound runtime. A record that
// does not resolve, or a capability with no wired runtime, reports
// StatusUnavailable — it does not fall back to a persisted status string, and
// it does not attempt to load anything (FR-6, FR-14).
func (r *Registry) Status(record workspace.InstalledCapability, workspaceID string) (Status, Resolved, error) {
	resolved := r.Resolve(record)
	if !resolved.Available {
		return Status{State: StatusUnavailable, Detail: resolved.Unavailable}, resolved, nil
	}

	runtime, ok := r.Runtime(resolved.Definition.ID)
	if !ok {
		return Status{
			State:  StatusUnavailable,
			Detail: "This capability is installed but not currently running.",
		}, resolved, nil
	}

	status, err := runtime.CapabilityStatus(workspaceID)
	if err != nil {
		// A capability's health check failing must not fail the workspace: the
		// caller renders "needs attention" and everything else keeps loading.
		return Status{
			State:  StatusNeedsAttention,
			Detail: "File Janitor could not report its status.",
		}, resolved, err
	}
	return status, resolved, nil
}

// unavailableDefinition builds the metadata-only placeholder shown for an
// install record this build cannot resolve. It carries the persisted ID and
// nothing else — no runtime, no routes, no console, no companion — so there is
// nothing for an unknown ID to activate.
func unavailableDefinition(id string) Definition {
	display := id
	if display == "" {
		display = "Unknown capability"
	}
	return Definition{
		ID:      id,
		Version: 0,
		Display: Display{
			Name:    display,
			Summary: "This capability is not available in this version of Ori.",
		},
	}
}
