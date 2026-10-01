package workspace

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type hiddenSnapshotCycle struct {
	Next *hiddenSnapshotCycle
}

func (*hiddenSnapshotCycle) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

func TestInMemoryStoreSnapshotPreservesTypedValues(t *testing.T) {
	type payload struct {
		Names []string
		At    time.Time
	}
	type payloadPointer *payload
	at := time.Date(2026, 10, 1, 12, 0, 0, 123, time.FixedZone("fixture", 3600))
	value := &payload{Names: []string{"original"}, At: at}
	seed := NewWorkspace(CreateWorkspaceParams{Name: "Typed Fixture"})
	seed.SharedData = map[string]any{
		"typed_map":     map[int][]int{1: {7}},
		"array":         [1]*payload{value},
		"number":        json.Number("9007199254740993"),
		"integer":       int64(9007199254740993),
		"empty":         []string{},
		"nil":           []string(nil),
		"pointer":       (*payload)(nil),
		"named_pointer": payloadPointer(value),
	}
	seed.MCPBindings = []MCPBinding{{
		ID: "binding", ServerName: "filesystem", Scope: map[string]any{"roots": []string{"/fixture"}},
		AllowedTools: []string{},
	}}
	seed.Tasks = []Task{{ID: "task", Context: map[string]any{"counts": []int{1}}}}
	store := NewInMemoryStore()
	if err := store.Save(seed); err != nil {
		t.Fatal(err)
	}
	// Retained input references must not mutate the store after Save.
	value.Names[0] = "input mutation"
	seed.SharedData["typed_map"].(map[int][]int)[1][0] = 99
	seed.MCPBindings[0].Scope["roots"].([]string)[0] = "/input-mutation"

	read, err := store.Get(seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := read.SharedData["typed_map"].(map[int][]int)[1][0]; got != 7 {
		t.Errorf("typed nested map changed: %d", got)
	}
	if got := read.SharedData["named_pointer"].(payloadPointer).Names[0]; got != "original" {
		t.Errorf("named pointer changed: %s", got)
	}
	array := read.SharedData["array"].([1]*payload)
	if array[0].Names[0] != "original" || array[0].At != at {
		t.Errorf("typed pointer or timestamp changed: %+v", array[0])
	}
	if got := read.SharedData["number"].(json.Number).String(); got != "9007199254740993" {
		t.Errorf("exact number changed: %s", got)
	}
	if got := read.SharedData["integer"].(int64); got != 9007199254740993 {
		t.Errorf("typed integer changed: %d", got)
	}
	if read.SharedData["empty"].([]string) == nil || read.SharedData["nil"].([]string) != nil || read.SharedData["pointer"].(*payload) != nil {
		t.Error("nil/empty distinction or typed nil pointer changed")
	}
	if read.MCPBindings[0].AllowedTools == nil || read.MCPBindings[0].Scope["roots"].([]string)[0] != "/fixture" {
		t.Error("scope type/value or explicit empty tool allowlist changed")
	}
	// Returned nested references must be isolated too.
	array[0].Names[0] = "read mutation"
	read.SharedData["typed_map"].(map[int][]int)[1][0] = 100
	read.Tasks[0].Context["counts"].([]int)[0] = 100
	fresh, err := store.Get(seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.SharedData["array"].([1]*payload)[0].Names[0] != "original" ||
		fresh.SharedData["typed_map"].(map[int][]int)[1][0] != 7 ||
		fresh.Tasks[0].Context["counts"].([]int)[0] != 1 {
		t.Error("nested references escaped a read snapshot")
	}
	if err := fresh.MutateTask("task", func(task *Task) error { task.FailureCount++; return nil }); err != nil {
		t.Fatalf("snapshot task index was not rebuilt: %v", err)
	}
}

func TestInMemoryStoreConcurrentSavesOfTheSameInput(t *testing.T) {
	store := NewInMemoryStore()
	seed := NewWorkspace(CreateWorkspaceParams{Name: "Concurrent Saves"})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := store.Save(seed); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	read, err := store.Get(seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if read.FolderSlug != "concurrent-saves" || seed.FolderSlug != read.FolderSlug {
		t.Error("concurrent saves did not retain the generated slug")
	}
}

func TestInMemoryStoreSnapshotKeepsReadProvenanceNotMutationIntent(t *testing.T) {
	seed := NewWorkspace(CreateWorkspaceParams{Name: "Read Provenance"})
	seed.MarkMissionLoaded()
	seed.capabilitiesExplicit = true
	store := NewInMemoryStore()
	if err := store.Save(seed); err != nil {
		t.Fatal(err)
	}
	read, err := store.Get(seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !read.MissionLoaded() {
		t.Error("snapshot lost the loaded mission envelope marker")
	}
	if read.capabilitiesExplicit {
		t.Error("snapshot retained another mutation cycle's write intent")
	}
}

func TestInMemoryStoreRejectsUnpersistableSnapshotWithoutChangingStoredData(t *testing.T) {
	for _, name := range []string{"function", "cycle", "hidden_marshaler_cycle"} {
		t.Run(name, func(t *testing.T) {
			store := NewInMemoryStore()
			seed := NewWorkspace(CreateWorkspaceParams{Name: "Original"})
			if err := store.Save(seed); err != nil {
				t.Fatal(err)
			}
			read, err := store.Get(seed.ID)
			if err != nil {
				t.Fatal(err)
			}
			read.Name = "Rejected"
			switch name {
			case "function":
				read.SharedData = map[string]any{"invalid": func() {}}
			case "cycle":
				cycle := map[string]any{}
				cycle["self"] = cycle
				read.SharedData = cycle
			case "hidden_marshaler_cycle":
				cycle := &hiddenSnapshotCycle{}
				cycle.Next = cycle
				read.SharedData = map[string]any{"invalid": cycle}
			}
			if err := store.Save(read); err == nil {
				t.Fatal("Save accepted data that cannot be serialized")
			}
			fresh, err := store.Get(seed.ID)
			if err != nil {
				t.Fatal(err)
			}
			if fresh.Name != "Original" || len(fresh.SharedData) != 0 {
				t.Error("failed snapshot changed the stored record")
			}
		})
	}
	if err := NewInMemoryStore().Save(nil); err == nil {
		t.Error("Save accepted a nil workspace")
	}
}
