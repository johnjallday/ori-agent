package projectlibrary

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// reconcileCandidates mutates only the caller's scratch document. Callers
// publish it only after the entire result and current grant pass validation.
// Metadata fields and historical observations are never reconstructed from
// filesystem names or silently moved onto a same-named replacement.
func reconcileCandidates(doc *Document, scan Scan, observed Discovery, root Root, store workspace.Store, scope Scope) error {
	if doc == nil || store == nil || observed.RootID != scan.RootID || observed.Scope != scan.Scope ||
		observed.StartedAt.IsZero() || len(observed.Candidates) > maxEntries {
		return ErrCorrupt
	}
	seen := map[string]bool{}
	for _, candidate := range observed.Candidates {
		if !validRelative(candidate.RelativeFolder) || candidate.FileIdentity == "" ||
			!validText(candidate.FileIdentity, 160) ||
			(scan.Scope != "" && candidate.RelativeFolder != scan.Scope &&
				!strings.HasPrefix(candidate.RelativeFolder, scan.Scope+string(filepath.Separator))) ||
			!folderdigest.KnownProjectFormat(candidate.Format) ||
			seen[candidate.RelativeFolder] || len(candidate.Alternates) > 64 {
			return ErrCorrupt
		}
		seen[candidate.RelativeFolder] = true
		name := filepath.Base(candidate.RelativeFolder)
		if candidate.RelativeFolder == "" {
			name = filepath.Base(root.Path)
		}
		if name == "" || !validText(name, 160) {
			return ErrLimit // never truncate/guess a different project name
		}
		for _, alternate := range candidate.Alternates {
			if alternate == "" || filepath.Base(alternate) != alternate || !validText(alternate, 240) {
				return ErrCorrupt
			}
		}
		// A candidate that moved or was replaced after the scan cannot become
		// an association even if its former name still appears in the result.
		if !directoryMatches(root, candidate.RelativeFolder, candidate.FileIdentity) {
			return ErrUnavailable
		}
	}
	// Read Home membership once, not for every discovered folder. The exact
	// child link is still rechecked when a link-only entry could be adopted.
	var linkedState *workspace.AssistantProgramState
	for _, entry := range doc.Entries {
		if entry.Link == nil || len(entry.Observations) != 0 {
			continue
		}
		home, err := store.Get(scope.HomeID)
		if err != nil || home == nil {
			return ErrConflict
		}
		linkedState = home.GetAssistantProgramState()
		key := workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID,
			PluginID: scope.ProviderID, ProgramID: scope.ProgramID}
		if linkedState == nil || linkedState.Key.Normalize() != key.Normalize() {
			return ErrConflict
		}
		break
	}
	observedKeys := map[string]bool{}
	for _, candidate := range observed.Candidates {
		key := candidate.RelativeFolder + "\x00" + candidate.FileIdentity
		observedKeys[key] = true
		observation := Observation{RootID: root.ID, RelativeFolder: candidate.RelativeFolder,
			FileIdentity: candidate.FileIdentity, Format: candidate.Format,
			Alternates: append([]string(nil), candidate.Alternates...),
			ScanID:     scan.ID, ScannedAt: observed.StartedAt, LastCheckedAt: observed.StartedAt,
			FileModifiedAt: candidate.FileModifiedAt.UTC(), Availability: "available"}
		if candidate.Ambiguous {
			observation.Availability = "ambiguous"
		}
		entryIndex, observationIndex, err := findExactObservation(*doc, observation)
		if err != nil {
			return err
		}
		if entryIndex >= 0 {
			// Facts stay only while the file that dates the song is the very
			// file they were read from; otherwise the fact pass reads it again.
			if prior := doc.Entries[entryIndex].Observations[observationIndex].Facts; prior != nil &&
				candidate.DatedFile != nil && prior.ReadFrom.sameFile(*candidate.DatedFile) &&
				factsFile(candidate.Format, candidate.DatedFile.File) {
				observation.Facts = prior.clone()
			}
			doc.Entries[entryIndex].Observations[observationIndex] = observation
			doc.Entries[entryIndex].Revision++
			continue
		}
		entryIndex, err = findOverlappingObservation(*doc, root, observation)
		if err != nil {
			return err
		}
		if entryIndex < 0 {
			entryIndex, err = findExactLinkedEntry(*doc, root, candidate, store, scope, linkedState)
			if err != nil {
				return err
			}
		}
		if entryIndex >= 0 {
			if len(doc.Entries[entryIndex].Observations) >= maxRoots {
				return ErrLimit
			}
			doc.Entries[entryIndex].Observations = append(doc.Entries[entryIndex].Observations, observation)
			doc.Entries[entryIndex].Revision++
			continue
		}
		if len(doc.Entries) >= maxEntries {
			return ErrLimit
		}
		doc.Entries = append(doc.Entries, Entry{ID: newID(), Revision: 1,
			Observations: []Observation{observation}})
	}
	// A partial scan has not covered every prior observation. Even an empty
	// partial result may not erase or mark any unvisited project as missing.
	if observed.PartialReason != "" {
		return nil
	}
	for i := range doc.Entries {
		for j := range doc.Entries[i].Observations {
			prior := &doc.Entries[i].Observations[j]
			if prior.RootID != root.ID || !scanCoversFolder(scan.Scope, prior.RelativeFolder) ||
				observedKeys[prior.RelativeFolder+"\x00"+prior.FileIdentity] {
				continue
			}
			if prior.Availability != "unavailable" {
				prior.Availability = "unavailable"
				prior.LastCheckedAt = observed.StartedAt
				doc.Entries[i].Revision++
			}
		}
	}
	return nil
}

func scanCoversFolder(scanScope, relative string) bool {
	if scanScope == "" {
		return relative == "" || !strings.ContainsRune(relative, filepath.Separator)
	}
	if relative == scanScope {
		return true
	}
	prefix := scanScope + string(filepath.Separator)
	if !strings.HasPrefix(relative, prefix) {
		return false
	}
	return !strings.ContainsRune(strings.TrimPrefix(relative, prefix), filepath.Separator)
}

func findExactObservation(doc Document, candidate Observation) (int, int, error) {
	foundEntry, foundObservation := -1, -1
	for i, entry := range doc.Entries {
		for j, prior := range entry.Observations {
			if prior.RootID == candidate.RootID && prior.RelativeFolder == candidate.RelativeFolder &&
				prior.FileIdentity == candidate.FileIdentity {
				if foundEntry >= 0 {
					return -1, -1, ErrConflict
				}
				foundEntry, foundObservation = i, j
			}
		}
	}
	return foundEntry, foundObservation, nil
}

func findOverlappingObservation(doc Document, root Root, candidate Observation) (int, error) {
	newPath := filepath.Join(root.Path, candidate.RelativeFolder)
	found := -1
	for i, entry := range doc.Entries {
		for _, prior := range entry.Observations {
			if prior.RootID == root.ID || prior.FileIdentity != candidate.FileIdentity ||
				(prior.Availability != "available" && prior.Availability != "ambiguous") {
				continue
			}
			for _, otherRoot := range doc.Roots {
				if otherRoot.ID == prior.RootID && otherRoot.RevokedAt == nil &&
					filepath.Join(otherRoot.Path, prior.RelativeFolder) == newPath &&
					directoryMatches(otherRoot, prior.RelativeFolder, candidate.FileIdentity) {
					if found >= 0 && found != i {
						return -1, ErrConflict
					}
					found = i
				}
			}
		}
	}
	return found, nil
}

func findExactLinkedEntry(doc Document, root Root, candidate Candidate, store workspace.Store,
	scope Scope, state *workspace.AssistantProgramState) (int, error) {
	if state == nil {
		return -1, nil
	}
	selectedPath := filepath.Join(root.Path, candidate.RelativeFolder)
	found := -1
	key := workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID, PluginID: scope.ProviderID, ProgramID: scope.ProgramID}
	for i, entry := range doc.Entries {
		if entry.Link == nil || len(entry.Observations) != 0 {
			continue
		}
		listed := false
		for _, id := range state.LinkedProjectIDs {
			listed = listed || id == entry.Link.WorkspaceID
		}
		if !listed || entry.Link.LinkID != workspace.AssistantProjectLinkID(scope.HomeID, entry.Link.WorkspaceID) {
			return -1, ErrConflict
		}
		child, err := store.Get(entry.Link.WorkspaceID)
		if err != nil || child == nil || child.Status == workspace.StatusTrashed || child.Status == workspace.StatusMissing ||
			(workspace.AssistantProgramKey{OwnerUserID: child.OwnerUserID}).Normalize().OwnerUserID != scope.OwnerUserID {
			return -1, ErrConflict
		}
		link := child.GetAssistantProjectLink()
		if link == nil || link.ID != entry.Link.LinkID || link.StationWorkspaceID != scope.HomeID ||
			link.Key.Normalize() != key.Normalize() || link.StateRevision != entry.Link.Revision {
			return -1, ErrConflict
		}
		locator, err := workspace.GetProjectEntryLocator(child.SharedData)
		if err != nil {
			return -1, ErrConflict
		}
		if locator == nil || locator.Kind != workspace.ProjectEntryDirectoryReference {
			continue
		}
		reference, err := child.GetDirectoryReference(locator.DirectoryReferenceID)
		if err != nil || reference == nil {
			return -1, ErrConflict
		}
		canonical, err := filepath.EvalSymlinks(reference.Path)
		if err != nil || filepath.Clean(canonical) != selectedPath {
			continue
		}
		entrySelected := false
		for _, alternative := range candidate.Alternates {
			entrySelected = entrySelected || alternative == locator.RelativePath
		}
		selectedMarker, markerOK := folderdigest.MatchMarker(locator.RelativePath, false)
		if !entrySelected || !markerOK || selectedMarker.ProjectFormat != candidate.Format ||
			!directoryMatches(root, candidate.RelativeFolder, candidate.FileIdentity) {
			return -1, ErrConflict
		}
		if found >= 0 {
			return -1, ErrConflict
		}
		found = i
	}
	return found, nil
}

// cloneEntries is the isolation boundary between fallible reconciliation and
// a completed Home transaction: a rejected candidate must not leave partial
// observational changes on a failure receipt.
func cloneEntries(entries []Entry) ([]Entry, error) {
	encoded, err := json.Marshal(entries)
	if err != nil {
		return nil, ErrCorrupt
	}
	var cloned []Entry
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return nil, ErrCorrupt
	}
	return cloned, nil
}
