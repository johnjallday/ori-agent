package folderdigest

import "time"

// HireLead is the choice between two hires for one folder: a project of a kind
// Ori knows inside it, and the loose files beside that project (folder-inbox-hire
// FR12). The host makes it from the verdict alone, with no model: the tidy hire
// leads when the root meets the dump rule, because the loose files are then the
// folder's main problem; otherwise the project hire leads. The other hire stays
// one click away.
type HireLead struct {
	// TidyLeads is true when the root is a dump and File Curator leads.
	TidyLeads bool
	// Project is the project the other hire is for (the top-ranked one).
	Project Candidate
	// LooseFiles and LooseKinds describe the root's direct child files.
	LooseFiles int
	LooseKinds int
}

// MixedHireLead reports the two-hire choice for a root, or false when there is
// none: a collection keeps its group proposal, a root that is itself a project
// keeps its own card, a root the user declined has no tidy to offer, and a root
// with nothing loose has nothing to tidy beside its project.
func MixedHireLead(r Result, now time.Time, declined func(Candidate) bool) (HireLead, bool) {
	if declined == nil {
		declined = func(Candidate) bool { return false }
	}
	root := r.RootCandidate()
	if declined(root) || root.HasMarker() || IsCollection(r, now) {
		return HireLead{}, false
	}
	v := Exclude(r, now, declined)
	if (v.Kind != KindMixed && v.Kind != KindProject) || v.Project == nil || v.Project.IsRoot || !knownProject(*v.Project) {
		return HireLead{}, false
	}
	if root.LooseFiles == 0 {
		return HireLead{}, false
	}
	return HireLead{TidyLeads: v.RootDump, Project: *v.Project, LooseFiles: root.LooseFiles, LooseKinds: root.LooseKinds()}, true
}
