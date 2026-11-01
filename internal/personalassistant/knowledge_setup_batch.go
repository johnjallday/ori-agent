package personalassistant

import "strings"

// SetupBatchSourceKind is the reviewed-knowledge source for what a setup beat
// learns in one sitting: the names that recur in the files the user approved for
// filing (first-week FR17, FR18). It has a pool of its own, so it can neither
// use up nor be blocked by the shared suggestions' 6 pending and 3 a day.
const SetupBatchSourceKind = "setup_batch"

// setupBatchPendingLimit is how many setup-beat learnings wait at once.
const setupBatchPendingLimit = 12

// SetupBatchEvidenceID names one supporting action as the learning card and the
// approval check read it: the Janitor workspace, then the action. Items store no
// scope of their own, so the workspace travels in the evidence.
func SetupBatchEvidenceID(workspaceID, actionID string) string {
	return workspaceID + ":" + actionID
}

// SetupBatchEvidenceParts splits an evidence id made by SetupBatchEvidenceID.
func SetupBatchEvidenceParts(evidenceID string) (workspaceID, actionID string, ok bool) {
	workspaceID, actionID, ok = strings.Cut(evidenceID, ":")
	return workspaceID, actionID, ok && workspaceID != "" && actionID != ""
}
