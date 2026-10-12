package foldercontext

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validObservation() Observation {
	return Observation{Version: 1, ID: "id", Folder: "Fixture", ScannedAt: time.Now(), Entries: 3, Files: 1,
		Kinds: []Kind{{Name: ".txt", Count: 1}}, Projects: []Project{{ID: "root", Name: "Fixture", Files: 1}},
		Coverage: Coverage{MaxDepth: 3, MaxEntries: 5000, BudgetSeconds: 3}}
}

func TestObservationBoundsAndNames(t *testing.T) {
	observation := validObservation()
	if err := observation.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Observation){
		func(o *Observation) { o.Version = 2 }, func(o *Observation) { o.Files = -1 },
		func(o *Observation) { o.Entries = 5001 }, func(o *Observation) { o.Folder = "/private/path" },
		func(o *Observation) { o.Folder = strings.Repeat("界", MaxNameRunes+1) },
		func(o *Observation) { o.Kinds = make([]Kind, MaxNames+1) },
		func(o *Observation) { o.Projects = make([]Project, MaxNames+1) },
		func(o *Observation) { o.Coverage.PartialReason = "<instructions>" },
	} {
		copy := validObservation()
		mutate(&copy)
		if copy.Validate() == nil {
			t.Fatalf("invalid observation accepted: %+v", copy)
		}
	}
	name := DisplayName("\x00../folder\\" + strings.Repeat("界", 200))
	if strings.ContainsAny(name, "/\\\x00") || len([]rune(name)) != MaxNameRunes {
		t.Fatalf("unbounded name %q", name)
	}
	data, err := json.Marshal(observation)
	if err != nil || len(data) > MaxBytes {
		t.Fatal("snapshot exceeds budget")
	}
}

// A project may say when it was last changed: a calendar day, and nothing a
// caller could smuggle a name, a path or a time of day through.
func TestObservationProjectLastChangedIsADayOrAbsent(t *testing.T) {
	observation := validObservation()
	observation.Projects[0].LastChanged = "2026-10-09"
	if err := observation.Validate(); err != nil {
		t.Fatalf("a day was refused: %v", err)
	}
	data, err := json.Marshal(observation)
	if err != nil || !strings.Contains(string(data), `"last_changed":"2026-10-09"`) {
		t.Fatalf("encoded = %s %v", data, err)
	}
	for _, value := range []string{"2026-10-09T16:30:00Z", "yesterday", "2026-13-40", "../notes.txt", "2026-10-09 "} {
		invalid := validObservation()
		invalid.Projects[0].LastChanged = value
		if invalid.Validate() == nil {
			t.Fatalf("last_changed %q was accepted", value)
		}
	}
	// An observation saved before the field existed still reads back valid.
	if data, _ = json.Marshal(validObservation()); strings.Contains(string(data), "last_changed") {
		t.Fatalf("an absent date was written: %s", data)
	}
}
