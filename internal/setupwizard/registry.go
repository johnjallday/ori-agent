// Package setupwizard runs a blueprint's Setup Wizard: the ordered, typed
// steps a workspace must satisfy after it is created. It owns the lifecycle
// (open, resume, dismiss, skip, complete, repair) and the readiness decisions;
// the domain work itself belongs to registered adapters, which call the
// existing Downloads Janitor, Calendar, email, plugin, and REAPER services
// rather than reimplementing them.
//
// Two boundaries define this package:
//
//   - The wizard is data the *workspace* persisted, not data a caller sends. A
//     request names a workspace and a step ID; everything else — the step's
//     kind, what it references, which adapter serves it — is read from the
//     workspace's own snapshot. There is no request field through which a
//     client could name an adapter, a filesystem path, a connector operation, a
//     plugin source, or an endpoint.
//   - Readiness is decided here, by asking an adapter, and never accepted from
//     a client. A browser can report that it finished a step; that report
//     changes nothing until the server agrees.
package setupwizard

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Errors callers distinguish. They are deliberately coarse: a caller learns
// that a step cannot be resolved, not which internal lookup failed.
var (
	// ErrNoWizard reports a workspace whose blueprint declares no setup wizard.
	ErrNoWizard = errors.New("workspace has no setup wizard")
	// ErrUnknownStep reports a step ID the workspace's snapshot does not declare.
	ErrUnknownStep = errors.New("unknown setup step")
	// ErrUnsupportedSnapshot reports a persisted wizard this build cannot run:
	// a schema version it does not understand, a kind it does not implement, or
	// a reference that no longer resolves. Nothing is executed for it.
	ErrUnsupportedSnapshot = errors.New("unsupported setup wizard snapshot")
	// ErrUnknownAdapter reports a step naming an adapter that is not registered
	// (or not wired in this process). The step is blocked, never attempted.
	ErrUnknownAdapter = errors.New("unknown setup adapter")
	// ErrInvalidAction reports an action a step does not offer.
	ErrInvalidAction = errors.New("invalid setup action")
	// ErrStepRejected reports that an adapter refused a confirmed action for
	// a reason the user can do something about -- a chosen option that turns
	// out not to work, rather than a malformed request or a broken service.
	//
	// It is the one domain error whose text is shown to the user, so
	// returning it is a promise about that text: it must be plain language
	// naming no path, address, credential, or connector internal, exactly as
	// StepReadiness.Summary must be. Every other domain failure is reported
	// generically, because a raw error can leak any of those.
	//
	// Confirm's returned readiness cannot carry this: the service discards it
	// and re-evaluates, which is correct (the persisted state must reflect
	// what is true now, not what an adapter asserted) but leaves an error as
	// the only channel for "your choice was refused, and here is why".
	ErrStepRejected = errors.New("setup step rejected")
)

// Safe error categories. They are stable, non-identifying strings: safe to log,
// safe to report to a client, and safe to count in analytics. A category never
// carries a path, account, calendar name, filename, or connector credential.
const (
	// ErrorCategoryNotConfigured is a requirement the user has not satisfied yet.
	ErrorCategoryNotConfigured = "not_configured"
	// ErrorCategoryUnavailable is a domain service that is not wired or not
	// reachable in this process.
	ErrorCategoryUnavailable = "adapter_unavailable"
	// ErrorCategoryUnsupported is a persisted step this build cannot run.
	ErrorCategoryUnsupported = "unsupported_step"
	// ErrorCategoryPermissionRequired is a requirement blocked pending an
	// explicit user grant.
	ErrorCategoryPermissionRequired = "permission_required"
	// ErrorCategoryDomainError is a domain operation that failed for a reason
	// the adapter could not classify further.
	ErrorCategoryDomainError = "domain_error"
)

// StepReadiness is an adapter's verdict on one step: does its requirement hold
// right now?
//
// It is a *current* answer, never a memory of a past one. The service asks
// again at every boundary that matters, which is what lets a workspace that was
// ready yesterday report needs_attention today without anything having been
// written in between.
type StepReadiness struct {
	// Ready reports whether the step's requirement currently holds.
	Ready bool
	// Blocked reports that the step was attempted and cannot pass yet — as
	// opposed to simply not having been done. It drives the blocked state and
	// the repair copy.
	Blocked bool
	// Summary is a short plain-language statement for the user ("Folder chosen",
	// "Gmail is connected but this workspace has no mailbox linked"). It must
	// not contain a local path, address, account identifier, calendar name, or
	// filename.
	Summary string
	// ErrorCategory is one of the stable categories above when the step cannot
	// pass. Empty when Ready.
	ErrorCategory string
	// Options are the choices this step offers, when it offers any (e.g. REAPER's
	// file-only versus Ori-assisted modes). The IDs are the only option values a
	// client may send back.
	Options []StepOption
}

// StepOption is one adapter-declared choice on a step.
//
// This type is serialized straight into the status payload, so the tags are
// load-bearing: without them the browser receives Go field names and renders an
// empty choice while every server-side test still passes.
type StepOption struct {
	// ID is the token a client echoes back to choose this option. Short and
	// opaque: never a path, URL, or command.
	ID string `json:"id"`
	// Label is the user-facing choice text.
	Label string `json:"label"`
	// Description explains what choosing it does and does not do.
	Description string `json:"description,omitempty"`
	// Selected reports whether this option is the workspace's current choice.
	Selected bool `json:"selected,omitempty"`
}

// StepRequest is everything an adapter is given about a step. Every field is
// derived from the workspace's persisted snapshot — none of it is client input.
type StepRequest struct {
	// WorkspaceID is the workspace being set up.
	WorkspaceID string
	// Step is the step as the workspace recorded it at creation time.
	Step workspace.SetupWizardStep
	// Directory is the directory requirement the step references, when its kind
	// resolves in the directory namespace.
	Directory *workspace.DirectoryRequirement
	// Automation is the automation the blueprint requested for that directory,
	// when it requested any.
	Automation *workspace.AutomationRecipe
	// Capability is the capability requirement the step references, when its
	// kind resolves in the capability namespace.
	Capability *workspace.CapabilityRequirement
	// Plugin is the declared plugin name the step references, and PluginSource
	// the install source the blueprint declared for it (empty when none).
	Plugin       string
	PluginSource string
	// SelectedOption is the option the user previously chose on this step, when
	// it offers a choice. It comes from the workspace's persisted progress, not
	// from the caller.
	SelectedOption string
	// Selections is every choice recorded across this wizard, keyed by step ID.
	// A later step usually has to honor a decision made on an earlier one — the
	// step that asks how REAPER should work is not the step that then checks the
	// prerequisites for that answer — and this is where it reads it.
	Selections map[string]string
}

// Choice returns the option recorded on stepID, or "" when that step has no
// choice recorded. Adapters should prefer it over indexing Selections, since a
// wizard whose progress predates any recorded choice has no map at all.
func (r StepRequest) Choice(stepID string) string {
	if r.Selections == nil {
		return ""
	}
	return r.Selections[stepID]
}

// StepAction is the only thing a client may ask the server to do to a step.
//
// Its shape is the security boundary. There is no path, URL, operation name,
// connector, plugin source, endpoint, method, or payload: the step's identity
// comes from the snapshot, and Option is a short token the adapter itself
// declared in a previous StepReadiness. Domain mutations that genuinely need
// richer input (choosing a folder, authorizing a connector) keep using their
// own endpoints, which already enforce their own validation; the wizard then
// re-asks the adapter what the result was.
type StepAction struct {
	// Type is the action being requested. Version 1 offers one: "confirm",
	// meaning "the user read the disclosure and approved this step".
	Type string
	// Option is an adapter-declared option ID, when the step offers a choice.
	Option string
}

// ActionConfirm is the only version 1 action type: explicit user approval of a
// step whose disclosure has been shown.
const ActionConfirm = "confirm"

// Adapter is server-owned domain behavior for a typed step. Implementations
// live next to the domain they serve and call its existing service; they never
// re-implement it, and never widen its permission boundary.
type Adapter interface {
	// ID is the stable registry key a manifest may name. It must match one of
	// projecttemplates.ValidSetupWizardAdapters.
	ID() string
	// Evaluate answers whether the step's requirement currently holds. It must
	// be read-only: no folder is chosen, no watcher registered, no account
	// linked, no plugin installed, and no permission granted by an evaluation.
	Evaluate(ctx context.Context, req StepRequest) (StepReadiness, error)
	// Confirm performs the step's committing action after explicit user
	// approval, and returns the resulting readiness. It must be safe to call
	// twice: a retry updates the same domain record rather than creating a
	// second one.
	Confirm(ctx context.Context, req StepRequest, action StepAction) (StepReadiness, error)
}

// AliasAdapter is an optional Adapter capability: additional registry keys that
// resolve to the same adapter.
//
// It exists so a domain can be renamed without stranding workspaces mid-setup.
// A persisted SetupWizardProgress records the adapter ID that was current when
// the workspace was created, so dropping the old key would leave those
// workspaces with a step no adapter serves — the wizard would report "unknown
// adapter" for setup the user had already partly completed.
//
// Aliases are compiled values like the primary ID. A manifest still cannot
// introduce one.
type AliasAdapter interface {
	Adapter
	// Aliases are extra keys that resolve to this adapter. The primary ID does
	// not need to be repeated.
	Aliases() []string
}

// Registry is the compiled set of adapters this process can run. It is
// populated at wiring time from code, never from configuration or a manifest —
// an adapter name in a template is a lookup key into this map and nothing more.
type Registry struct {
	adapters map[string]Adapter
	// primary records each adapter's canonical ID, so IDs() lists one entry per
	// adapter rather than one per key.
	primary map[string]bool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{adapters: make(map[string]Adapter), primary: make(map[string]bool)}
}

// Register adds an adapter under its own ID, plus any aliases it declares.
// Registering a second adapter for an already-claimed key is a wiring bug and
// is reported rather than silently accepted.
//
// A conflicting alias fails the whole registration and leaves the registry
// unchanged: a half-registered adapter, resolvable under some names but not
// others, would be harder to diagnose than one that is simply absent.
func (r *Registry) Register(adapter Adapter) error {
	if r == nil {
		return errors.New("setup adapter registry is not initialized")
	}
	if adapter == nil {
		return errors.New("cannot register a nil setup adapter")
	}
	id := normalizeAdapterID(adapter.ID())
	if id == "" {
		return errors.New("cannot register a setup adapter with a blank id")
	}

	keys := []string{id}
	if aliased, ok := adapter.(AliasAdapter); ok {
		for _, alias := range aliased.Aliases() {
			normalized := normalizeAdapterID(alias)
			if normalized == "" || normalized == id {
				continue
			}
			keys = append(keys, normalized)
		}
	}

	for _, key := range keys {
		if _, exists := r.adapters[key]; exists {
			return fmt.Errorf("setup adapter %q is already registered", key)
		}
	}
	for _, key := range keys {
		r.adapters[key] = adapter
	}
	r.primary[id] = true
	return nil
}

// Lookup resolves a registered adapter. A blank or unregistered name resolves
// to nothing: this is the fail-closed gate between a manifest's adapter string
// and any domain behavior.
func (r *Registry) Lookup(id string) (Adapter, bool) {
	if r == nil {
		return nil, false
	}
	id = normalizeAdapterID(id)
	if id == "" {
		return nil, false
	}
	adapter, ok := r.adapters[id]
	return adapter, ok
}

// IDs lists the registered adapters' canonical IDs in sorted order. Aliases are
// resolvable but not listed: they are compatibility keys, not separate adapters,
// and the manifest-parity test compares this against the authoring allowlist.
func (r *Registry) IDs() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.primary))
	for id := range r.primary {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Keys lists every resolvable key, canonical and alias, in sorted order.
func (r *Registry) Keys() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.adapters))
	for key := range r.adapters {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func normalizeAdapterID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}
