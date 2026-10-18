package projectlibrary

import (
	"maps"
	"reflect"

	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

// Counts describe operation slots/member references, not unique projects or
// executed effects. No old labels or member IDs survive in this projection.
type ArrangementRevisionDiff struct {
	BaseRevision      int64 `json:"base_revision"`
	Revision          int64 `json:"revision"`
	ChangedOperations int   `json:"changed_operations"`
	AddedMembers      int   `json:"added_members"`
	RemovedMembers    int   `json:"removed_members"`
	ChangedExclusions bool  `json:"changed_exclusions"`
	ChangedQuestions  bool  `json:"changed_questions"`
}

func memberSet(ids []string) map[string]bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}
func arrangementRevisionDiff(before, after workspacearrangement.Record) *ArrangementRevisionDiff {
	diff := &ArrangementRevisionDiff{BaseRevision: before.Revision, Revision: after.Revision, ChangedExclusions: !maps.Equal(memberSet(before.Draft.Unchanged.IDs), memberSet(after.Draft.Unchanged.IDs)), ChangedQuestions: !reflect.DeepEqual(before.Draft.NeedsReview, after.Draft.NeedsReview) || !reflect.DeepEqual(before.Draft.Unknowns, after.Draft.Unknowns)}
	for i := 0; i < max(len(before.Draft.Operations), len(after.Draft.Operations)); i++ {
		var a, b workspacearrangement.Operation
		if i < len(before.Draft.Operations) {
			a = before.Draft.Operations[i]
		}
		if i < len(after.Draft.Operations) {
			b = after.Draft.Operations[i]
		}
		old, next := memberSet(a.Members.IDs), memberSet(b.Members.IDs)
		if a.Kind != b.Kind || a.Name != b.Name || a.CollectionID != b.CollectionID || a.CollectionRevision != b.CollectionRevision || !maps.Equal(old, next) {
			diff.ChangedOperations++
		}
		for id := range next {
			if !old[id] {
				diff.AddedMembers++
			}
		}
		for id := range old {
			if !next[id] {
				diff.RemovedMembers++
			}
		}
	}
	return diff
}
func validArrangementRevisionDiff(saved Arrangement) bool {
	d := saved.RevisionDiff
	return d == nil || (saved.outcomeKind() == "proposal" && d.BaseRevision > 0 && d.Revision == d.BaseRevision+1 && d.Revision <= saved.Record.Revision && d.ChangedOperations >= 0 && d.ChangedOperations <= workspacearrangement.MaxOperations && d.AddedMembers >= 0 && d.AddedMembers <= workspacearrangement.MaxMemberships && d.RemovedMembers >= 0 && d.RemovedMembers <= workspacearrangement.MaxMemberships)
}
