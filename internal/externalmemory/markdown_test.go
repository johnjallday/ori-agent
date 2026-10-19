package externalmemory

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReadSections_TracksHeadingPathsAndIgnoresHeadingsInCode(t *testing.T) {
	doc := strings.Join([]string{
		"intro line",
		"# Group",
		"text a",
		"## Task 1: first",
		"```",
		"# not a heading",
		"```",
		"### keywords",
		"- k",
		"## Task 2: second",
		"#### deep",
	}, "\n")
	sections, err := readSections(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range sections {
		got = append(got, strings.Join(s.Path, " > "))
	}
	want := []string{"", "Group", "Group > Task 1: first", "Group > Task 1: first > keywords", "Group > Task 2: second", "Group > Task 2: second >  > deep"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %q, want %q", got, want)
	}
	if text := sectionText(sections, 2); !strings.Contains(text, "# not a heading") || !strings.Contains(text, "### keywords\n- k") {
		t.Fatalf("section text lost code or nested sections:\n%s", text)
	}
	if text := sectionText(sections, 4); strings.Contains(text, "keywords") {
		t.Fatalf("section text leaked a sibling section:\n%s", text)
	}
}

func TestSplitBlocks(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  []mdBlock
	}{
		{
			name:  "paragraphs split on blank lines",
			lines: []string{"first line", "still first", "", "second"},
			want:  []mdBlock{{blockParagraph, "first line\nstill first"}, {blockParagraph, "second"}},
		},
		{
			name:  "bullets keep nested items and continuation",
			lines: []string{"- top one", "  - desc: nested", "  - learnings: more", "", "- top two", "lazy continuation"},
			want: []mdBlock{
				{blockBullet, "top one\n  - desc: nested\n  - learnings: more"},
				{blockBullet, "top two\nlazy continuation"},
			},
		},
		{
			name:  "numbered items and checkboxes",
			lines: []string{"1. one", "2) two"},
			want:  []mdBlock{{blockNumbered, "one"}, {blockNumbered, "two"}},
		},
		{
			name:  "paragraph then list then paragraph",
			lines: []string{"lead in", "- item", "", "after the list"},
			want:  []mdBlock{{blockParagraph, "lead in"}, {blockBullet, "item"}, {blockParagraph, "after the list"}},
		},
		{
			name:  "indented text after a blank line stays in the item",
			lines: []string{"- item", "", "    more of the item", "- next"},
			want:  []mdBlock{{blockBullet, "item\n    more of the item"}, {blockBullet, "next"}},
		},
		{
			name:  "horizontal rule ends a block",
			lines: []string{"- a", "---", "b"},
			want:  []mdBlock{{blockBullet, "a"}, {blockParagraph, "b"}},
		},
		{
			name:  "fenced code with blank lines stays whole",
			lines: []string{"```go", "x := 1", "", "y := 2", "```", "", "- z"},
			want:  []mdBlock{{blockParagraph, "```go\nx := 1\n\ny := 2\n```"}, {blockBullet, "z"}},
		},
		{
			name:  "korean bullets",
			lines: []string{"- 1시간봉 종가 기준", "- 결과는 한국어로"},
			want:  []mdBlock{{blockBullet, "1시간봉 종가 기준"}, {blockBullet, "결과는 한국어로"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := splitBlocks(tc.lines); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("blocks =\n%#v\nwant\n%#v", got, tc.want)
			}
		})
	}
}

func TestEntryID_IsStableAndDistinct(t *testing.T) {
	a := EntryID(SourceCodex, "/home/.codex/memories/MEMORY.md", "group/task-1")
	if again := EntryID(SourceCodex, "/home/.codex/memories/MEMORY.md", "group/task-1"); again != a {
		t.Fatalf("id changed between calls: %s vs %s", a, again)
	}
	for _, other := range []string{
		EntryID(SourceClaudeCode, "/home/.codex/memories/MEMORY.md", "group/task-1"),
		EntryID(SourceCodex, "/home/.codex/memories/raw_memories.md", "group/task-1"),
		EntryID(SourceCodex, "/home/.codex/memories/MEMORY.md", "group/task-2"),
	} {
		if other == a {
			t.Fatalf("distinct inputs produced the same id %s", a)
		}
	}
	if len(a) != 32 {
		t.Fatalf("id length = %d, want 32 hex chars", len(a))
	}
}

func TestNewEntry_CapsBodyAtRuneBoundary(t *testing.T) {
	body := strings.Repeat("가", MaxBodyBytes) // 3 bytes per rune
	entry := newEntry(SourceCodex, "/f", "k", Entry{Title: "t", Body: body})
	if !entry.Truncated {
		t.Fatal("long body not flagged truncated")
	}
	if len(entry.Body) > MaxBodyBytes {
		t.Fatalf("body is %d bytes, over the cap", len(entry.Body))
	}
	if !utf8.ValidString(entry.Body) || !strings.HasSuffix(entry.Body, truncationMarker) {
		t.Fatal("truncated body is not valid UTF-8 ending in the marker")
	}
	short := newEntry(SourceCodex, "/f", "k", Entry{Title: "t", Body: "short"})
	if short.Truncated || short.Body != "short" {
		t.Fatalf("short body changed: %+v", short)
	}
}

func TestNewEntry_ContentHashFollowsVisibleFields(t *testing.T) {
	base := newEntry(SourceCodex, "/f", "k", Entry{Title: "t", Body: "b", Folder: "/p", Category: "c", Date: "2026-01-02"})
	same := newEntry(SourceCodex, "/f", "k", Entry{Title: "t", Body: "b", Folder: "/p", Category: "c", Date: "2026-01-02"})
	if base.ContentHash != same.ContentHash {
		t.Fatal("identical entries hash differently")
	}
	for _, changed := range []Entry{
		{Title: "t2", Body: "b", Folder: "/p", Category: "c", Date: "2026-01-02"},
		{Title: "t", Body: "b2", Folder: "/p", Category: "c", Date: "2026-01-02"},
		{Title: "t", Body: "b", Folder: "/q", Category: "c", Date: "2026-01-02"},
		{Title: "t", Body: "b", Folder: "/p", Category: "c", Date: "2026-01-03"},
	} {
		if newEntry(SourceCodex, "/f", "k", changed).ContentHash == base.ContentHash {
			t.Fatalf("change %+v did not change the hash", changed)
		}
	}
}

func TestKeySlugAndTitle(t *testing.T) {
	if got := Slug("User Preferences — Korean: 1시간봉!"); got != "user-preferences-korean-1시간봉" {
		t.Fatalf("Slug = %q", got)
	}
	long := strings.Repeat("word ", 40)
	a, b := keySlug(long+"a"), keySlug(long+"b")
	if a == b || utf8.RuneCountInString(a) > maxKeySlugRunes+7 {
		t.Fatalf("long key slugs not bounded and distinct: %q %q", a, b)
	}
	if keySlug("!!!") != "item" {
		t.Fatal("empty slug should fall back to item")
	}
	title := titleFrom("- " + long)
	if utf8.RuneCountInString(title) > maxTitleRunes+1 || !strings.HasSuffix(title, "…") || strings.HasPrefix(title, "-") {
		t.Fatalf("title not cut cleanly: %q", title)
	}
	if got := titleFrom("- [x] done item\nsecond line"); got != "done item" {
		t.Fatalf("titleFrom = %q", got)
	}
}

func TestNormalizeDate(t *testing.T) {
	cases := map[string]string{
		"2026-10-05T08:57:42+00:00": "2026-10-05",
		"2026-10-18T23:38:45.732Z":  "2026-10-18",
		"2026-07-16":                "2026-07-16",
		"20260716T235137Z":          "2026-07-16",
		`"2026-01-02"`:              "2026-01-02",
		"yesterday":                 "",
		"":                          "",
	}
	for raw, want := range cases {
		if got := normalizeDate(raw); got != want {
			t.Errorf("normalizeDate(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestSectionKeys_SuffixRepeats(t *testing.T) {
	keys := sectionKeys{}
	got := []string{keys.next("a"), keys.next("b"), keys.next("a"), keys.next("a")}
	if want := []string{"a", "b", "a~2", "a~3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
}
