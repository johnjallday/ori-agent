package session

import (
	"reflect"
	"testing"
)

func TestParseWikilinks_Empty(t *testing.T) {
	if got := ParseWikilinks(""); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}
}

func TestParseWikilinks_Simple(t *testing.T) {
	src := "See [[Brand Kit]] for details."
	want := []Wikilink{{Target: "Brand Kit", Position: 4}}
	got := ParseWikilinks(src)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseWikilinks mismatch\n got=%v\nwant=%v", got, want)
	}
}

func TestParseWikilinks_PipeFormUsesDisplay(t *testing.T) {
	src := "See [[brand-kit-2026|the 2026 update]]."
	got := ParseWikilinks(src)
	if len(got) != 1 {
		t.Fatalf("expected 1 wikilink, got %d", len(got))
	}
	if got[0].Target != "brand-kit-2026" {
		t.Errorf("expected target 'brand-kit-2026', got %q", got[0].Target)
	}
	if got[0].Display != "the 2026 update" {
		t.Errorf("expected display 'the 2026 update', got %q", got[0].Display)
	}
}

func TestParseWikilinks_Multiple(t *testing.T) {
	src := "[[A]] and [[B]] both relevant."
	got := ParseWikilinks(src)
	if len(got) != 2 {
		t.Fatalf("expected 2 wikilinks, got %d: %v", len(got), got)
	}
	if got[0].Target != "A" || got[1].Target != "B" {
		t.Errorf("unexpected targets: %v", got)
	}
}

func TestParseWikilinks_ExcludesFencedCodeBlock(t *testing.T) {
	src := "[[Real]]\n```\n[[Inside fence]]\n[[Also fake]]\n```\n[[Other]]\n"
	got := ParseWikilinks(src)
	if len(got) != 2 {
		t.Fatalf("expected 2 (Real, Other), got %d: %v", len(got), got)
	}
	if got[0].Target != "Real" || got[1].Target != "Other" {
		t.Errorf("expected Real, Other; got %v", got)
	}
}

func TestParseWikilinks_TildeFenceAlsoExcluded(t *testing.T) {
	src := "[[Outer]]\n~~~\n[[Hidden]]\n~~~\n[[Last]]\n"
	got := ParseWikilinks(src)
	if len(got) != 2 || got[0].Target != "Outer" || got[1].Target != "Last" {
		t.Fatalf("expected Outer, Last; got %v", got)
	}
}

func TestParseWikilinks_RejectsEmptyTarget(t *testing.T) {
	src := "[[]] [[ ]] [[real]]"
	got := ParseWikilinks(src)
	if len(got) != 1 {
		t.Fatalf("expected 1 (the real one), got %d: %v", len(got), got)
	}
	if got[0].Target != "real" {
		t.Errorf("expected 'real', got %q", got[0].Target)
	}
}

func TestParseWikilinks_UnbalancedBracketsIgnored(t *testing.T) {
	src := "[[Open without close \nor [[]] empty"
	// "[[Open without close" never closes -> ignored. "[[]]" empty target -> ignored.
	got := ParseWikilinks(src)
	if len(got) != 0 {
		t.Fatalf("expected 0 wikilinks, got %v", got)
	}
}

func TestParseWikilinks_TrimsWhitespace(t *testing.T) {
	src := "[[  Padded  |  Display label  ]]"
	got := ParseWikilinks(src)
	if len(got) != 1 {
		t.Fatalf("expected 1 wikilink, got %v", got)
	}
	if got[0].Target != "Padded" {
		t.Errorf("expected target 'Padded', got %q", got[0].Target)
	}
	if got[0].Display != "Display label" {
		t.Errorf("expected display 'Display label', got %q", got[0].Display)
	}
}

func TestParseWikilinks_PositionPointsAtOpeningBracket(t *testing.T) {
	src := "preamble [[Heading here]] suffix"
	got := ParseWikilinks(src)
	if len(got) != 1 {
		t.Fatalf("expected 1 wikilink, got %v", got)
	}
	if src[got[0].Position] != '[' || src[got[0].Position+1] != '[' {
		t.Errorf("position %d does not point at [[: %q", got[0].Position, src[got[0].Position:got[0].Position+2])
	}
}

func TestParseWikilinks_HandlesMissingTrailingNewline(t *testing.T) {
	src := "no newline [[Target]]"
	got := ParseWikilinks(src)
	if len(got) != 1 || got[0].Target != "Target" {
		t.Fatalf("expected single wikilink Target, got %v", got)
	}
}

func TestParseWikilinks_UnicodeTargets(t *testing.T) {
	src := "Reference [[日本語ノート]] etc."
	got := ParseWikilinks(src)
	if len(got) != 1 || got[0].Target != "日本語ノート" {
		t.Fatalf("expected single Unicode target, got %v", got)
	}
}

func TestParseWikilinks_NestedBracketsRejected(t *testing.T) {
	// Inner brackets aren't allowed; the parser should bail out of the match.
	src := "[[outer [inner] outer]] [[good]]"
	got := ParseWikilinks(src)
	if len(got) != 1 || got[0].Target != "good" {
		t.Fatalf("expected only 'good', got %v", got)
	}
}
