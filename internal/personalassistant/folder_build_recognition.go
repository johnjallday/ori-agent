package personalassistant

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/folderdigest"
)

// Recognize says what the folder digest concludes about a held selection: the
// verdict, capability row and suggested blueprint Home's Explore a folder works
// from, read from the scan Observe already made. It reads only — no rescan, no
// offer, no journey, no model — and a selection that is no longer held is
// ErrFolderSelection, exactly as Resolve refuses it.
//
// Like a conversation's review, it does not read a No thanks to a kind of work
// (attaching the folder is the ask), but a folder the user said never to ask
// about again is not recognized.
func (s *FolderObservationService) Recognize(ctx context.Context, target foldercontext.Target, observationID string) (*BuildFolderRecognition, error) {
	held, _, err := s.resolve(ctx, target, observationID)
	if err != nil {
		return nil, err
	}
	d := s.digest
	doc, err := d.store.Read(ctx, target.UserID)
	if err != nil {
		return nil, err
	}
	now := d.now()
	unrecognized := &BuildFolderRecognition{Verdict: BuildFolderUnrecognized}
	// A folder of several projects is set up as a group, never as the one
	// workspace a build makes. The count follows IsCollection's own rule.
	if folderdigest.IsCollection(held.result, now) {
		count := 0
		for _, candidate := range held.result.Subfolders() {
			if folderdigest.IsProject(candidate, now) && folderdigest.ShapeFor(candidate) != folderdigest.ShapeCode {
				count++
			}
		}
		recognition := &BuildFolderRecognition{Verdict: BuildFolderCollection, ProjectCount: count}
		if portfolio := folderdigest.Decide(held.result, now).Portfolio; portfolio != nil {
			recognition.Shape = string(portfolio.Shape)
		}
		return recognition, nil
	}
	tombstoned := func(c folderdigest.Candidate) bool { return doc.Tombstoned(FolderKey(c.Path)) }
	// Below a collection and any project of a known kind, the root itself is an
	// inbox folder: Ori recognizes it by its verdict, never by its name.
	if folderdigest.IsInbox(held.result, now, tombstoned) {
		if recognition, ok := inboxRecognition(held); ok {
			return recognition, nil
		}
		return unrecognized, nil
	}
	verdict := folderdigest.Exclude(held.result, now, tombstoned)
	if verdict.Kind != folderdigest.KindProject || verdict.Project == nil {
		return unrecognized, nil
	}
	project := *verdict.Project
	candidateID := ""
	for index, candidate := range held.result.Candidates {
		if candidate.Path == project.Path {
			candidateID = fmt.Sprintf("candidate-%d", index)
			break
		}
	}
	// Only a project the observation disclosed can be named to the model.
	if _, ok := observedReviewCandidate(held, candidateID); !ok {
		return unrecognized, nil
	}
	subject := folderCandidateRecord(project, FolderChoiceProject, "")
	recognition := &BuildFolderRecognition{Verdict: BuildFolderProject, Shape: subject.Shape, ProjectCount: 1, CandidateID: candidateID}
	// The shape's blueprint by its own id. Whether it can be chosen is the
	// build's to say, from the catalog it offers on each turn.
	if blueprint, ok := subjectBlueprint(subject); ok {
		recognition.BlueprintID, recognition.BlueprintLabel = blueprint.BlueprintID, blueprint.Label
	}
	integrationName := ""
	if row, ok := folderdigest.ProjectCapabilityFor(folderdigest.Shape(subject.Shape), subject.MarkerName, subject.DominantExtension); ok {
		offer := row.Offer
		integrationName = offer.IntegrationName
		recognition.IntegrationKey, recognition.IntegrationName = offer.IntegrationKey, offer.IntegrationName
		recognition.HomeProviderKey, recognition.HomeProviderName, recognition.HomeName = offer.HomeProviderKey, offer.HomeProviderName, offer.HomeName
		if offer.HomeProviderKey != "" && d.deps.HomeExists != nil {
			// A Home that cannot be read is said to be absent; the plan re-reads it.
			if exists, err := d.deps.HomeExists(ctx, target.UserID, offer.HomeProviderKey); err == nil {
				recognition.HomeExists = exists
			}
		}
	}
	if label, ok := recognizedProjectLabel(subject); ok {
		recognition.Label = label
	} else if project.Marker != nil {
		recognition.Label = project.Marker.Label
	} else if recognition.BlueprintLabel != "" {
		recognition.Label = recognition.BlueprintLabel
	} else {
		// A project only by the density of its files, of no kind Ori knows.
		return unrecognized, nil
	}
	// The files that say what the project is: the session files a glob marker
	// matches. A marker that is a folder or a single named file has no count.
	if marker := project.Marker; marker != nil && marker.Kind == folderdigest.MarkerGlob {
		if count := project.Extensions[strings.ToLower(filepath.Ext(marker.Name))]; count > 0 {
			recognition.MarkerCount = count
			recognition.MarkerNoun = strings.TrimPrefix(marker.Label, integrationName+" ")
			if integrationName == "" {
				recognition.MarkerNoun = marker.Label
			}
		}
	}
	return recognition, nil
}

// inboxRecognition is an inbox folder as both entries say it: the root as the
// one candidate, set up from the host's File Janitor row. ok is false when the
// observation did not disclose the root, or the host has no such row.
func inboxRecognition(held heldFolderObservation) (*BuildFolderRecognition, bool) {
	row, ok := folderdigest.InboxCapability()
	if !ok {
		return nil, false
	}
	if _, ok := observedReviewCandidate(held, folderRootCandidateID); !ok {
		return nil, false
	}
	return &BuildFolderRecognition{
		Verdict: BuildFolderProject, Shape: string(folderdigest.ShapeInbox), ProjectCount: 1, CandidateID: folderRootCandidateID,
		Label: row.Builtin.DisplayName, BlueprintID: row.Blueprint.BlueprintID, BlueprintLabel: row.Blueprint.Label,
	}, true
}

// isInboxSubject is true for an offer's subject that is an inbox folder's root.
func isInboxSubject(subject FolderCandidateRecord) bool {
	return subject.Kind == FolderChoiceProject && subject.IsRoot && subject.Shape == string(folderdigest.ShapeInbox)
}

// isInboxOffer is true for an offer about an inbox folder that the host's own
// File Janitor setup can answer. A suppressed capability, a supporting-folder
// link and a collection are not one.
func isInboxOffer(offer FolderOffer) bool {
	return !offer.CapabilitySuppressed && offer.Portfolio == nil && offer.Structure == nil && isInboxSubject(offer.Subject)
}

// recognizedProjectLabel names a recognized project the way both entries say
// it: "<integration> project" for a host capability row, or the software
// table's own name. ok is false for any other project.
func recognizedProjectLabel(subject FolderCandidateRecord) (string, bool) {
	if isInboxSubject(subject) {
		if row, ok := folderdigest.InboxCapability(); ok {
			return row.Builtin.DisplayName, true
		}
		return "", false
	}
	if row, ok := folderdigest.ProjectCapabilityFor(folderdigest.Shape(subject.Shape), subject.MarkerName, subject.DominantExtension); ok && row.Offer != nil {
		return row.Offer.IntegrationName + " project", true
	}
	if row, ok := softwareCapability(subject); ok {
		return row.Builtin.DisplayName, true
	}
	return "", false
}
