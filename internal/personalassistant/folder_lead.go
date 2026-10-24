package personalassistant

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// FolderLead is the choice between two hires for a folder that holds a project
// beside loose files (folder-inbox-hire FR12, FR13). The host makes it from the
// digest alone, with no model: the review that leads carries the host-written
// reason, and either review carries the other hire, one click away. Nothing is
// created by switching. Every field is plain text and none is a path.
type FolderLead struct {
	// Leading says this review is the suggested hire.
	Leading bool `json:"leading"`
	// Reason is the evidence for the suggestion, on the leading review only.
	Reason string `json:"reason,omitempty"`
	// AlternativeID is the other hire's candidate id in the same observation, and
	// AlternativeLabel how the link names it ("hire File Curator to tidy the
	// loose files").
	AlternativeID    string `json:"alternative_id"`
	AlternativeLabel string `json:"alternative_label"`
}

func (l *FolderLead) validate() error {
	if l == nil {
		return nil
	}
	for _, text := range []string{l.Reason, l.AlternativeID, l.AlternativeLabel} {
		if len(text) > 400 || strings.ContainsAny(text, "\x00\r\n") || looksLikeFilesystemPath(text) {
			return fmt.Errorf("%w: hire lead", errFolderDigestInvalid)
		}
	}
	if !validStructureName(l.AlternativeID) || strings.TrimSpace(l.AlternativeLabel) == "" || (!l.Leading && l.Reason != "") {
		return fmt.Errorf("%w: hire lead", errFolderDigestInvalid)
	}
	return nil
}

func (l *FolderLead) clone() *FolderLead {
	if l == nil {
		return nil
	}
	copied := *l
	return &copied
}

// folderTidyLabel and the project label are the two alternatives' link text.
const folderTidyLabel = "hire File Curator to tidy the loose files"

// leadAgentLabel names the assistant a project of this shape is hired with.
func leadAgentLabel(shape folderdigest.Shape) string {
	if shape == folderdigest.ShapeCode {
		return "Development Assistant"
	}
	if row, ok := folderdigest.CapabilityForShape(shape); ok {
		if row.Offer != nil && strings.TrimSpace(row.Offer.SpecialistName) != "" {
			return row.Offer.SpecialistName
		}
		if row.Blueprint.Label != "" {
			return "the " + row.Blueprint.Label + " assistant"
		}
	}
	return "a project assistant"
}

func countOf(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// leadReason is the host's "Why this suggestion?" line, written from the
// evidence: the loose files' count and kinds, the project's marker label and the
// date it last changed. It names no path.
func leadReason(lead folderdigest.HireLead) string {
	label := strings.TrimSpace(lead.Project.Marker.Label)
	if lead.Project.Marker == nil || label == "" {
		label = "project"
	}
	article := "a"
	if strings.ContainsRune("AEIOUaeiou", rune(label[0])) {
		article = "an"
	}
	beside := fmt.Sprintf("%s of %s sit beside %s %s", countOf(lead.LooseFiles, "loose file"), countOf(lead.LooseKinds, "kind"), article, label)
	if !lead.Project.NewestModTime.IsZero() {
		beside += " changed on " + lead.Project.NewestModTime.Format("Jan 2")
	}
	if lead.TidyLeads {
		return beside + ": the loose files are the bigger job."
	}
	return beside + ": the project is the bigger job."
}

// reviewLead is the lead for the review of one candidate of a held folder, or
// nil: the root's tidy review and the leading project's review are the two
// hires, each naming the other. A build's review and a supporting link are
// neither.
func reviewLead(held heldFolderObservation, candidate folderdigest.Candidate, lead folderdigest.HireLead) *FolderLead {
	idOf := func(path string) string {
		for index, found := range held.result.Candidates {
			if found.Path == path {
				return fmt.Sprintf("candidate-%d", index)
			}
		}
		return ""
	}
	rootID, projectID := folderRootCandidateID, idOf(lead.Project.Path)
	if projectID == "" {
		return nil
	}
	reason := leadReason(lead)
	projectLabel := "hire " + leadAgentLabel(folderdigest.ShapeFor(lead.Project)) + " for " + foldercontext.DisplayName(lead.Project.Name) + " instead"
	switch {
	case candidate.IsRoot:
		out := &FolderLead{Leading: lead.TidyLeads, AlternativeID: projectID, AlternativeLabel: projectLabel}
		if out.Leading {
			out.Reason = reason
		}
		return out
	case candidate.Path == lead.Project.Path:
		out := &FolderLead{Leading: !lead.TidyLeads, AlternativeID: rootID, AlternativeLabel: folderTidyLabel + " instead"}
		if out.Leading {
			out.Reason = reason
		}
		return out
	}
	return nil
}
