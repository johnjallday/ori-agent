package workspacerun

import (
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// memoryDiffRole tags the trace artifact that records what a run learned —
// the entries added to or removed from workspace memory during the run.
const memoryDiffRole = "memory_diff"

// snapshotMemoryLines returns the workspace's memory entries rendered as their
// canonical lines, for before/after diffing. Returns nil when memory can't be
// read (no resolver, non-folder store, missing workspace) — callers treat a nil
// snapshot as "memory unobservable", which yields an empty diff.
func snapshotMemoryLines(resolver workspaceFolderResolver, workspaceID string) []string {
	if resolver == nil || workspaceID == "" {
		return nil
	}
	doc, err := workspace.NewMemoryStore(resolver).Read(workspaceID)
	if err != nil {
		return nil
	}
	entries := doc.Entries()
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		lines = append(lines, e.Render())
	}
	return lines
}

// diffMemoryLines reports which entry lines were added (in after, not before)
// and removed (in before, not after). Order follows the slice the line came
// from. An edit surfaces as one removal plus one addition, which is the
// intended representation for "what changed".
func diffMemoryLines(before, after []string) (added, removed []string) {
	beforeSet := make(map[string]int, len(before))
	for _, l := range before {
		beforeSet[l]++
	}
	afterSet := make(map[string]int, len(after))
	for _, l := range after {
		afterSet[l]++
	}
	for _, l := range after {
		if afterSet[l] > beforeSet[l] {
			added = append(added, l)
			afterSet[l]-- // count each surplus occurrence once
		}
	}
	for _, l := range before {
		if beforeSet[l] > afterSet[l] {
			removed = append(removed, l)
			beforeSet[l]--
		}
	}
	return added, removed
}

// memoryDiffArtifact builds the run's "what it learned" trace artifact, or nil
// when nothing changed (so empty diffs add no noise to the run record).
func memoryDiffArtifact(runID string, before, after []string) *Artifact {
	added, removed := diffMemoryLines(before, after)
	if len(added) == 0 && len(removed) == 0 {
		return nil
	}
	artifact := NewArtifact(runID, ArtifactTrace, ArtifactMetadata(map[string]any{
		"role":    memoryDiffRole,
		"added":   added,
		"removed": removed,
	}))
	return &artifact
}
