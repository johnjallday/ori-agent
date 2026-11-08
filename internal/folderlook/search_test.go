package folderlook

import (
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/foldermap"
)

func TestSearchWords_AreTheTablesWordsNeverFolderOrFileNames(t *testing.T) {
	facts := foldermap.Facts{
		Members: []foldermap.Project{
			{Path: "Albums/Secret Client Mix", Name: "Secret Client Mix", Shape: folderdigest.ShapeAudio, Format: "reaper", Marker: "*.rpp"},
			{Path: "Albums/Logic Sketch", Name: "Logic Sketch", Shape: folderdigest.ShapeAudio, Format: "logic", Marker: "*.logicx"},
			{Path: "Drafts/My Novel", Name: "My Novel", Shape: folderdigest.ShapeManuscript, Marker: "outline.md"},
		},
		Shapes: []foldermap.ShapeCount{
			{Shape: folderdigest.ShapeAudio, Format: "reaper", Count: 1},
			{Shape: folderdigest.ShapeAudio, Format: "logic", Count: 1},
			{Shape: folderdigest.ShapeManuscript, Count: 1},
		},
	}
	words := SearchWords(facts)
	want := []string{"REAPER", "Logic Pro", "audio", "manuscript"}
	if !reflect.DeepEqual(words, want) {
		t.Fatalf("words = %v, want %v", words, want)
	}
	for _, w := range words {
		for _, p := range facts.Members {
			if strings.Contains(strings.ToLower(p.Path), strings.ToLower(w)) && w != "audio" {
				t.Fatalf("search word %q comes from a folder name", w)
			}
		}
	}
	if got := SearchWords(foldermap.Facts{Dump: true}); len(got) != 0 {
		t.Fatalf("nothing recognized searches nothing: %v", got)
	}
}

func TestSearchCandidates_DedupeInstalledDeclinedAndCap(t *testing.T) {
	existing := []Candidate{{ID: "integration:ori_reaper", Required: true}}
	hits := []SearchHit{
		{Kind: KindSkill, ID: "Mixing-Notes", Name: "Mixing notes", Description: "Keeps mix notes per song.", Marketplace: "Ori Skills"},
		{Kind: KindSkill, ID: "mixing-notes", Name: "Mixing notes (dup)", Marketplace: "Ori Skills"},
		{Kind: KindMCP, ID: "reaper-mcp", Name: "REAPER MCP", Marketplace: "MCP Registry"},
		{Kind: KindPlugin, ID: "release-planner", Name: "Release planner", Marketplace: "Community"},
		{Kind: KindSkill, ID: "declined-one", Name: "Declined", Marketplace: "Ori Skills"},
		{Kind: "theme", ID: "x", Name: "Unknown kind"},
		{Kind: KindSkill, ID: "", Name: "No id"},
	}
	installed := func(kind, id string) bool { return kind == KindMCP && id == "reaper-mcp" }
	declined := func(id string) bool { return id == "skill:declined-one" }
	got := SearchCandidates(hits, existing, installed, declined)
	var ids []string
	for _, c := range got {
		ids = append(ids, c.ID+"/"+c.Action)
	}
	if strings.Join(ids, ",") != "plugin:release-planner/add,skill:mixing-notes/add,mcp:reaper-mcp/already_here" {
		t.Fatalf("candidates = %v", ids)
	}
	if got[1].Ref != "Mixing-Notes" {
		t.Fatalf("the install needs the marketplace's own id, case kept: %q", got[1].Ref)
	}
	if got[1].Source != SourceSearch || got[1].Marketplace != "Ori Skills" || got[1].Detail != "Keeps mix notes per song." {
		t.Fatalf("search candidate = %+v", got[1])
	}
	var many []SearchHit
	for i := range MaxSearchCandidates + 5 {
		many = append(many, SearchHit{Kind: KindSkill, ID: string(rune('a' + i)), Name: string(rune('a' + i))})
	}
	if n := len(SearchCandidates(many, nil, nil, nil)); n != MaxSearchCandidates {
		t.Fatalf("cap = %d", n)
	}
}
