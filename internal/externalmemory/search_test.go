package externalmemory

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func searchEntries() []Entry {
	mk := func(source, key, title, body, folder, category, date string) Entry {
		return newEntry(source, "/fixture/"+source, key, Entry{Title: title, Body: body, Folder: folder, Category: category, Date: date})
	}
	removed := mk(SourceCodex, "removed", "Fee sensitivity removed", "fee sensitivity that is gone", "", "task", "2026-10-18")
	removed.State = StateRemoved
	return []Entry{
		mk(SourceCodex, "phrase", "EMA exit study", "Results showed strong fee sensitivity across exits.", "/Users/me/Projects/chart-lab", "task", "2026-10-01"),
		mk(SourceCodex, "terms-new", "Fees and sensitivity notes", "Fees matter. Sensitivity to slippage too.", "/Users/me/Projects/chart-lab/sub", "reusable-knowledge", "2026-10-10"),
		mk(SourceClaudeCode, "terms-old", "Older fee note", "sensitivity of fee schedule", "/Users/me/Projects/notes", "project", "2026-09-01"),
		mk(SourceClaudeCode, "one-term", "Only fees", "Fee schedule lives in docs.", "", "reference", "2026-10-15"),
		mk(SourceCodex, "korean", "한국어 결과 보고", "1시간봉 종가 기준 결과는 한국어로 작성한다.", "", "user-preferences", "2026-10-12"),
		mk(SourceGeminiCLI, "undated", "Answer in the user's language", "Reply in Korean when asked in Korean.", "", "instruction", ""),
		removed,
	}
}

func titles(result SearchResult) []string {
	var out []string
	for _, hit := range result.Hits {
		out = append(out, hit.Entry.Title)
	}
	return out
}

func TestSearch_RanksPhraseThenTermsThenRecency(t *testing.T) {
	result := Search(searchEntries(), Query{Text: "fee sensitivity", Limit: 25}, "")
	got := titles(result)
	if got[0] != "EMA exit study" || !result.Hits[0].Phrase {
		t.Fatalf("phrase match should rank first: %v", got)
	}
	if got[1] != "Fees and sensitivity notes" || got[2] != "Older fee note" {
		t.Fatalf("two-term matches should follow, newest first: %v", got)
	}
	if got[3] != "Only fees" || result.Hits[3].Terms != 1 {
		t.Fatalf("one-term match last: %v", got)
	}
	if result.Total != 4 {
		t.Fatalf("total = %d, removed entries must never match", result.Total)
	}
}

func TestSearch_QuotedPhraseAndTerms(t *testing.T) {
	q := parseQuery(`"Fee  Sensitivity" exits`)
	if !reflect.DeepEqual(q.phrases, []string{"fee sensitivity"}) || !reflect.DeepEqual(q.terms, []string{"fee", "sensitivity", "exits"}) {
		t.Fatalf("parsed = %+v", q)
	}
	single := parseQuery("a fees")
	if !reflect.DeepEqual(single.terms, []string{"fees"}) {
		t.Fatalf("one-letter Latin terms should drop: %+v", single)
	}
	if got := parseQuery(`unclosed "quote`); !reflect.DeepEqual(got.terms, []string{"unclosed", "quote"}) {
		t.Fatalf("unclosed quote: %+v", got)
	}
}

func TestSearch_KoreanQueries(t *testing.T) {
	entries := searchEntries()
	// A stem matches the word with its particle attached (결과 → 결과는).
	if got := titles(Search(entries, Query{Text: "결과"}, "")); len(got) != 1 || got[0] != "한국어 결과 보고" {
		t.Fatalf("Korean stem: %v", got)
	}
	// A mixed query of Korean and digits.
	if got := titles(Search(entries, Query{Text: "1시간봉 종가"}, "")); len(got) != 1 {
		t.Fatalf("Korean phrase: %v", got)
	}
	hit := Search(entries, Query{Text: "1시간봉 종가"}, "").Hits[0]
	if !hit.Phrase || !strings.Contains(hit.Excerpt, "1시간봉 종가") {
		t.Fatalf("Korean phrase hit: %+v", hit)
	}
	// A single Hangul syllable is a word, unlike a single Latin letter.
	if got := Search(entries, Query{Text: "봉"}, ""); got.Total != 1 {
		t.Fatalf("single syllable: %d hits", got.Total)
	}
	// Case folding works across scripts and the English mention matches too.
	if got := Search(entries, Query{Text: "KOREAN"}, ""); got.Total != 1 {
		t.Fatalf("case folding: %d hits", got.Total)
	}
}

func TestSearch_Filters(t *testing.T) {
	entries := searchEntries()
	cases := []struct {
		name  string
		query Query
		want  int
	}{
		{"source", Query{Text: "fee", Source: SourceClaudeCode}, 2},
		{"category", Query{Text: "fee", Category: "task"}, 1},
		{"folder prefix includes subfolders", Query{Text: "fee", Folder: "~/Projects/chart-lab"}, 2},
		{"folder prefix is boundary aware", Query{Text: "fee", Folder: "/Users/me/Projects/chart"}, 0},
		{"since drops older and undated", Query{Since: "2026-10-10"}, 3},
		{"empty query lists everything active", Query{Limit: 25}, 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Search(entries, tc.query, "/Users/me"); got.Total != tc.want {
				t.Fatalf("total = %d, want %d (%v)", got.Total, tc.want, titles(got))
			}
		})
	}
}

func TestSearch_FacetsIgnoreSourceAndCategoryFilters(t *testing.T) {
	result := Search(searchEntries(), Query{Text: "fee", Source: SourceCodex}, "")
	if result.Facets.Sources[SourceCodex] != 2 || result.Facets.Sources[SourceClaudeCode] != 2 {
		t.Fatalf("source facets = %v", result.Facets.Sources)
	}
	if result.Total != 2 {
		t.Fatalf("filtered total = %d", result.Total)
	}
}

func TestSearch_PagingAndLimits(t *testing.T) {
	entries := searchEntries()
	all := Search(entries, Query{Limit: 25}, "")
	page := Search(entries, Query{Limit: 2, Offset: 2}, "")
	if !reflect.DeepEqual(titles(page), titles(all)[2:4]) || page.Total != all.Total {
		t.Fatalf("page = %v, all = %v", titles(page), titles(all))
	}
	if got := Search(entries, Query{}, ""); len(got.Hits) != min(DefaultRecallLimit, all.Total) {
		t.Fatalf("default limit gave %d hits", len(got.Hits))
	}
	if got := Search(entries, Query{Offset: 99}, ""); len(got.Hits) != 0 || got.Total != all.Total {
		t.Fatalf("offset past the end: %+v", got)
	}
	// Newest first when listing; undated last.
	if titles(all)[0] != "Only fees" || titles(all)[len(all.Hits)-1] != "Answer in the user's language" {
		t.Fatalf("listing order = %v", titles(all))
	}
}

func TestSearch_ExcerptIsBoundedAndCentredOnTheMatch(t *testing.T) {
	body := strings.Repeat("filler words here ", 120) + "the needle sits here " + strings.Repeat("more filler text ", 120)
	entries := []Entry{newEntry(SourceCodex, "/f", "k", Entry{Title: "long", Body: body})}
	hit := Search(entries, Query{Text: "needle"}, "").Hits[0]
	if n := utf8.RuneCountInString(hit.Excerpt); n > MaxExcerptRunes {
		t.Fatalf("excerpt is %d runes", n)
	}
	if !strings.Contains(hit.Excerpt, "the needle sits here") || !strings.HasPrefix(hit.Excerpt, "…") || !strings.HasSuffix(hit.Excerpt, "…") {
		t.Fatalf("excerpt not around the match: %q", hit.Excerpt)
	}
	korean := strings.Repeat("가나다라 ", 300) + "목표단어 " + strings.Repeat("마바사 ", 300)
	hit = Search([]Entry{newEntry(SourceCodex, "/f", "k", Entry{Title: "t", Body: korean})}, Query{Text: "목표단어"}, "").Hits[0]
	if !utf8.ValidString(hit.Excerpt) || !strings.Contains(hit.Excerpt, "목표단어") || utf8.RuneCountInString(hit.Excerpt) > MaxExcerptRunes {
		t.Fatalf("Korean excerpt: %q", hit.Excerpt)
	}
}
