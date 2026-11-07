package rosterstaffing

import "testing"

func TestObservedAssignmentsRemainDetachedBoundedDisclosureNotFitReasons(t *testing.T) {
	fact := Evidence{ID: "assignment-1", Kind: "current_assignment", Value: "manager — Reviewed collection"}
	candidate := Candidate{Evidence: []Evidence{{ID: "purpose", Kind: "declared_purpose", Value: "Coordinates priorities."}, fact}}
	advice := adviceFixture()
	advice.Roles[0].Choices[0].Assignments = candidate.AssignmentEvidence()
	if err := advice.Validate(0); err != nil {
		t.Fatal(err)
	}
	public := advice.Public()
	public.Roles[0].Choices[0].Assignments[0].Value = "Changed"
	if advice.Roles[0].Choices[0].Assignments[0] != fact || candidate.Evidence[1] != fact || len(advice.Roles[0].Choices[0].Reasons) != 1 {
		t.Fatal("assignment disclosure mutated original proof or fit reasons")
	}
	for _, invalid := range []Evidence{
		{ID: "assignment-1", Kind: "declared_purpose", Value: "Unsupported source"},
		{ID: "assignment-1", Kind: "current_assignment", Value: "/private/path"},
		{ID: "assignment-1", Kind: "current_assignment", Value: "Bearer private-token"},
	} {
		advice.Roles[0].Choices[0].Assignments = []Evidence{invalid}
		if advice.Validate(0) == nil {
			t.Fatal("unsupported assignment metadata accepted")
		}
	}
	advice.Roles[0].Choices[0].Assignments = make([]Evidence, 9)
	if advice.Validate(0) == nil {
		t.Fatal("unbounded assignment disclosure accepted")
	}
}
