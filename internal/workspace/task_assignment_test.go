package workspace

import "testing"

func TestBackfillTaskAssignmentProvenance(t *testing.T) {
	ws := &Workspace{
		Tasks: []Task{
			{ID: "legacy", To: "Writer"}, // no provenance -> backfill
			{ID: "manual", To: "Writer", AssignmentMode: TaskAssignmentModeManual}, // already stamped -> untouched
		},
	}

	if changed := backfillTaskAssignmentProvenance(ws); !changed {
		t.Fatal("backfillTaskAssignmentProvenance() = false, want true on first pass")
	}

	if got := ws.Tasks[0].AssignmentMode; got != TaskAssignmentModeLegacyUnknown {
		t.Fatalf("legacy task AssignmentMode = %q, want %q", got, TaskAssignmentModeLegacyUnknown)
	}
	// Existing assignee must be preserved exactly.
	if got := ws.Tasks[0].To; got != "Writer" {
		t.Fatalf("legacy task To = %q, want it preserved as %q", got, "Writer")
	}
	// An already-stamped task must not be rewritten.
	if got := ws.Tasks[1].AssignmentMode; got != TaskAssignmentModeManual {
		t.Fatalf("manual task AssignmentMode = %q, want it left as %q", got, TaskAssignmentModeManual)
	}

	// Idempotent: a second pass changes nothing.
	if changed := backfillTaskAssignmentProvenance(ws); changed {
		t.Fatal("backfillTaskAssignmentProvenance() = true on second pass, want false (idempotent)")
	}
}

func TestBackfillTaskAssignmentProvenanceNilWorkspace(t *testing.T) {
	if changed := backfillTaskAssignmentProvenance(nil); changed {
		t.Fatal("backfillTaskAssignmentProvenance(nil) = true, want false")
	}
}

func TestIsValidTaskAssignmentMode(t *testing.T) {
	valid := []TaskAssignmentMode{
		TaskAssignmentModeStaticPlan,
		TaskAssignmentModeManual,
		TaskAssignmentModeDynamicDelegation,
		TaskAssignmentModeLegacyUnknown,
	}
	for _, m := range valid {
		if !IsValidTaskAssignmentMode(m) {
			t.Errorf("IsValidTaskAssignmentMode(%q) = false, want true", m)
		}
	}
	if IsValidTaskAssignmentMode("") || IsValidTaskAssignmentMode("bogus") {
		t.Error("IsValidTaskAssignmentMode accepted an invalid value")
	}
}

func TestEntryAgentDefaultModeIsValid(t *testing.T) {
	if !IsValidTaskAssignmentMode(TaskAssignmentModeEntryAgentDefault) {
		t.Fatalf("IsValidTaskAssignmentMode(%q) = false, want true", TaskAssignmentModeEntryAgentDefault)
	}
	// The sweep attributes claimed tasks to the system sentinel, distinct from
	// the manual sentinel, so audits can tell a claim from a user assignment.
	if TaskAssignedBySystem == TaskAssignedByManual {
		t.Fatal("TaskAssignedBySystem must differ from TaskAssignedByManual")
	}
}
