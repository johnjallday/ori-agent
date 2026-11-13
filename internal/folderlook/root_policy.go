package folderlook

import "github.com/johnjallday/ori-agent/internal/foldermap"

// RootPolicy is trusted host context, never inferred from project evidence or
// supplied by the model. The zero value preserves the ordinary pure builders.
type RootPolicy string

const (
	RootOrdinary   RootPolicy = ""
	RootDownloads  RootPolicy = "downloads_inbox"
	RootUnresolved RootPolicy = "unresolved"
)

// PolicyLine explains eligibility without hiding the walk's project facts.
func PolicyLine(policy RootPolicy) string {
	switch policy {
	case RootDownloads:
		return "Downloads stays an inbox, not a Home or group. Detected projects do not connect tools to the whole folder. File Janitor needs separate folder access and filing approval. Explore a project folder separately."
	case RootUnresolved:
		return "Ori could not verify this folder against its known Downloads folder. Check that both folders are available, then reopen this report before confirming setup. Nothing has been changed."
	default:
		return ""
	}
}

// CandidatesForPolicy keeps the generic classifier intact. Inbox installation
// is not consent to access files: that remains File Janitor's own setup.
func CandidatesForPolicy(policy RootPolicy, facts foldermap.Facts, ready Readiness) []Candidate {
	switch policy {
	case RootDownloads:
		return []Candidate{{ID: "file-curator", Kind: KindFileCurator, Name: "File Janitor", Source: SourceTable,
			Action: ActionAdd, Required: true, Detail: "adds File Janitor; the folder's agent explains proposed filing",
			Why: "Downloads is an inbox, even when it contains projects."}}
	case RootUnresolved:
		return nil
	default:
		return RequiredCandidates(facts, ready)
	}
}

// StepsForPolicy filters at the catalog boundary as well as candidate
// construction: group is normally derived independently from the facts.
func StepsForPolicy(policy RootPolicy, name string, facts foldermap.Facts, candidates []Candidate) []Step {
	if policy == RootOrdinary {
		return Steps(name, facts, candidates)
	}
	if policy != RootDownloads {
		return nil
	}
	return Steps(name, foldermap.Facts{}, CandidatesForPolicy(policy, facts, Readiness{}))
}
