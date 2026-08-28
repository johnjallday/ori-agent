// Package blueprintreadiness defines the single contract describing whether a
// blueprint offered in the creation catalog can actually be used, and — when it
// cannot — what the one next action is.
//
// The contract is deliberately bounded. Everything crossing to a client is an
// enum from a closed set or sanitized display copy; nothing here can carry an
// executable path, artifact URL, local endpoint, command line, or credential.
// A plugin source declared by a template is an untrusted hint and is reported
// only as "a source is declared", never echoed — the existing trust-preview
// flow remains the only place a source is shown before it is acted on.
//
// The contract is also domain-generic: it names states, reasons, and actions
// that apply to every plugin-backed blueprint. No template ID, capability ID,
// runtime key, file extension, or product name appears in it.
package blueprintreadiness

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// State is the coarse, stable classification a client renders from. Three
// values, ordered by how much they permit: create now, do one thing first, or
// this cannot be used here at all.
type State string

const (
	// StateReady means creation may proceed. The server still revalidates.
	StateReady State = "ready"
	// StateActionRequired means one explicit, user-initiated recovery action
	// can make the blueprint ready without leaving the wizard.
	StateActionRequired State = "action_required"
	// StateUnavailable means nothing the wizard offers will fix it: the
	// platform, the host protocol, or the manifest itself is the obstacle.
	StateUnavailable State = "unavailable"
)

// Ownership says who can act on a problem. It is what keeps "fix this
// template's template.json" pointed at people who actually own the file.
type Ownership string

const (
	// OwnershipBuiltin is a blueprint shipped inside the app.
	OwnershipBuiltin Ownership = "builtin"
	// OwnershipUser is a blueprint the user authored, imported, or duplicated.
	OwnershipUser Ownership = "user"
	// OwnershipPlugin is a blueprint contributed by an installed plugin.
	OwnershipPlugin Ownership = "plugin"
)

// Reason is the closed set of why-codes. A client may map each to its own copy;
// none is ever assembled from template-supplied text.
type Reason string

const (
	// ReasonNone accompanies StateReady.
	ReasonNone Reason = ""
	// ReasonPluginInstallRequired: the blueprint depends on a plugin that is
	// not installed on this machine.
	ReasonPluginInstallRequired Reason = "plugin_install_required"
	// ReasonPluginEnableRequired: the plugin is installed but disabled.
	ReasonPluginEnableRequired Reason = "plugin_enable_required"
	// ReasonPluginUpdateRequired: the installed plugin cannot supply this
	// blueprint until it is updated, and the update may change trust.
	ReasonPluginUpdateRequired Reason = "plugin_update_required"
	// ReasonPlatformUnsupported: the plugin ships no artifact for this
	// operating system and architecture.
	ReasonPlatformUnsupported Reason = "platform_unsupported"
	// ReasonProtocolIncompatible: the plugin's declared surface protocol range
	// does not include the running host.
	ReasonProtocolIncompatible Reason = "protocol_incompatible"
	// ReasonBlueprintRetired: an on-disk manifest claims built-in ownership
	// under an ID this version of the app no longer ships. Its files are
	// preserved; it is simply no longer offered for ordinary creation.
	ReasonBlueprintRetired Reason = "blueprint_retired"
	// ReasonManifestInvalid: the blueprint's own manifest could not be
	// understood. Actionable only for a user-owned template.
	ReasonManifestInvalid Reason = "manifest_invalid"
	// ReasonRuntimeProviderUnavailable: the manifest references a runtime
	// provider or adapter this build does not register.
	ReasonRuntimeProviderUnavailable Reason = "runtime_provider_unavailable"
	// ReasonDependencyStateUnknown: dependency state could not be read this
	// time (a transient store or listing failure). Retry is meaningful.
	ReasonDependencyStateUnknown Reason = "dependency_state_unknown"
)

// Action is the allowlist of recovery actions a readiness descriptor may
// offer. A client sends one of these back verbatim; anything else is refused
// rather than interpreted. No action carries a target beyond the identifiers
// already in the descriptor, so a descriptor can never widen what a click does.
type Action string

const (
	// ActionInstallPlugin starts the existing confirm-gated install flow,
	// beginning with a trust preview.
	ActionInstallPlugin Action = "install_plugin"
	// ActionEnablePlugin enables an already-installed plugin.
	ActionEnablePlugin Action = "enable_plugin"
	// ActionReviewPluginUpdate previews an update and re-discloses trust.
	ActionReviewPluginUpdate Action = "review_plugin_update"
	// ActionRetry re-reads dependency state after a transient failure.
	ActionRetry Action = "retry"
	// ActionManagePlugins routes to the Plugins page.
	ActionManagePlugins Action = "manage_plugins"
	// ActionChangeBlueprint returns to blueprint selection.
	ActionChangeBlueprint Action = "change_blueprint"
	// ActionEditTemplateManifest points a template author at their own
	// template.json. Only ever offered for OwnershipUser.
	ActionEditTemplateManifest Action = "edit_template_manifest"
)

// Copy length limits. Long enough to explain a state in a sentence or two,
// short enough that no diagnostic can smuggle a payload through display copy.
const (
	MaxSummaryLen    = 160
	MaxDetailLen     = 400
	MaxDiagnosticLen = 600
	// MaxActions bounds the action list so a descriptor stays a single next
	// step plus escape routes, never a menu.
	MaxActions = 4
)

// Dependency identifies the plugin a blueprint depends on, by name and
// recorded version only.
//
// SourceDeclared reports that the manifest names a source for this plugin
// without disclosing it: the source is an untrusted template-supplied hint,
// and the trust preview is the only surface allowed to show it, immediately
// before the user confirms acting on it.
type Dependency struct {
	PluginName string `json:"plugin_name"`
	// PluginVersion is the version already recorded in the installed-plugin
	// store, or empty when the plugin is not installed.
	PluginVersion string `json:"plugin_version,omitempty"`
	// Installed and Enabled mirror the authoritative store, so a client can
	// label "Installed, still disabled" without inferring it from the reason.
	Installed bool `json:"installed"`
	Enabled   bool `json:"enabled"`
	// SourceDeclared reports whether a source hint exists, never what it is.
	SourceDeclared bool `json:"source_declared"`
}

// Readiness is the full projection attached to one catalog blueprint, and the
// same shape returned in a creation conflict.
type Readiness struct {
	State     State     `json:"state"`
	Ownership Ownership `json:"ownership"`
	Reason    Reason    `json:"reason,omitempty"`
	// Summary is one sanitized sentence naming the state and its consequence.
	Summary string `json:"summary,omitempty"`
	// Detail is sanitized supporting copy: what still works, or what happens
	// after the action. Never a parser message.
	Detail string `json:"detail,omitempty"`
	// Diagnostic is bounded technical text for a template author, shown behind
	// a disclosure. Populated only when Ownership is OwnershipUser — nobody
	// else can act on it, so nobody else is shown it.
	Diagnostic string `json:"diagnostic,omitempty"`
	// Dependency is the plugin this state is about, when there is one.
	Dependency *Dependency `json:"dependency,omitempty"`
	// Actions is the ordered allowlisted recovery set, most useful first.
	Actions []Action `json:"actions,omitempty"`
	// Generation is the installed-plugin generation this projection was
	// derived from, so a stale confirmation can be rejected rather than
	// applied to a plugin that changed underneath it.
	Generation uint64 `json:"generation,omitempty"`
}

// Creatable reports whether creation may proceed from this projection. It is
// guidance for the client; the server revalidates before it mutates anything.
func (r Readiness) Creatable() bool { return r.State == StateReady }

// ready builds the trivial projection.
func Ready(ownership Ownership) Readiness {
	return Readiness{State: StateReady, Ownership: ownership}
}

var validStates = map[State]struct{}{
	StateReady: {}, StateActionRequired: {}, StateUnavailable: {},
}

var validOwnerships = map[Ownership]struct{}{
	OwnershipBuiltin: {}, OwnershipUser: {}, OwnershipPlugin: {},
}

var validReasons = map[Reason]struct{}{
	ReasonNone: {}, ReasonPluginInstallRequired: {}, ReasonPluginEnableRequired: {},
	ReasonPluginUpdateRequired: {}, ReasonPlatformUnsupported: {}, ReasonProtocolIncompatible: {},
	ReasonBlueprintRetired: {}, ReasonManifestInvalid: {}, ReasonRuntimeProviderUnavailable: {},
	ReasonDependencyStateUnknown: {},
}

var validActions = map[Action]struct{}{
	ActionInstallPlugin: {}, ActionEnablePlugin: {}, ActionReviewPluginUpdate: {},
	ActionRetry: {}, ActionManagePlugins: {}, ActionChangeBlueprint: {},
	ActionEditTemplateManifest: {},
}

// ParseAction resolves a client-supplied action name against the allowlist.
// An unknown, differently-cased, or padded value is refused rather than
// coerced: the set is small and fixed, so there is nothing to be lenient about.
func ParseAction(raw string) (Action, bool) {
	action := Action(raw)
	_, ok := validActions[action]
	return action, ok
}

// ValidState, ValidOwnership, and ValidReason report membership of the closed
// sets. They exist so a projection can be asserted whole in tests and refused
// wholesale at a boundary rather than trusted field by field.
func ValidState(s State) bool         { _, ok := validStates[s]; return ok }
func ValidOwnership(o Ownership) bool { _, ok := validOwnerships[o]; return ok }
func ValidReason(r Reason) bool       { _, ok := validReasons[r]; return ok }

// Normalize enforces every invariant the contract promises and returns the
// repaired projection. It is the last thing a producer calls and the first
// thing a consumer may rely on:
//
//   - unknown state/ownership/reason collapse to the safe classification
//     (unavailable, plugin-owned, unknown dependency state) rather than being
//     passed through;
//   - StateReady carries no reason, no diagnostic, and no actions;
//   - copy is sanitized and truncated;
//   - a diagnostic survives only for a user-owned blueprint;
//   - actions are deduplicated, allowlisted, and capped;
//   - ActionEditTemplateManifest survives only for a user-owned blueprint.
func (r Readiness) Normalize() Readiness {
	if !ValidState(r.State) {
		r.State = StateUnavailable
	}
	if !ValidOwnership(r.Ownership) {
		r.Ownership = OwnershipPlugin
	}
	if !ValidReason(r.Reason) {
		r.Reason = ReasonDependencyStateUnknown
	}

	r.Summary = SanitizeCopy(r.Summary, MaxSummaryLen)
	r.Detail = SanitizeCopy(r.Detail, MaxDetailLen)
	r.Diagnostic = SanitizeCopy(r.Diagnostic, MaxDiagnosticLen)
	if r.Ownership != OwnershipUser {
		// Nobody but the template's author can act on a parser message, and
		// showing one for a shipped or plugin-owned manifest reads as an
		// instruction to edit a file the user must not touch.
		r.Diagnostic = ""
	}

	if r.State == StateReady {
		r.Reason = ReasonNone
		r.Diagnostic = ""
		r.Actions = nil
		return r
	}

	r.Actions = normalizeActions(r.Actions, r.Ownership)
	return r
}

func normalizeActions(actions []Action, ownership Ownership) []Action {
	if len(actions) == 0 {
		return nil
	}
	out := make([]Action, 0, len(actions))
	seen := make(map[Action]struct{}, len(actions))
	for _, action := range actions {
		if _, ok := validActions[action]; !ok {
			continue
		}
		if action == ActionEditTemplateManifest && ownership != OwnershipUser {
			continue
		}
		if _, duplicate := seen[action]; duplicate {
			continue
		}
		seen[action] = struct{}{}
		out = append(out, action)
		if len(out) == MaxActions {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SanitizeCopy prepares text for display: control characters are dropped,
// whitespace is collapsed to single spaces, anything that reads as a locator
// (a URL, a filesystem path, a shell expansion) is redacted, and the result is
// truncated on a rune boundary.
//
// The redaction is what makes it safe to build copy from text a manifest or a
// third-party error message contributed. Truncation alone would still let a
// crafted diagnostic display a working URL, and display copy is exactly where
// a user is most likely to trust one.
func SanitizeCopy(text string, max int) string {
	text = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return ' '
		}
		if unicode.IsControl(r) || !utf8.ValidRune(r) {
			return -1
		}
		return r
	}, text)

	fields := strings.Fields(text)
	for i, field := range fields {
		if looksLikeLocator(field) {
			fields[i] = "…"
		}
	}
	text = strings.Join(fields, " ")
	return truncateRunes(text, max)
}

// looksLikeLocator reports whether a whitespace-delimited token reads as
// something a user could act on as an address: a URL, an absolute or
// home-relative path, a Windows drive path, or a shell expansion.
func looksLikeLocator(token string) bool {
	trimmed := strings.Trim(token, `"'()[]{}<>,;`)
	if trimmed == "" {
		return false
	}
	if strings.Contains(trimmed, "://") {
		return true
	}
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "~/") || strings.HasPrefix(trimmed, `\\`) {
		return true
	}
	if strings.ContainsAny(trimmed, "$`|") {
		return true
	}
	// A Windows drive letter, e.g. C:\Users\…
	if len(trimmed) >= 3 && trimmed[1] == ':' && (trimmed[2] == '\\' || trimmed[2] == '/') {
		return true
	}
	return false
}

func truncateRunes(text string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	runes := []rune(text)
	cut := max - 1
	if cut < 1 {
		return "…"
	}
	return strings.TrimRight(string(runes[:cut]), " ") + "…"
}
