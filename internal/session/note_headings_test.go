package session

import (
	"reflect"
	"testing"
)

func TestParseHeadings_Empty(t *testing.T) {
	if got := ParseHeadings(""); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}
}

func TestParseHeadings_SimpleATX(t *testing.T) {
	src := "# Title\n\n## Subhead\nbody\n### Detail\n"
	want := []Heading{
		{Level: 1, Text: "Title", Position: 0},
		{Level: 2, Text: "Subhead", Position: 9},
		{Level: 3, Text: "Detail", Position: 25},
	}
	got := ParseHeadings(src)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseHeadings mismatch\n got=%v\nwant=%v", got, want)
	}
}

func TestParseHeadings_AllSixLevels(t *testing.T) {
	src := "# 1\n## 2\n### 3\n#### 4\n##### 5\n###### 6\n####### 7 (not a heading)\n"
	got := ParseHeadings(src)
	if len(got) != 6 {
		t.Fatalf("expected 6 headings, got %d: %v", len(got), got)
	}
	for i, h := range got {
		if h.Level != i+1 {
			t.Errorf("heading %d: expected level %d, got %d", i, i+1, h.Level)
		}
	}
}

func TestParseHeadings_ExcludesFencedCodeBlocks(t *testing.T) {
	src := "# Real\n```\n# Not a heading\n## Also not\n```\n## Real two\n"
	got := ParseHeadings(src)
	if len(got) != 2 {
		t.Fatalf("expected 2 headings, got %d: %v", len(got), got)
	}
	if got[0].Text != "Real" || got[1].Text != "Real two" {
		t.Errorf("expected Real, Real two; got %v", got)
	}
}

func TestParseHeadings_ExcludesTildeFencedBlock(t *testing.T) {
	src := "~~~~\n# Inside tilde fence\n~~~~\n# Outside\n"
	got := ParseHeadings(src)
	if len(got) != 1 {
		t.Fatalf("expected 1 heading, got %d: %v", len(got), got)
	}
	if got[0].Text != "Outside" {
		t.Errorf("expected Outside, got %v", got)
	}
}

func TestParseHeadings_BackticksDoNotCloseTildeFence(t *testing.T) {
	src := "~~~\n# in tildes\n```\n# still in tildes — backticks don't close ~~~\n~~~\n# out\n"
	got := ParseHeadings(src)
	if len(got) != 1 || got[0].Text != "out" {
		t.Fatalf("expected single heading 'out', got %v", got)
	}
}

func TestParseHeadings_RequiresWhitespaceAfterHashes(t *testing.T) {
	src := "#NoSpace\n# Yes space\n#\n"
	got := ParseHeadings(src)
	if len(got) != 1 || got[0].Text != "Yes space" {
		t.Fatalf("expected only 'Yes space', got %v", got)
	}
}

func TestParseHeadings_TrimsTrailingWhitespace(t *testing.T) {
	src := "#   Padded title   \n"
	got := ParseHeadings(src)
	if len(got) != 1 {
		t.Fatalf("expected 1 heading, got %v", got)
	}
	if got[0].Text != "Padded title" {
		t.Errorf("expected 'Padded title', got %q", got[0].Text)
	}
}

func TestParseHeadings_PositionPointsAtFirstHash(t *testing.T) {
	src := "preamble\n## Heading here\nmore\n"
	got := ParseHeadings(src)
	if len(got) != 1 {
		t.Fatalf("expected 1 heading, got %v", got)
	}
	if src[got[0].Position] != '#' {
		t.Errorf("position %d does not point at #: %q", got[0].Position, src[got[0].Position])
	}
}

func TestParseHeadings_HandlesMissingTrailingNewline(t *testing.T) {
	src := "# Last line no newline"
	got := ParseHeadings(src)
	if len(got) != 1 || got[0].Text != "Last line no newline" {
		t.Fatalf("expected single heading, got %v", got)
	}
}

func TestParseHeadings_BlankInput(t *testing.T) {
	if got := ParseHeadings("\n\n\n"); got != nil {
		t.Fatalf("expected nil for blank input, got %v", got)
	}
}

func TestParseHeadings_IndentedFenceStillExcludes(t *testing.T) {
	// Up to 3 spaces of indent are still a valid fence per CommonMark.
	src := "   ```\n# inside indented fence\n   ```\n# outside\n"
	got := ParseHeadings(src)
	if len(got) != 1 || got[0].Text != "outside" {
		t.Fatalf("expected only 'outside', got %v", got)
	}
}
