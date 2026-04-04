package toolapi

import "testing"

func TestParseStructuredResult_RequiresStructuredMetadata(t *testing.T) {
	if _, err := ParseStructuredResult(`{"sessions":[{"id":"session-1"}],"total":1}`); err == nil {
		t.Fatal("expected plain JSON tool payload to be rejected as a structured result")
	}
}

func TestParseStructuredResult_AcceptsStructuredJSON(t *testing.T) {
	result, err := ParseStructuredResult(`{"displayType":"table","title":"Sessions","data":[{"id":"session-1"}]}`)
	if err != nil {
		t.Fatalf("expected structured JSON to parse, got %v", err)
	}
	if result.DisplayType != DisplayTypeTable {
		t.Fatalf("expected displayType table, got %q", result.DisplayType)
	}
}

func TestParseStructuredResult_AcceptsStructuredYAML(t *testing.T) {
	result, err := ParseStructuredResult("displayType: list\ndata:\n  - item-1\n  - item-2\n")
	if err != nil {
		t.Fatalf("expected structured YAML to parse, got %v", err)
	}
	if result.DisplayType != DisplayTypeList {
		t.Fatalf("expected displayType list, got %q", result.DisplayType)
	}
}
