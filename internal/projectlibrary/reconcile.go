package projectlibrary

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// reconcileResult is what one reconciliation did beyond the entries it
// wrote: relink conflicts it refused to guess at, and relinked entries that
// carry a workspace link (the caller logs those; nothing here touches the
// linked workspace).
type reconcileResult struct {
	RelinkConflicts int
	MovedLinked     []string
}

// reconcileCandidates mutates only the caller's scratch document. Callers
// publish it only after the entire result and current grant pass validation.
// Metadata fields and historical observations are never reconstructed from
// filesystem names or silently moved onto a same-named replacement: a moved
// folder keeps its record only through a relink match (relinkSources), which
// needs the same folder identity, the same format and a shared project-file
// name, and never a guess between several.
func reconcileCandidates(doc *Document, scan Scan, observed Discovery, root Root, store workspace.Store, scope Scope) (reconcileResult, error) {
	var result reconcileResult
	if doc == nil || store == nil || observed.RootID != scan.RootID || observed.Scope != scan.Scope ||
		observed.StartedAt.IsZero() || len(observed.Candidates) > maxEntries {
		return result, ErrCorrupt
	}
	seen := map[string]bool{}
	for _, candidate := range observed.Candidates {
		if !validRelative(candidate.RelativeFolder) || candidate.FileIdentity == "" ||
			!validText(candidate.FileIdentity, 160) ||
			(scan.Scope != "" && candidate.RelativeFolder != scan.Scope &&
				!strings.HasPrefix(candidate.RelativeFolder, scan.Scope+string(filepath.Separator))) ||
			!folderdigest.KnownProjectFormat(candidate.Format) ||
			seen[candidate.RelativeFolder] || len(candidate.Alternates) > 64 {
			return result, ErrCorrupt
		}
		seen[candidate.RelativeFolder] = true
		name := filepath.Base(candidate.RelativeFolder)
		if candidate.RelativeFolder == "" {
			name = filepath.Base(root.Path)
		}
		if name == "" || !validText(name, 160) {
			return result, ErrLimit // never truncate/guess a different project name
		}
		for _, alternate := range candidate.Alternates {
			if alternate == "" || filepath.Base(alternate) != alternate || !validText(alternate, 240) {
				return result, ErrCorrupt
			}
		}
		// A candidate that moved or was replaced after the scan cannot become
		// an association even if its former name still appears in the result.
		if !directoryMatches(root, candidate.RelativeFolder, candidate.FileIdentity) {
			return result, ErrUnavailable
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
			return result, ErrConflict
		}
		linkedState = home.GetAssistantProgramState()
		key := workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID,
			PluginID: scope.ProviderID, ProgramID: scope.ProgramID}
		if linkedState == nil || linkedState.Key.Normalize() != key.Normalize() {
			return result, ErrConflict
		}
		break
	}
	// Every folder this scan saw, before any entry is written: a relink may
	// only take a prior observation this scan did not see again.
	observedKeys := map[string]bool{}
	for _, candidate := range observed.Candidates {
		observedKeys[candidate.RelativeFolder+"\x00"+candidate.FileIdentity] = true
	}
	relinks := planRelinks(*doc, scan, observed, root, observedKeys)
	result.RelinkConflicts = relinks.conflicts
	for index, candidate := range observed.Candidates {
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
			return result, err
		}
		if entryIndex >= 0 {
			// Facts stay only while the file that dates the song is the very
			// file they were read from; otherwise the fact pass reads it again.
			keepFacts(&observation, doc.Entries[entryIndex].Observations[observationIndex], candidate)
			doc.Entries[entryIndex].Observations[observationIndex] = observation
			doc.Entries[entryIndex].Revision++
			continue
		}
		entryIndex, err = findOverlappingObservation(*doc, root, observation)
		if err != nil {
			return result, err
		}
		if entryIndex < 0 {
			entryIndex, err = findExactLinkedEntry(*doc, root, candidate, store, scope, linkedState)
			if err != nil {
				return result, err
			}
		}
		if entryIndex >= 0 {
			if len(doc.Entries[entryIndex].Observations) >= maxRoots {
				return result, ErrLimit
			}
			doc.Entries[entryIndex].Observations = append(doc.Entries[entryIndex].Observations, observation)
			doc.Entries[entryIndex].Revision++
			continue
		}
		if source, ok := relinks.byCandidate[index]; ok {
			// The song moved: its entry keeps its ID, fields, link and history;
			// only the observation follows the folder (FR8). Writing it in place
			// also keeps the unavailable pass below from marking it.
			entry := &doc.Entries[source.entry]
			prior := entry.Observations[source.observation]
			keepFacts(&observation, prior, candidate)
			observation.MovedFrom = &MovedFrom{RootID: prior.RootID, RelativeFolder: prior.RelativeFolder, ScanID: scan.ID}
			entry.Observations[source.observation] = observation
			entry.Revision++
			if entry.Link != nil {
				result.MovedLinked = append(result.MovedLinked, entry.ID)
			}
			continue
		}
		if len(doc.Entries) >= maxEntries {
			return result, ErrLimit
		}
		doc.Entries = append(doc.Entries, Entry{ID: newID(), Revision: 1,
			Observations: []Observation{observation}})
	}
	// A partial scan has not covered every prior observation. Even an empty
	// partial result may not erase or mark any unvisited project as missing.
	if observed.PartialReason != "" {
		return result, nil
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
	return result, nil
}

func keepFacts(observation *Observation, prior Observation, candidate Candidate) {
	if prior.Facts != nil && candidate.DatedFile != nil && prior.Facts.ReadFrom.sameFile(*candidate.DatedFile) &&
		factsFile(candidate.Format, candidate.DatedFile.File) {
		observation.Facts = prior.Facts.clone()
	}
}

type observationRef struct{ entry, observation int }

// relinkPlan maps a candidate (by index in the scan result) to the one prior
// observation it moved from, and counts candidates left alone because more
// than one prior, or a prior claimed by more than one candidate, qualified.
type relinkPlan struct {
	byCandidate map[int]observationRef
	conflicts   int
}

// planRelinks decides every relink of one scan before any entry is written,
// so the outcome does not depend on candidate order. A candidate that the
// exact match, the overlapping-root match or the exact-link adoption claims
// never reaches its relink, as before.
func planRelinks(doc Document, scan Scan, observed Discovery, root Root, observedKeys map[string]bool) relinkPlan {
	plan := relinkPlan{byCandidate: map[int]observationRef{}}
	sources := map[int][]observationRef{}
	claims := map[observationRef][]int{}
	for index, candidate := range observed.Candidates {
		probe := Observation{RootID: root.ID, RelativeFolder: candidate.RelativeFolder, FileIdentity: candidate.FileIdentity}
		if entryIndex, _, err := findExactObservation(doc, probe); err != nil || entryIndex >= 0 {
			continue
		}
		for _, ref := range relinkSources(doc, scan, observed, root, candidate, observedKeys) {
			sources[index] = append(sources[index], ref)
			claims[ref] = append(claims[ref], index)
		}
	}
	for index, refs := range sources {
		if len(refs) == 1 && len(claims[refs[0]]) == 1 {
			plan.byCandidate[index] = refs[0]
			continue
		}
		plan.conflicts++
	}
	return plan
}

// relinkSources lists the prior observations a candidate may have moved from
// (FR7, FR13). All of these must hold:
//   - the same folder identity, the same format and at least one project-file
//     name in common: identity numbers are reused after a delete (Linux), and
//     people reuse names, so neither is enough alone;
//   - a different place (root or folder) under a root that is still connected:
//     a disconnected root is never a relink source;
//   - the prior is gone from its place: already unavailable, about to be
//     marked unavailable by this complete scan, or no longer that folder on
//     disk;
//   - a move into another root needs an entry with no observation there yet
//     (one observation per root).
func relinkSources(doc Document, scan Scan, observed Discovery, root Root, candidate Candidate, observedKeys map[string]bool) []observationRef {
	var refs []observationRef
	complete := observed.PartialReason == ""
	for i, entry := range doc.Entries {
		for j, prior := range entry.Observations {
			if prior.FileIdentity != candidate.FileIdentity || prior.Format != candidate.Format ||
				!sharesName(prior.Alternates, candidate.Alternates) ||
				(prior.RootID == root.ID && prior.RelativeFolder == candidate.RelativeFolder) {
				continue
			}
			source, connected := connectedRoot(doc, prior.RootID)
			if !connected {
				continue
			}
			if prior.RootID != root.ID && observationFor(entry, root.ID) != nil {
				continue
			}
			gone := prior.Availability == "unavailable" ||
				(prior.RootID == root.ID && complete && scanCoversFolder(scan.Scope, prior.RelativeFolder) &&
					!observedKeys[prior.RelativeFolder+"\x00"+prior.FileIdentity]) ||
				!directoryMatches(source, prior.RelativeFolder, prior.FileIdentity)
			if gone {
				refs = append(refs, observationRef{entry: i, observation: j})
			}
		}
	}
	return refs
}

func sharesName(a, b []string) bool {
	for _, left := range a {
		for _, right := range b {
			if left == right {
				return true
			}
		}
	}
	return false
}

func connectedRoot(doc Document, rootID string) (Root, bool) {
	for _, root := range doc.Roots {
		if root.ID == rootID {
			return root, root.RevokedAt == nil
		}
	}
	return Root{}, false
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
