package foldermap

import (
	"fmt"
	"testing"
)

func TestStageFor_EachStage(t *testing.T) {
	var dump []string
	kinds := []string{"pdf", "jpg", "zip", "docx", "dmg"}
	for i := range DumpMinLooseFiles {
		dump = append(dump, fmt.Sprintf("f%02d.%s", i, kinds[i%len(kinds)]))
	}
	cases := []struct {
		name  string
		paths []string
		want  Stage
	}{
		{"demo folder is a Home portfolio", nil, StageHome},
		{"three drafts are a group", []string{"Drafts/A/outline.md", "Drafts/B/outline.md", "Drafts/C/main.tex"}, StageGroup},
		{"four songs are a group, not a Home", []string{"a/Song.rpp", "b/Song.rpp", "c/Song.rpp", "d/Song.rpp"}, StageGroup},
		{"a project at the fed folder is the Tools stage", []string{"go.mod", "cmd/main.go"}, StageTools},
		{"a dump is the Tools stage", dump, StageTools},
		{"nothing recognized stays a folder", []string{"notes.txt", "Photos/a.jpg"}, StageFolder},
		{"an empty folder stays a folder", []string{"Empty/"}, StageFolder},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := ""
			if tc.paths == nil {
				root = demoLayout(t)
			} else {
				root = build(t, tc.paths...)
			}
			_, facts := walk(t, root)
			if got := StageFor(facts); got != tc.want {
				t.Fatalf("stage = %q, want %q (facts %+v)", got, tc.want, facts)
			}
		})
	}
}

func TestFacts_ShapesTie(t *testing.T) {
	_, tie := walk(t, build(t, "a/Song.rpp", "b/go.mod"))
	if !tie.ShapesTie() {
		t.Fatal("one song and one repository tie")
	}
	_, clear := walk(t, build(t, "a/Song.rpp", "b/Song.rpp", "c/go.mod"))
	if clear.ShapesTie() {
		t.Fatal("two songs and one repository do not tie")
	}
}

func TestFactsLine_EmptyRoot(t *testing.T) {
	_, facts := walk(t, t.TempDir())
	if got := FactsLine(facts); got != "This folder is empty" {
		t.Fatalf("facts line = %q", got)
	}
}

func TestFactsLine_DemoFolder(t *testing.T) {
	_, facts := walk(t, demoLayout(t))
	want := "7 REAPER songs · 1 Logic Pro song · 2 folders with songs · 1 empty folder"
	if got := FactsLine(facts); got != want {
		t.Fatalf("facts line = %q, want %q", got, want)
	}
	// Same map, same line.
	_, again := walk(t, demoLayout(t))
	if FactsLine(again) != want {
		t.Fatal("the facts line must be stable across runs")
	}
}

func TestFactsLine_OtherShapes(t *testing.T) {
	cases := map[string][]string{
		"1 software project":                                   {"go.mod"},
		"3 manuscripts · 1 folder with manuscripts":            {"Drafts/A/outline.md", "Drafts/B/outline.md", "Drafts/C/main.tex"},
		"1 empty folder":                                       {"Empty/"},
		"Nothing Ori recognizes yet · 2 folders · 1 file here": {"notes.txt", "Photos/a.jpg"},
	}
	for want, paths := range cases {
		_, facts := walk(t, build(t, paths...))
		if got := FactsLine(facts); got != want {
			t.Errorf("facts line = %q, want %q", got, want)
		}
	}
}
