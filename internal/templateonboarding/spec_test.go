package templateonboarding

import (
	"encoding/json"
	"testing"
)

func TestParseSpecEmptyOrNullIsNoOnboarding(t *testing.T) {
	for _, raw := range []string{"", "   ", "null", "  null  "} {
		spec, err := ParseSpec(json.RawMessage(raw))
		if err != nil {
			t.Errorf("ParseSpec(%q): unexpected error %v", raw, err)
		}
		if spec != nil {
			t.Errorf("ParseSpec(%q): expected nil spec, got %+v", raw, spec)
		}
	}
}

func TestParseSpecDefaultsVersion(t *testing.T) {
	spec, err := ParseSpec(json.RawMessage(`{"completion":{"type":"none"}}`))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if spec.Version != DefaultVersion {
		t.Errorf("expected version %q, got %q", DefaultVersion, spec.Version)
	}
}

func TestParseSpecInvalidJSONErrors(t *testing.T) {
	if _, err := ParseSpec(json.RawMessage(`{not json`)); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestParseSpecAllowsUnknownTopLevelKeys(t *testing.T) {
	spec, err := ParseSpec(json.RawMessage(`{"version":"1","completion":{"type":"none"},"future_key":true}`))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if spec == nil || spec.Completion.Type != ActionNone {
		t.Fatalf("unexpected spec: %+v", spec)
	}
}

func TestParseSpecEmptyFieldsActionOnly(t *testing.T) {
	spec, err := ParseSpec(json.RawMessage(`{"completion":{"type":"none","instantiate_skeleton":true}}`))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if len(spec.Fields) != 0 {
		t.Errorf("expected no fields, got %d", len(spec.Fields))
	}
	if !spec.Completion.InstantiateSkeleton {
		t.Errorf("expected instantiate_skeleton true")
	}
}

func TestActionTypeExecutableInV1(t *testing.T) {
	for _, tc := range []struct {
		typ  ActionType
		want bool
	}{
		{ActionNone, true},
		{ActionTask, true},
		{ActionTool, false},
		{ActionWorkflowTemplate, false},
	} {
		if got := tc.typ.ExecutableInV1(); got != tc.want {
			t.Errorf("%q.ExecutableInV1() = %v, want %v", tc.typ, got, tc.want)
		}
	}
}
