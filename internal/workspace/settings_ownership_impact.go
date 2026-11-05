package workspace

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/store"
)

// Impact: what an edit to the library profile would reach, from current
// evidence. It mirrors the carry rules (projectstaffing): only the carried
// field family reaches tracked copies, and only copies still as Ori wrote them;
// a workspace with no copy reads the library directly. It is advisory — a
// later save re-checks everything — and it never carries or writes.

// Impact effects for one workspace.
const (
	ImpactReadsLibrary = "reads_library" // no own copy: every library edit applies at once
	ImpactKeptInStep   = "kept_in_step"  // tracked, unchanged copy: carried fields follow
	ImpactPending      = "pending"       // tracked, unchanged, not yet updated
	ImpactCustomized   = "customized"    // changed in its workspace: library edits stop here
	ImpactIndependent  = "independent"   // untracked own copy: library edits never reach it
	ImpactUnknown      = "unknown"
)

// ImpactCarriedFields is the field family an edit carries into tracked copies
// (projectstaffing.carried). Role and description are not in it.
var ImpactCarriedFields = []string{"provider", "model", "reasoning_effort", "temperature", "max_output_tokens", "system_prompt"}

// ImpactNotCarried names what never propagates through a copy.
const ImpactNotCarried = "API keys, web search, native tools and cloud fallback permissions are never copied to workspaces; each workspace keeps the access it was given."

// OwnershipImpact summarizes what a library edit would reach.
type OwnershipImpact struct {
	State         string                `json:"state"`
	Copies        []OwnershipImpactCopy `json:"copies"`
	CarriedFields []string              `json:"carried_fields,omitempty"`
	NotCarried    string                `json:"not_carried,omitempty"`
}

// OwnershipImpactCopy is one workspace and how a library edit would reach it.
type OwnershipImpactCopy struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	FolderSlug  string `json:"folder_slug"`
	Effect      string `json:"effect"`
}

func projectLibraryImpact(src SettingsOwnershipSources, out SettingsOwnership, library store.LibraryRecord, name string) OwnershipImpact {
	impact := OwnershipImpact{State: OwnershipToolboxNotApplicable, Copies: []OwnershipImpactCopy{}}
	if library.State != store.LibraryPresent || library.Definition == nil || out.Context.State != OwnershipStateOK {
		return impact
	}
	impact.State = OwnershipStateOK
	impact.CarriedFields = append([]string(nil), ImpactCarriedFields...)
	impact.NotCarried = ImpactNotCarried
	if src.Workspaces == nil || out.Contexts.State != OwnershipStateOK {
		impact.State = "partial"
		return impact
	}
	for _, ref := range out.Contexts.Workspaces {
		entry := OwnershipImpactCopy{WorkspaceID: ref.ID, Name: ref.Name, FolderSlug: ref.FolderSlug, Effect: ImpactUnknown}
		entry.Effect = impactFor(src, library, name, ref.ID)
		if entry.Effect == ImpactUnknown {
			impact.State = "partial"
		}
		impact.Copies = append(impact.Copies, entry)
	}
	return impact
}

func impactFor(src SettingsOwnershipSources, library store.LibraryRecord, name, workspaceID string) string {
	ws, err := src.Workspaces.Get(workspaceID)
	if err != nil || ws == nil {
		return ImpactUnknown
	}
	for _, ref := range referencedAgentNames(ws) {
		if !strings.EqualFold(ref, name) && Slugify(ref) == Slugify(name) {
			return ImpactUnknown // a slug-shared copy file cannot be attributed
		}
	}
	copyAgent, hasCopy, err := src.Workspaces.GetWorkspaceAgent(workspaceID, name)
	switch {
	case err != nil:
		return ImpactUnknown
	case !hasCopy || copyAgent == nil:
		return ImpactReadsLibrary
	case src.Tracking == nil:
		return ImpactUnknown
	}
	state, _ := src.Tracking(name, workspaceID, library.Definition, copyAgent)
	switch state {
	case "in_step":
		return ImpactKeptInStep
	case "pending":
		return ImpactPending
	case "customized":
		return ImpactCustomized
	case "untracked":
		return ImpactIndependent
	default:
		return ImpactUnknown
	}
}
