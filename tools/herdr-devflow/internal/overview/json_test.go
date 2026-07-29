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

// rosterAgent finds one agent in the encoded roster by pane.
func rosterAgent(t *testing.T, decoded map[string]any, pane string) map[string]any {
	t.Helper()
	agents, ok := decoded["agents"].([]any)
	if !ok {
		t.Fatalf("payload carried no agent roster: %v", decoded)
	}
	for _, entry := range agents {
		agent, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("agent was not an object: %v", entry)
		}
		live, _ := agent["live"].(map[string]any)
		saved, _ := agent["saved"].(map[string]any)
		if (live != nil && live["pane"] == pane) || (saved != nil && saved["pane"] == pane) {
			return agent
		}
	}
	t.Fatalf("no agent with pane %q in %v", pane, agents)
	return nil
}

// TestJSONCarriesTheWholeAgentRoster locks in the contract an Overnight Run
// selector consumes: every agent, its scope, kind, activity, worktree, feature,
// eligibility, and reason, without parsing terminal output. FR15.
func TestJSONCarriesTheWholeAgentRoster(t *testing.T) {
	decoded := decode(t, newHerdScenario(t).snapshot(t))

	managed := rosterAgent(t, decoded, "w-managed:p1")
	for field, want := range map[string]any{
		"feature": scenarioManagedFeature,
		"scope":   string(AgentScopeFeature),
		"managed": true,
		"kind":    claudeKind,
		"status":  string(AgentIdle),
		"binding": string(BindingExact),
	} {
		if managed[field] != want {
			t.Fatalf("managed agent %q = %v, want %v", field, managed[field], want)
		}
	}
	if managed["matched_path"] == nil || managed["matched_path"] == "" {
		t.Fatalf("managed agent carried no worktree: %v", managed)
	}
	if _, present := managed["run"]; present {
		t.Fatalf("an agent in no Overnight Run carried run membership: %v", managed["run"])
	}
	eligibility, ok := managed["eligibility"].(map[string]any)
	if !ok {
		t.Fatalf("managed agent carried no eligibility: %v", managed)
	}
	// The structural requirements hold, but Claude's readiness has not been
	// established, so the honest answer is unverified — never eligible.
	if eligibility["state"] != string(EligibilityUnverified) || eligibility["reason"] == "" {
		t.Fatalf("eligibility = %v, want an unverified state with a reason", eligibility)
	}

	repository := rosterAgent(t, decoded, "w-dev:p1")
	if repository["scope"] != string(AgentScopeRepository) || repository["feature"] != "" {
		t.Fatalf("repository agent = %v, want repository scope and no feature", repository)
	}
	repositoryEligibility := repository["eligibility"].(map[string]any)
	if repositoryEligibility["state"] != string(EligibilityIneligible) {
		t.Fatalf("repository agent eligibility = %v, want ineligible", repositoryEligibility)
	}

	unplaced := rosterAgent(t, decoded, "w-shared:p1")
	if unplaced["scope"] != "unknown" {
		t.Fatalf("unplaced agent scope = %v, want the explicit \"unknown\" spelling", unplaced["scope"])
	}
}

// TestJSONCarriesEveryCheckoutWithOccupancy proves a consumer can see the
// repository's non-feature checkouts without inferring them from agent paths.
func TestJSONCarriesEveryCheckoutWithOccupancy(t *testing.T) {
	decoded := decode(t, newHerdScenario(t).snapshot(t))

	checkouts, ok := decoded["checkouts"].([]any)
	if !ok || len(checkouts) != 4 {
		t.Fatalf("checkouts = %v, want all four working copies", decoded["checkouts"])
	}
	baselines := 0
	for _, entry := range checkouts {
		checkout := entry.(map[string]any)
		if checkout["path"] == nil || checkout["path"] == "" {
			t.Fatalf("checkout carried no path: %v", checkout)
		}
		if _, present := checkout["occupancy"]; !present {
			t.Fatalf("checkout carried no occupancy: %v", checkout)
		}
		if checkout["baseline"] == true {
			baselines++
		}
	}
	if baselines != 2 {
		t.Fatalf("baseline checkouts = %d, want the main source checkout and the dev worktree", baselines)
	}
}

func TestJSONRoundTripsAgentScope(t *testing.T) {
	for _, scope := range []AgentScope{AgentScopeFeature, AgentScopeRepository, AgentScopeUnknown} {
		encoded, err := json.Marshal(scope)
		if err != nil {
			t.Fatalf("marshal %q: %v", scope, err)
		}
		var decoded AgentScope
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("unmarshal %s: %v", encoded, err)
		}
		if decoded != scope {
			t.Fatalf("round trip of %q produced %q", scope, decoded)
		}
	}
}
