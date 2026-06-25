package templateonboarding

import (
	"encoding/json"
	"strings"
	"testing"
)

// parseValidate is a helper that parses a raw onboarding block and validates it,
// failing the test on a parse error (the cases here are all valid JSON).
func parseValidate(t *testing.T, raw string) ValidationResult {
	t.Helper()
	spec, err := ParseSpec(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("ParseSpec(%s): %v", raw, err)
	}
	return Validate(spec)
}

func TestValidateActionOnlyNoneIsValid(t *testing.T) {
	res := parseValidate(t, `{"version":"1","completion":{"type":"none","instantiate_skeleton":true}}`)
	if !res.OK() {
		t.Fatalf("expected valid, got problems: %v", res.Problems)
	}
}

func TestValidateReaperLikeSpecIsValid(t *testing.T) {
	res := parseValidate(t, `{
		"version":"1",
		"fields":[
			{"id":"bpm","label":"BPM","type":"number","default":120,"validation":{"min":20,"max":300}},
			{"id":"key","label":"Key","type":"enum","options":["C","D","E"],"default":"C"},
			{"id":"song_name","label":"Song name","type":"string","required":true}
		],
		"completion":{
			"type":"task",
			"instructions":"Create the REAPER project",
			"skill_refs":["reaper"],
			"inputs":{"bpm":"${fields.bpm}","name":"${fields.song_name}"},
			"instantiate_skeleton":true
		},
		"dependencies":[{"type":"skill","ref":"reaper"}]
	}`)
	if !res.OK() {
		t.Fatalf("expected valid, got problems: %v", res.Problems)
	}
}

func TestValidateBadFieldID(t *testing.T) {
	for _, id := range []string{"BPM", "1bpm", "has-dash", ""} {
		raw := `{"fields":[{"id":"` + id + `","label":"x","type":"string"}],"completion":{"type":"none"}}`
		res := parseValidate(t, raw)
		if res.OK() {
			t.Errorf("id %q: expected invalid, got OK", id)
		}
	}
}

func TestValidateDuplicateFieldID(t *testing.T) {
	res := parseValidate(t, `{"fields":[
		{"id":"x","label":"A","type":"string"},
		{"id":"x","label":"B","type":"string"}
	],"completion":{"type":"none"}}`)
	if res.OK() || !hasProblemContaining(res, "duplicate") {
		t.Fatalf("expected duplicate-id problem, got: %v", res.Problems)
	}
}

func TestValidateEnumRequiresOptions(t *testing.T) {
	res := parseValidate(t, `{"fields":[{"id":"k","label":"K","type":"enum"}],"completion":{"type":"none"}}`)
	if res.OK() || !hasProblemContaining(res, "options") {
		t.Fatalf("expected enum-options problem, got: %v", res.Problems)
	}
}

func TestValidateDefaultTypeMismatch(t *testing.T) {
	res := parseValidate(t, `{"fields":[{"id":"bpm","label":"BPM","type":"number","default":"fast"}],"completion":{"type":"none"}}`)
	if res.OK() || !hasProblemContaining(res, "default must be a number") {
		t.Fatalf("expected number-default problem, got: %v", res.Problems)
	}
}

func TestValidateEnumDefaultNotInOptions(t *testing.T) {
	res := parseValidate(t, `{"fields":[{"id":"k","label":"K","type":"enum","options":["C","D"],"default":"Z"}],"completion":{"type":"none"}}`)
	if res.OK() || !hasProblemContaining(res, "not one of the options") {
		t.Fatalf("expected enum-default problem, got: %v", res.Problems)
	}
}

func TestValidateUnknownFieldReference(t *testing.T) {
	res := parseValidate(t, `{"fields":[{"id":"bpm","label":"BPM","type":"number"}],
		"completion":{"type":"task","inputs":{"x":"${fields.nope}"}}}`)
	if res.OK() || !hasProblemContaining(res, "unknown field") {
		t.Fatalf("expected unknown-reference problem, got: %v", res.Problems)
	}
}

func TestValidateReservedTypeValidatesButNotExecutable(t *testing.T) {
	res := parseValidate(t, `{"completion":{"type":"tool","ref":"some_tool"}}`)
	if !res.OK() {
		t.Fatalf("reserved type with ref should validate, got: %v", res.Problems)
	}
	if ActionTool.ExecutableInV1() {
		t.Fatal("tool should not be executable in v1")
	}
}

func TestValidateReservedTypeRequiresRef(t *testing.T) {
	res := parseValidate(t, `{"completion":{"type":"workflow_template"}}`)
	if res.OK() || !hasProblemContaining(res, "requires a ref") {
		t.Fatalf("expected missing-ref problem, got: %v", res.Problems)
	}
}

func TestValidateUnknownVersionDisables(t *testing.T) {
	res := parseValidate(t, `{"version":"2","completion":{"type":"none"}}`)
	if res.OK() || !hasProblemContaining(res, "unsupported onboarding version") {
		t.Fatalf("expected version problem, got: %v", res.Problems)
	}
}

func TestValidateMinGreaterThanMax(t *testing.T) {
	res := parseValidate(t, `{"fields":[{"id":"n","label":"N","type":"number","validation":{"min":10,"max":1}}],"completion":{"type":"none"}}`)
	if res.OK() || !hasProblemContaining(res, "greater than max") {
		t.Fatalf("expected min/max problem, got: %v", res.Problems)
	}
}

func TestValidateBadPattern(t *testing.T) {
	res := parseValidate(t, `{"fields":[{"id":"s","label":"S","type":"string","validation":{"pattern":"["}}],"completion":{"type":"none"}}`)
	if res.OK() || !hasProblemContaining(res, "not a valid regexp") {
		t.Fatalf("expected pattern problem, got: %v", res.Problems)
	}
}

func TestValidateUnknownActionType(t *testing.T) {
	res := parseValidate(t, `{"completion":{"type":"explode"}}`)
	if res.OK() || !hasProblemContaining(res, "unknown action type") {
		t.Fatalf("expected unknown-action problem, got: %v", res.Problems)
	}
}

func TestValidateDependencyShape(t *testing.T) {
	res := parseValidate(t, `{"completion":{"type":"none"},"dependencies":[{"type":"bogus","ref":""}]}`)
	if res.OK() || !hasProblemContaining(res, "dependencies[0]") {
		t.Fatalf("expected dependency problems, got: %v", res.Problems)
	}
}

func hasProblemContaining(res ValidationResult, substr string) bool {
	for _, p := range res.Problems {
		if strings.Contains(p, substr) {
			return true
		}
	}
	return false
}
