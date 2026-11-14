package folderlook

import "github.com/johnjallday/ori-agent/internal/foldermap"

// RootPolicy is trusted host context, never inferred from project evidence or
// supplied by the model. The zero value preserves the ordinary pure builders.
type RootPolicy string

const (
	RootOrdinary   RootPolicy = ""
	RootDownloads  RootPolicy = "downloads_inbox"
	RootDesktop    RootPolicy = "desktop_inbox"
	RootDocuments  RootPolicy = "documents_inbox"
	RootUnresolved RootPolicy = "unresolved"
)

// IsCatchAll identifies only the user's own three known folders.
func (p RootPolicy) IsCatchAll() bool {
	return p == RootDownloads || p == RootDesktop || p == RootDocuments
}

// PolicyLine explains eligibility without hiding the walk's project facts.
func PolicyLine(policy RootPolicy) string {
	switch policy {
	case RootDownloads:
		return "Downloads stays an inbox, not a Home or group. Detected projects do not connect tools to the whole folder. File Janitor needs separate folder access and filing approval. Explore a project folder separately."
	case RootDesktop:
		return "Desktop stays a workbench, not a Home or group. Project folders inside it can be set up on their own. File Janitor files into a Filed folder here; every move needs your approval."
	case RootDocuments:
		return "Documents stays a folder with its agent. Project folders inside it can be set up on their own; the whole folder does not become a Home or group."
	case RootUnresolved:
		return "Ori could not verify this folder. Check that it is available, then reopen this report before confirming setup. Nothing has been changed."
	default:
		return ""
	}
}

// CandidatesForPolicy keeps the generic classifier intact. Inbox installation
// is not consent to access files: that remains File Janitor's own setup.
func CandidatesForPolicy(policy RootPolicy, facts foldermap.Facts, ready Readiness) []Candidate {
	if policy.IsCatchAll() {
		why := "Downloads is an inbox, even when it contains projects."
		switch policy {
		case RootDesktop:
			why = "Desktop is a workbench. File Janitor can file screenshots and loose files by kind into Filed; you approve every move."
		case RootDocuments:
			why = "Documents is where you keep things. File Janitor is optional; nothing is selected to set up."
		}
		return []Candidate{{ID: "file-curator", Kind: KindFileCurator, Name: "File Janitor", Source: SourceTable,
			Action: ActionAdd, Required: policy != RootDocuments, Detail: "adds File Janitor; the folder's agent explains proposed filing", Why: why}}
	}
	if policy == RootUnresolved {
		return nil
	}
	return RequiredCandidates(facts, ready)
}

// StepsForPolicy filters at the catalog boundary as well as candidate
// construction: group is normally derived independently from the facts.
func StepsForPolicy(policy RootPolicy, name string, facts foldermap.Facts, candidates []Candidate) []Step {
	if policy == RootOrdinary {
		return Steps(name, facts, candidates)
	}
	if !policy.IsCatchAll() {
		return nil
	}
	return Steps(name, foldermap.Facts{}, CandidatesForPolicy(policy, facts, Readiness{}))
}
