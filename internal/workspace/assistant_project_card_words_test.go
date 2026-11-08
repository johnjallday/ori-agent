package workspace

import (
	"encoding/json"
	"strings"
	"testing"
)

func musicWordsSnapshot() *ProjectCardWords {
	return &ProjectCardWords{PluginID: "music-project-management", Version: "0.5.0", Noun: "Song", NounPlural: "Songs",
		RosterTitle: "Songs and assistants",
		Facts:       []ProjectCardWordsFact{{Kind: "track_count", Unit: "tracks", UnitOne: "track"}, {Kind: "tempo", Unit: "BPM"}},
		Fields:      []ProjectCardWordsField{{ID: "key", Label: "Key", Kind: "text"}}}
}

func TestProjectCardWords_LenientDecodeAndValidity(t *testing.T) {
	var state AssistantProgramState
	if err := json.Unmarshal([]byte(`{"schema_version":1,"project_card_words":{"noun":7,"facts":"x"}}`), &state); err != nil {
		t.Fatalf("an unreadable snapshot failed the Home's load: %v", err)
	}
	if state.ProjectCardWords == nil || state.ProjectCardWords.Valid() {
		t.Fatalf("an unreadable snapshot must read as present but invalid: %+v", state.ProjectCardWords)
	}
	encoded, _ := json.Marshal(AssistantProgramState{ProjectCardWords: musicWordsSnapshot()})
	var round AssistantProgramState
	if err := json.Unmarshal(encoded, &round); err != nil || !round.ProjectCardWords.Valid() || round.ProjectCardWords.Noun != "Song" {
		t.Fatalf("round trip: %+v %v", round.ProjectCardWords, err)
	}
	for name, bad := range map[string]func(*ProjectCardWords){
		"no noun":      func(w *ProjectCardWords) { w.Noun = "" },
		"long title":   func(w *ProjectCardWords) { w.RosterTitle = strings.Repeat("a", 61) },
		"unknown fact": func(w *ProjectCardWords) { w.Facts[0].Kind = "key" },
		"twin facts":   func(w *ProjectCardWords) { w.Facts[1].Kind = "track_count" },
		"bad field id": func(w *ProjectCardWords) { w.Fields[0].ID = "Key" },
		"url label":    func(w *ProjectCardWords) { w.Fields[0].Label = "https://x" },
	} {
		words := musicWordsSnapshot()
		bad(words)
		if words.Valid() {
			t.Errorf("%s passed", name)
		}
	}
	clone := musicWordsSnapshot().Clone()
	clone.Fields[0].Label = "changed"
	if musicWordsSnapshot().Fields[0].Label != "Key" {
		t.Fatal("clone shares fields")
	}
	if CloneAssistantProgramState(&AssistantProgramState{ProjectCardWords: musicWordsSnapshot()}).ProjectCardWords.Noun != "Song" {
		t.Fatal("the state clone dropped the snapshot")
	}
}

func TestProjectCardWords_PromptLinesFallBackToNeutral(t *testing.T) {
	lines := strings.Join(RenderProjectCardWordsPromptLines(musicWordsSnapshot()), "\n")
	if !strings.Contains(lines, `called a "song" (plural "songs")`) || !strings.Contains(lines, `key "Key" (text)`) {
		t.Fatalf("package lines: %s", lines)
	}
	for _, words := range []*ProjectCardWords{nil, {}} {
		neutral := strings.Join(RenderProjectCardWordsPromptLines(words), "\n")
		if !strings.Contains(neutral, `called a "project" (plural "projects")`) || strings.Contains(neutral, "Plugin fields") {
			t.Fatalf("neutral lines: %s", neutral)
		}
	}
}
