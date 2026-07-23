package calendar

import "testing"

func TestValidateJSONPointer(t *testing.T) {
	cases := []struct {
		name    string
		ptr     string
		wantErr bool
	}{
		{"empty is valid", "", false},
		{"simple path", "/items/0/id", false},
		{"escaped tilde", "/a~0b", false},
		{"escaped slash", "/a~1b", false},
		{"missing leading slash", "items/0", true},
		{"bad escape", "/a~2b", true},
		{"trailing tilde", "/a~", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateJSONPointer(tc.ptr)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for %q", tc.ptr)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.ptr, err)
			}
		})
	}
}

func TestResolvePointer(t *testing.T) {
	doc := map[string]any{
		"id": "evt-1",
		"nested": map[string]any{
			"summary": "Standup",
		},
		"items": []any{
			map[string]any{"name": "first"},
			map[string]any{"name": "second"},
		},
		"weird/key": "slash",
		"weird~key": "tilde",
	}

	cases := []struct {
		name   string
		ptr    string
		want   any
		wantOK bool
	}{
		{"whole document", "", doc, true},
		{"top-level field", "/id", "evt-1", true},
		{"nested field", "/nested/summary", "Standup", true},
		{"array index", "/items/1/name", "second", true},
		{"missing field", "/nope", nil, false},
		{"out of range index", "/items/9/name", nil, false},
		{"non-numeric index", "/items/foo", nil, false},
		{"escaped slash key", "/weird~1key", "slash", true},
		{"escaped tilde key", "/weird~0key", "tilde", true},
		{"traverse into scalar", "/id/nope", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ResolvePointer(doc, tc.ptr)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && tc.name != "whole document" && got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSetPointer(t *testing.T) {
	doc := map[string]any{}
	if err := SetPointer(doc, "/calendarId", "primary"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc["calendarId"] != "primary" {
		t.Fatalf("unexpected doc: %+v", doc)
	}

	if err := SetPointer(doc, "/time/start", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	nested, ok := doc["time"].(map[string]any)
	if !ok || nested["start"] != "2026-01-01T00:00:00Z" {
		t.Fatalf("unexpected doc: %+v", doc)
	}

	if err := SetPointer(doc, "", "value"); err == nil {
		t.Fatal("expected error for empty pointer")
	}
	if err := SetPointer(doc, "/calendarId/nope", "x"); err == nil {
		t.Fatal("expected error traversing through a scalar")
	}
}
