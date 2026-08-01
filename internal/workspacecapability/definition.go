// Package workspacecapability defines the built-in Workspace Capabilities Ori
// is allowed to activate, and resolves a workspace's persisted install records
// against them.
//
// The security shape of this package is deliberate (PRD FR-14). A Definition is
// pure declarative metadata: strings, bools, and string slices describing a
// capability that is already compiled into this build. It has no field that can
// name a script, a URL, a shell command, a file path, or a browser module, so a
// persisted install record — which is workspace-controlled data — cannot cause
// anything to be loaded or executed. It can only select an allowlisted
// definition, or fail to.
//
// Compiled behavior lives behind the Runtime interfaces and is bound to the
// registry by the server at startup, never by a workspace file.
package workspacecapability

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// StatusState is the derived runtime health of an installed capability.
//
// It is always computed by the capability's compiled service at read time and
// is never persisted on the workspace record (FR-6): a stored "watching" string
// would keep claiming the capability is healthy after the folder disappeared.
type StatusState string

const (
	// StatusSetupNeeded means installed but not yet configured — no folder has
	// been approved.
	StatusSetupNeeded StatusState = "setup_needed"
	// StatusWatching means configured, healthy, and automation is active.
	StatusWatching StatusState = "watching"
	// StatusPaused means configured and healthy, but event and scheduled scans
	// are paused by the user.
	StatusPaused StatusState = "paused"
	// StatusReviewReady means candidates are waiting for a decision.
	StatusReviewReady StatusState = "review_ready"
	// StatusNeedsAttention means a required component is failing — permission
	// loss, a missing root, or unwritable state.
	StatusNeedsAttention StatusState = "needs_attention"
	// StatusUnavailable means the installed ID does not resolve to a definition
	// compiled into this build, or the capability's runtime is not wired. The
	// install record stays visible as metadata; nothing is executed.
	StatusUnavailable StatusState = "unavailable"
)

// StationStatusPriority is the display order required when several conditions
// apply at once (PRD design 8.4). Earlier entries win.
var StationStatusPriority = []StatusState{
	StatusNeedsAttention,
	StatusSetupNeeded,
	StatusReviewReady,
	StatusPaused,
	StatusWatching,
}

// HighestPriorityStatus returns whichever of the given states ranks highest in
// StationStatusPriority. States outside the priority list (e.g. Unavailable)
// rank below every listed state, and an empty input yields "".
func HighestPriorityStatus(states ...StatusState) StatusState {
	best := StatusState("")
	bestRank := len(StationStatusPriority)
	for _, state := range states {
		rank := len(StationStatusPriority)
		for i, candidate := range StationStatusPriority {
			if candidate == state {
				rank = i
				break
			}
		}
		if best == "" || rank < bestRank {
			best, bestRank = state, rank
		}
	}
	return best
}

// Status is a capability's derived health for one workspace.
type Status struct {
	State StatusState `json:"state"`
	// Detail is short human-readable text for the station and card. It must
	// carry the status in words, not only as a colour or badge (FR-96).
	Detail string `json:"detail,omitempty"`
	// ReviewCount is how many candidates are waiting for a decision.
	ReviewCount int `json:"review_count,omitempty"`
	// FolderDisplayName is a safe short name for the managed folder, suitable
	// for the station and console header. It is not the full path (FR-95).
	FolderDisplayName string `json:"folder_display_name,omitempty"`
	// Configured reports whether setup has completed.
	Configured bool `json:"configured"`
}

// Display is the user-facing catalog copy for a capability (FR-18).
type Display struct {
	Name    string `json:"name"`
	Tagline string `json:"tagline,omitempty"`
	Summary string `json:"summary,omitempty"`
	// Highlights are the short factual bullets the catalog item must show
	// before install: the one-folder limit, metadata-first behavior, the
	// approval requirement, and the fixed Filed/ destination model.
	Highlights []string `json:"highlights,omitempty"`
}

// Requirements declares what a workspace must support to install a capability.
type Requirements struct {
	// LocalFolderAccess marks a capability that needs access to a local
	// directory, so it can be offered only where that is possible (FR-19).
	LocalFolderAccess bool `json:"local_folder_access,omitempty"`
	// MaxInstallsPerWorkspace caps concurrent active installs. V1 File Janitor
	// is 1 (FR-8).
	MaxInstallsPerWorkspace int `json:"max_installs_per_workspace,omitempty"`
}

// SetupDescriptor names the capability's setup adapter and the directory
// requirement key its blueprint declares, plus the legacy spellings that must
// keep resolving during the compatibility release (FR-134).
type SetupDescriptor struct {
	AdapterID                      string   `json:"adapter_id,omitempty"`
	LegacyAdapterIDs               []string `json:"legacy_adapter_ids,omitempty"`
	DirectoryRequirementKey        string   `json:"directory_requirement_key,omitempty"`
	LegacyDirectoryRequirementKeys []string `json:"legacy_directory_requirement_keys,omitempty"`
}

// APIDescriptor names the canonical workspace-scoped route segment for a
// capability and the legacy segments retained as tested aliases (FR-132–133).
type APIDescriptor struct {
	Prefix         string   `json:"prefix,omitempty"`
	LegacyPrefixes []string `json:"legacy_prefixes,omitempty"`
}

// StationDescriptor describes the capability's presence on the Workspace Map.
type StationDescriptor struct {
	// Title is the station's fixed label. It never varies by workspace name,
	// template name, folder name, or agent name (FR-94–95).
	Title string `json:"title,omitempty"`
	// ShowFolderDisplayName adds the managed folder's safe short name beneath
	// the title when one is known.
	ShowFolderDisplayName bool `json:"show_folder_display_name,omitempty"`
}

// ConsoleDescriptor describes the capability's primary surface.
type ConsoleDescriptor struct {
	// PanelID is the workspace-local deep-link value, i.e. ?panel=<PanelID>
	// (FR-117).
	PanelID string `json:"panel_id,omitempty"`
	// Tabs is the allowlist of valid tab values for a configured console. A
	// deep link naming anything outside this list is rejected without
	// preventing the workspace from loading (FR-117, FR-145).
	Tabs []string `json:"tabs,omitempty"`
	// DefaultTab is used when no tab is requested and nothing needs a decision.
	DefaultTab string `json:"default_tab,omitempty"`
}

// AutomationDescriptor declares the automation a capability may register once
// the user approves it. Declaring it here does NOT start anything: it names the
// stable in-process registration identities so migration and removal can
// reconcile them without guessing (FR-130, FR-138).
type AutomationDescriptor struct {
	// WatcherID and ScheduleID are stable identities for the capability's
	// folder watcher and daily catch-up registration.
	WatcherID  string `json:"watcher_id,omitempty"`
	ScheduleID string `json:"schedule_id,omitempty"`
	// DefaultDailyLocalTime is the suggested (not activated) catch-up time
	// (FR-54).
	DefaultDailyLocalTime string `json:"default_daily_local_time,omitempty"`
}

// CompanionDescriptor declares the capability's optional companion agent
// (FR-35–FR-40). It carries no tool grants: the read-only tool allowlist is
// compiled into the companion's binding logic, not described by data.
type CompanionDescriptor struct {
	// DefaultDisplayName is used when no folder-specific name applies (FR-40).
	DefaultDisplayName string `json:"default_display_name,omitempty"`
	// IncludedByBlueprint adds the companion to blueprint-created workspaces
	// by default (FR-35).
	IncludedByBlueprint bool `json:"included_by_blueprint,omitempty"`
	// OfferedOnInPlaceInstall exposes the companion as a separate opt-in after
	// an in-place install (FR-36). Declining leaves the capability fully
	// functional (FR-37).
	OfferedOnInPlaceInstall bool `json:"offered_on_in_place_install,omitempty"`
	// ReadOnly records the product guarantee that this companion never
	// receives approval, scan, configuration, or mutation tools (FR-42).
	ReadOnly bool `json:"read_only,omitempty"`
}

// Definition is the compiled description of one built-in Workspace Capability.
//
// Every field is inert metadata. Adding a field that could name executable
// behavior (a command, URL, script path, or module specifier) would break the
// FR-14 guarantee that a persisted install record can only select compiled
// behavior, never supply it.
type Definition struct {
	// ID is the stable capability identifier, normalized to lower case.
	ID string `json:"id"`
	// Version is this build's definition version, used to detect that a
	// persisted install predates the current definition and may need
	// capability-specific migration (FR-13).
	Version int `json:"version"`

	Display      Display              `json:"display"`
	Requirements Requirements         `json:"requirements,omitempty"`
	Setup        SetupDescriptor      `json:"setup,omitempty"`
	API          APIDescriptor        `json:"api,omitempty"`
	Station      StationDescriptor    `json:"station,omitempty"`
	Console      ConsoleDescriptor    `json:"console,omitempty"`
	Automation   AutomationDescriptor `json:"automation,omitempty"`
	Companion    *CompanionDescriptor `json:"companion,omitempty"`
}

// Clone returns a deep copy so a caller (e.g. a catalog HTTP response builder)
// cannot mutate the compiled definition held by the registry.
func (d Definition) Clone() Definition {
	cp := d
	cp.Display.Highlights = append([]string(nil), d.Display.Highlights...)
	cp.Setup.LegacyAdapterIDs = append([]string(nil), d.Setup.LegacyAdapterIDs...)
	cp.Setup.LegacyDirectoryRequirementKeys = append([]string(nil), d.Setup.LegacyDirectoryRequirementKeys...)
	cp.API.LegacyPrefixes = append([]string(nil), d.API.LegacyPrefixes...)
	cp.Console.Tabs = append([]string(nil), d.Console.Tabs...)
	if d.Companion != nil {
		companion := *d.Companion
		cp.Companion = &companion
	}
	return cp
}

// Validate reports whether a definition is well-formed enough to register.
func (d Definition) Validate() error {
	if workspace.NormalizeCapabilityID(d.ID) == "" {
		return fmt.Errorf("capability definition: id is required")
	}
	if d.Version < 1 {
		return fmt.Errorf("capability definition %q: version must be positive, got %d", d.ID, d.Version)
	}
	if strings.TrimSpace(d.Display.Name) == "" {
		return fmt.Errorf("capability definition %q: display name is required", d.ID)
	}
	if d.Requirements.MaxInstallsPerWorkspace < 0 {
		return fmt.Errorf("capability definition %q: max installs per workspace cannot be negative", d.ID)
	}
	if tab := strings.TrimSpace(d.Console.DefaultTab); tab != "" && !d.Console.HasTab(tab) {
		return fmt.Errorf("capability definition %q: default tab %q is not in the tab allowlist", d.ID, tab)
	}
	return nil
}

// HasTab reports whether tab is in the console's allowlist, case-insensitively.
// Deep-link and Action Center routing must validate through this rather than
// trusting a requested tab value (FR-116–117).
func (c ConsoleDescriptor) HasTab(tab string) bool {
	want := strings.ToLower(strings.TrimSpace(tab))
	if want == "" {
		return false
	}
	for _, candidate := range c.Tabs {
		if strings.ToLower(strings.TrimSpace(candidate)) == want {
			return true
		}
	}
	return false
}

// MatchesAdapterID reports whether the given setup-adapter identifier addresses
// this capability, accepting the canonical ID and every retained legacy alias.
func (s SetupDescriptor) MatchesAdapterID(id string) bool {
	return matchesAny(id, s.AdapterID, s.LegacyAdapterIDs)
}

// MatchesDirectoryRequirementKey reports whether the given directory
// requirement key addresses this capability, canonical or legacy.
func (s SetupDescriptor) MatchesDirectoryRequirementKey(key string) bool {
	return matchesAny(key, s.DirectoryRequirementKey, s.LegacyDirectoryRequirementKeys)
}

// MatchesPrefix reports whether the given route segment addresses this
// capability, canonical or legacy.
func (a APIDescriptor) MatchesPrefix(prefix string) bool {
	return matchesAny(prefix, a.Prefix, a.LegacyPrefixes)
}

func matchesAny(value, canonical string, aliases []string) bool {
	want := strings.ToLower(strings.TrimSpace(value))
	if want == "" {
		return false
	}
	if strings.ToLower(strings.TrimSpace(canonical)) == want {
		return true
	}
	for _, alias := range aliases {
		if strings.ToLower(strings.TrimSpace(alias)) == want {
			return true
		}
	}
	return false
}
