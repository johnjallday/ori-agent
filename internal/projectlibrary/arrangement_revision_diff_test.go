package projectlibrary

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func TestArrangementRevisionDiff_CountsReferencesWithoutLosingOverlapSemantics(t *testing.T) {
	operation := func(name string, ids ...string) workspacearrangement.Operation {
		return workspacearrangement.Operation{Kind: workspacearrangement.CreateCollection, Name: name, Members: workspacearrangement.Selection{IDs: ids}}
	}
	before := workspacearrangement.Record{Revision: 1, Draft: workspacearrangement.Draft{Operations: []workspacearrangement.Operation{operation("First", "a", "b"), operation("Second", "b", "c")}, Unchanged: workspacearrangement.Selection{IDs: []string{"x"}}}}
	after := workspacearrangement.Record{Revision: 2, Draft: workspacearrangement.Draft{Operations: []workspacearrangement.Operation{operation("Renamed", "b", "d"), operation("Second", "b")}, Unchanged: workspacearrangement.Selection{IDs: []string{"y"}}, Unknowns: []string{"Ownership remains unclear"}}}
	diff := arrangementRevisionDiff(before, after)
	if diff.ChangedOperations != 2 || diff.AddedMembers != 1 || diff.RemovedMembers != 2 || !diff.ChangedExclusions || !diff.ChangedQuestions || !validArrangementRevisionDiff(Arrangement{Record: after, RevisionDiff: diff}) {
		t.Fatal(diff)
	}
	// Reordering one exact set does not manufacture a membership edit.
	after.Draft = before.Draft
	after.Draft.Operations = []workspacearrangement.Operation{operation("First", "b", "a"), operation("Second", "c", "b")}
	diff = arrangementRevisionDiff(before, after)
	if diff.ChangedOperations != 0 || diff.AddedMembers != 0 || diff.RemovedMembers != 0 || diff.ChangedExclusions || diff.ChangedQuestions {
		t.Fatal(diff)
	}
	diff.Revision = 3
	if validArrangementRevisionDiff(Arrangement{Record: after, RevisionDiff: diff}) {
		t.Fatal("future diff accepted")
	}
}
