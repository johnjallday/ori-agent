package overview

import (
	"encoding/json"
	"testing"
)

// decode round-trips a snapshot through JSON so assertions run against what a
// consumer actually receives, not against the in-memory struct.
func decode(t *testing.T, snapshot Snapshot) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return decoded
}

func firstFeature(t *testing.T, decoded map[string]any) map[string]any {
	t.Helper()
	features, ok := decoded["features"].([]any)
	if !ok || len(features) == 0 {
		t.Fatalf("payload carried no features: %v", decoded)
	}
	feature, ok := features[0].(map[string]any)
	if !ok {
		t.Fatalf("feature was not an object: %v", features[0])
	}
	return feature
}

func TestJSONCarriesSchemaVersionAndCompleteness(t *testing.T) {
	snapshot := baseSnapshot(feature("anything"))
	snapshot.Complete = false

	decoded := decode(t, snapshot)
	if decoded["schema_version"] != float64(SchemaVersion) {
		t.Fatalf("schema_version = %v, want %d", decoded["schema_version"], SchemaVersion)
	}
	if decoded["complete"] != false {
		t.Fatalf("complete = %v, want false", decoded["complete"])
	}
	if _, ok := decoded["generated_at"]; !ok {
		t.Fatal("payload carried no generation timestamp")
	}
}

func TestJSONDistinguishesUnknownFromAbsentAndZero(t *testing.T) {
	// Three plans that a naive encoder would flatten into "0 of 0 done".
	absent := feature("absent-plan")
	absent.Phase = PhaseState{Phase: PhaseUnknown}

	unknown := feature("unknown-plan")
	unknown.Plan.Progress = PlanProgress{Availability: AvailabilityUnknown}

	real := feature("real-zero")
	real.Plan.Progress = PlanProgress{
		Availability:    AvailabilityAvailable,
		MilestonesTotal: 7,
		SubtasksTotal:   136,
	}

	snapshot := baseSnapshot(absent, unknown, real)
	features, _ := decode(t, snapshot)["features"].([]any)

	want := []string{"absent", "unknown", "available"}
	for index, expected := range want {
		row := features[index].(map[string]any)
		progress := row["plan"].(map[string]any)["progress"].(map[string]any)
		if got := progress["availability"]; got != expected {
			t.Fatalf("feature %d availability = %v, want %q", index, got, expected)
		}
	}

	// Genuine zero progress must still carry its real totals.
	third := features[2].(map[string]any)["plan"].(map[string]any)["progress"].(map[string]any)
	if third["subtasks_total"] != float64(136) || third["subtasks_completed"] != float64(0) {
		t.Fatalf("real zero progress lost its totals: %v", third)
	}
}

func TestJSONEncodesUnknownAvailabilityExplicitly(t *testing.T) {
	// An empty string would read to a consumer as a missing field rather than
	// as a deliberate "no determination was made".
	snapshot := baseSnapshot(feature("unmeasured"))
	feature := firstFeature(t, decode(t, snapshot))
	git := feature["git"].(map[string]any)
	if git["availability"] != "unknown" {
		t.Fatalf("git availability = %v, want the explicit \"unknown\" spelling", git["availability"])
	}
}

func TestJSONRoundTripsAvailability(t *testing.T) {
	for _, availability := range []Availability{
		AvailabilityAvailable, AvailabilityAbsent, AvailabilityMalformed,
		AvailabilityUnavailable, AvailabilityStale, AvailabilityUnknown,
	} {
		encoded, err := json.Marshal(availability)
		if err != nil {
			t.Fatalf("marshal %q: %v", availability, err)
		}
		var decoded Availability
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("unmarshal %s: %v", encoded, err)
		}
		if decoded != availability {
			t.Fatalf("round trip of %q produced %q", availability, decoded)
		}
	}
}

func TestJSONCarriesPlanProvenanceAndNextAction(t *testing.T) {
	row := feature("measured", withWorktree("/w/measured"))
	progressed(&row)

	plan := firstFeature(t, decode(t, baseSnapshot(row)))["plan"].(map[string]any)
	if plan["copy"] != string(PlanCopyActive) {
		t.Fatalf("copy = %v, want the active worktree copy named", plan["copy"])
	}
	if plan["task_list_path"] == nil {
		t.Fatal("task list path was not carried")
	}
	progress := plan["progress"].(map[string]any)
	next := progress["next_actionable"].(map[string]any)
	if next["ordinal"] != "5.1" {
		t.Fatalf("next ordinal = %v, want 5.1", next["ordinal"])
	}
	if progress["delivery_checkpoints_remaining"] != float64(4) {
		t.Fatalf("remaining checkpoints = %v, want 4", progress["delivery_checkpoints_remaining"])
	}
}

func TestJSONOmitsEmptyOptionalStructsButKeepsRequiredState(t *testing.T) {
	row := feature("bare")
	encoded, err := json.Marshal(baseSnapshot(row))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	decoded := firstFeature(t, decode(t, baseSnapshot(row)))

	// Absent optional detail should not appear as null noise...
	if _, present := decoded["agents"]; present {
		t.Fatalf("empty agent list was encoded: %s", encoded)
	}
	// ...but every state-bearing field must always be present.
	for _, required := range []string{"slug", "phase", "plan", "backlog", "git", "remote"} {
		if _, present := decoded[required]; !present {
			t.Fatalf("required field %q was omitted: %s", required, encoded)
		}
	}
}
