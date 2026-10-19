package externalmemory

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Result limits (FR18.2). The recall tool returns at most MaxRecallLimit; the
// Profile browser pages through at most MaxBrowseLimit at a time.
const (
	DefaultRecallLimit = 8
	MaxRecallLimit     = 25
	MaxBrowseLimit     = 100
	MaxExcerptRunes    = 600
)

// Query is a keyword and phrase search with filters. Text may hold quoted
// phrases ("fee sensitivity"); everything else is split into terms.
type Query struct {
	Text     string
	Source   string
	Category string
	// Folder keeps entries at or under this folder (a leading ~ expands).
	Folder string
	// Since keeps entries dated on or after this YYYY-MM-DD.
	Since  string
	Limit  int
	Offset int
}

// Hit is one search result.
type Hit struct {
	Entry   Entry
	Excerpt string
	// Phrase is set when a whole phrase matched; Terms counts the distinct
	// query terms found.
	Phrase bool
	Terms  int
}

// Facets count matches by source and by category, ignoring the source and
// category filters, so the browser's chips show what each choice would give.
type Facets struct {
	Sources    map[string]int `json:"sources"`
	Categories map[string]int `json:"categories"`
}

// SearchResult is one page of hits and the total that matched.
type SearchResult struct {
	Hits   []Hit
	Total  int
	Facets Facets
}

// parsedQuery is a query reduced to lowercase phrases and terms.
type parsedQuery struct {
	phrases []string
	terms   []string
}

// parseQuery lowercases the text, takes quoted runs as phrases, and splits the
// rest into terms on anything that is not a letter or digit. An unquoted query
// of several terms is also tried as a phrase. Latin terms of one letter are
// dropped as noise; a single Hangul or Han character is a word and is kept.
func parseQuery(text string) parsedQuery {
	text = foldText(text)
	var q parsedQuery
	var rest strings.Builder
	for {
		start := strings.IndexByte(text, '"')
		if start < 0 {
			rest.WriteString(text)
			break
		}
		end := strings.IndexByte(text[start+1:], '"')
		if end < 0 {
			rest.WriteString(text[:start] + " " + text[start+1:])
			break
		}
		rest.WriteString(text[:start] + " ")
		if phrase := collapseSpace(text[start+1 : start+1+end]); phrase != "" {
			q.phrases = append(q.phrases, phrase)
		}
		text = text[start+1+end+1:]
	}
	seen := map[string]bool{}
	addTerm := func(term string) {
		if term == "" || seen[term] {
			return
		}
		if utf8.RuneCountInString(term) == 1 && !isCJK([]rune(term)[0]) {
			return
		}
		seen[term] = true
		q.terms = append(q.terms, term)
	}
	for _, phrase := range q.phrases {
		for _, term := range splitTerms(phrase) {
			addTerm(term)
		}
	}
	unquoted := splitTerms(rest.String())
	for _, term := range unquoted {
		addTerm(term)
	}
	if len(q.phrases) == 0 && len(unquoted) > 1 {
		q.phrases = append(q.phrases, strings.Join(unquoted, " "))
	}
	return q
}

func (q parsedQuery) empty() bool { return len(q.terms) == 0 && len(q.phrases) == 0 }

func splitTerms(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

func isCJK(r rune) bool {
	return unicode.In(r, unicode.Hangul, unicode.Han, unicode.Hiragana, unicode.Katakana)
}

// foldText lowercases rune by rune, which keeps the rune count, so a position
// found in the folded text is the same position in the original.
func foldText(text string) string {
	return strings.Map(unicode.ToLower, text)
}

// collapseSpace joins every run of whitespace into one space and trims.
func collapseSpace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// Search ranks entries against q: entries with a phrase match first, then by
// the number of distinct terms matched, then newest first. An empty query lists
// every entry that passes the filters, newest first.
func Search(entries []Entry, q Query, home string) SearchResult {
	parsed := parseQuery(q.Text)
	folder := ""
	if q.Folder != "" {
		folder = filepath.Clean(expandHome(strings.TrimSpace(q.Folder), home))
	}
	since := normalizeDate(q.Since)

	result := SearchResult{Facets: Facets{Sources: map[string]int{}, Categories: map[string]int{}}}
	var hits []Hit
	for _, entry := range entries {
		if !entry.Active() {
			continue
		}
		if folder != "" && entry.Folder != folder && !strings.HasPrefix(entry.Folder, strings.TrimRight(folder, "/")+"/") {
			continue
		}
		if since != "" && (entry.Date == "" || entry.Date < since) {
			continue
		}
		hit, ok := matchEntry(entry, parsed)
		if !ok {
			continue
		}
		result.Facets.Sources[entry.Source]++
		result.Facets.Categories[entry.Category]++
		if q.Source != "" && entry.Source != q.Source {
			continue
		}
		if q.Category != "" && entry.Category != q.Category {
			continue
		}
		hits = append(hits, hit)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.Phrase != b.Phrase {
			return a.Phrase
		}
		if a.Terms != b.Terms {
			return a.Terms > b.Terms
		}
		if a.Entry.Date != b.Entry.Date {
			return a.Entry.Date > b.Entry.Date
		}
		if !a.Entry.SyncedAt.Equal(b.Entry.SyncedAt) {
			return a.Entry.SyncedAt.After(b.Entry.SyncedAt)
		}
		return a.Entry.Title < b.Entry.Title
	})
	result.Total = len(hits)

	offset := max(q.Offset, 0)
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultRecallLimit
	}
	limit = min(limit, MaxBrowseLimit)
	if offset >= len(hits) {
		return result
	}
	result.Hits = hits[offset:min(offset+limit, len(hits))]
	return result
}

// matchEntry scores one entry. Title, category, and body are searched; the
// excerpt comes from the body around the best match.
func matchEntry(entry Entry, q parsedQuery) (Hit, bool) {
	hit := Hit{Entry: entry}
	body := collapseSpace(entry.Body)
	if q.empty() {
		hit.Excerpt = excerptAt(body, 0, 0)
		return hit, true
	}
	haystack := foldText(collapseSpace(entry.Title + " " + entry.Category + " " + body))
	foldedBody := foldText(body)

	bestPos, bestLen := -1, 0
	for _, phrase := range q.phrases {
		if strings.Contains(haystack, phrase) {
			hit.Phrase = true
			if pos := strings.Index(foldedBody, phrase); pos >= 0 && bestPos < 0 {
				bestPos, bestLen = pos, len(phrase)
			}
		}
	}
	longest := ""
	for _, term := range q.terms {
		if !strings.Contains(haystack, term) {
			continue
		}
		hit.Terms++
		if len(term) > len(longest) && strings.Contains(foldedBody, term) {
			longest = term
		}
	}
	if !hit.Phrase && hit.Terms == 0 {
		return Hit{}, false
	}
	if bestPos < 0 && longest != "" {
		bestPos, bestLen = strings.Index(foldedBody, longest), len(longest)
	}
	if bestPos < 0 {
		bestPos = 0
	}
	hit.Excerpt = excerptAt(body, utf8.RuneCountInString(foldedBody[:bestPos]), utf8.RuneCountInString(foldedBody[bestPos:bestPos+bestLen]))
	return hit, true
}

// excerptAt cuts at most MaxExcerptRunes of text around the match at rune
// offset pos (length n), starting a third of the window before it and snapping
// to spaces. Cut ends are marked with an ellipsis.
func excerptAt(text string, pos, n int) string {
	runes := []rune(text)
	if len(runes) <= MaxExcerptRunes {
		return text
	}
	window := MaxExcerptRunes - 2 // room for the two ellipses
	start := max(0, pos-window/3)
	if pos+n > start+window {
		start = pos + n - window
	}
	start = max(0, min(start, len(runes)-window))
	end := min(len(runes), start+window)
	if start > 0 {
		for i := start; i < min(start+40, end); i++ {
			if unicode.IsSpace(runes[i]) {
				start = i + 1
				break
			}
		}
	}
	if end < len(runes) {
		for i := end - 1; i > max(end-40, start); i-- {
			if unicode.IsSpace(runes[i]) {
				end = i
				break
			}
		}
	}
	out := strings.TrimSpace(string(runes[start:end]))
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out
}
