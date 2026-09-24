package folderdigest

import "testing"

func TestMatchMarker_TableRows(t *testing.T) {
	cases := []struct {
		name  string
		isDir bool
		shape Shape
		ok    bool
	}{
		{".git", true, ShapeCode, true},
		{".git", false, "", false}, // a .git file (worktree pointer) is not the marker
		{"package.json", false, ShapeCode, true},
		{"package.json", true, "", false},
		{"go.mod", false, ShapeCode, true},
		{"Cargo.toml", false, ShapeCode, true},
		{"pyproject.toml", false, ShapeCode, true},
		{"requirements.txt", false, ShapeCode, true},
		{"App.xcodeproj", true, ShapeCode, true},
		{"Song.rpp", false, ShapeAudio, true},
		{"Song.logicx", true, ShapeAudio, true},
		{"Set.als", false, ShapeAudio, true},
		{"main.tex", false, ShapeManuscript, true},
		{"chapters", true, ShapeManuscript, true},
		{"chapters", false, "", false},
		{"outline.md", false, ShapeManuscript, true},
		{"refs.bib", false, ShapeCorpus, true},
		{".obsidian", true, ShapeNotes, true},
		{"notes.md", false, "", false},
		{"README.md", false, "", false},
	}
	for _, tc := range cases {
		m, ok := MatchMarker(tc.name, tc.isDir)
		if ok != tc.ok {
			t.Errorf("MatchMarker(%q, dir=%v) ok = %v, want %v", tc.name, tc.isDir, ok, tc.ok)
			continue
		}
		if ok && m.Shape != tc.shape {
			t.Errorf("MatchMarker(%q) shape = %s, want %s", tc.name, m.Shape, tc.shape)
		}
	}
}

func TestMarkerPrecedence_SpecificShapesBeatCode(t *testing.T) {
	git, _ := MatchMarker(".git", true)
	tex, _ := MatchMarker("main.tex", false)
	bib, _ := MatchMarker("refs.bib", false)
	vault, _ := MatchMarker(".obsidian", true)
	if markerRank(tex) >= markerRank(git) {
		t.Errorf("a thesis under git is a manuscript, not code")
	}
	if markerRank(tex) >= markerRank(bib) {
		t.Errorf("a manuscript with a bibliography is a manuscript, not a corpus")
	}
	if markerRank(vault) >= markerRank(git) {
		t.Errorf("a vault under git is a notes vault, not code")
	}
}

func TestToolTable_Lookups(t *testing.T) {
	if tool, ok := ToolForExtension("rpp"); !ok || tool.ToolID != "reaper" || tool.ToolName != "REAPER" {
		t.Errorf("ToolForExtension(rpp) = %+v, %v", tool, ok)
	}
	if tool, ok := ToolForExtension(".TEX"); !ok || tool.ToolID != "latex" {
		t.Errorf("ToolForExtension(.TEX) = %+v, %v", tool, ok)
	}
	if _, ok := ToolForExtension(".pdf"); ok {
		t.Errorf("PDF names no tool")
	}
	goMod, _ := MatchMarker("go.mod", false)
	if tool, ok := ToolForMarker(goMod); !ok || tool.ToolID != "go" {
		t.Errorf("ToolForMarker(go.mod) = %+v, %v", tool, ok)
	}
	rpp, _ := MatchMarker("Song.rpp", false)
	if tool, ok := ToolForMarker(rpp); !ok || tool.ToolID != "reaper" {
		t.Errorf("ToolForMarker(*.rpp) = %+v, %v", tool, ok)
	}
	git, _ := MatchMarker(".git", true)
	if _, ok := ToolForMarker(git); ok {
		t.Errorf(".git names no tool")
	}
	if tool, ok := ToolByID("reference-manager"); !ok || tool.HypothesisText != "A reference manager may be one of the tools you use." {
		t.Errorf("ToolByID = %+v, %v", tool, ok)
	}
	for _, tool := range Tools {
		if tool.HypothesisText == "" || tool.ToolID == "" || tool.ToolName == "" {
			t.Errorf("incomplete tool row %+v", tool)
		}
	}
}
