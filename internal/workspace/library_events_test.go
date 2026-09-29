package workspace

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The scan-completed payload is path-free by contract. A new field must be
// added here deliberately, never a path, folder, file or project name.
func TestLibraryScanCompleted_PayloadHasOnlyIDsAndCounts(t *testing.T) {
	allowed := []string{"activatable", "coverage", "home_id", "new", "projects", "root_id",
		"scan_id", "unavailable", "unsupported_format", "updated"}
	payload := LibraryScanCompleted{HomeID: "home", ScanID: "scan", RootID: "root", Coverage: "complete",
		Projects: 6, New: 6, Activatable: 5, UnsupportedFormat: 1}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
		for _, forbidden := range []string{"path", "folder", "file", "name", "title"} {
			if strings.Contains(key, forbidden) {
				t.Fatalf("payload field %q could carry a path or name", key)
			}
		}
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, allowed) {
		t.Fatalf("payload fields changed: %v", keys)
	}
	event := payload.Event("project_library")
	eventKeys := make([]string, 0, len(event.Data))
	for key := range event.Data {
		eventKeys = append(eventKeys, key)
	}
	sort.Strings(eventKeys)
	if !reflect.DeepEqual(eventKeys, allowed) || event.Type != EventLibraryScanCompleted ||
		event.WorkspaceID != "home" {
		t.Fatalf("bus event differs from the payload: %+v", event)
	}
	decoded, ok := LibraryScanCompletedFromEvent(event)
	if !ok || decoded != payload {
		t.Fatalf("round trip: %+v %v", decoded, ok)
	}
}

func TestLibraryScanCompleted_RejectsMalformedEvents(t *testing.T) {
	good := LibraryScanCompleted{HomeID: "home", ScanID: "scan", RootID: "root", Coverage: "partial"}.Event("x")
	cases := map[string]func(*Event){
		"wrong type":     func(e *Event) { e.Type = EventWorkspaceUpdated },
		"other home":     func(e *Event) { e.WorkspaceID = "other" },
		"failed status":  func(e *Event) { e.Data["coverage"] = "failed" },
		"negative count": func(e *Event) { e.Data["new"] = -1 },
		"string count":   func(e *Event) { e.Data["projects"] = "6" },
		"missing scan":   func(e *Event) { delete(e.Data, "scan_id") },
		"nil data":       func(e *Event) { e.Data = nil },
	}
	for name, mutate := range cases {
		event := good
		event.Data = make(map[string]any, len(good.Data))
		for key, value := range good.Data {
			event.Data[key] = value
		}
		mutate(&event)
		if _, ok := LibraryScanCompletedFromEvent(event); ok {
			t.Fatalf("%s: malformed event accepted", name)
		}
	}
}
