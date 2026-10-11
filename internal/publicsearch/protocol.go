package publicsearch

import "strings"

// Shared read-only protocol types preserve the existing utility API schema.
type WebSearchRequest struct {
	Query   string `json:"query"`
	Recency string `json:"recency,omitempty"`
}
type WebSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}
type WebSearchResponse struct {
	Query   string            `json:"query"`
	Results []WebSearchResult `json:"results"`
	Source  string            `json:"source,omitempty"`
}
type DuckDuckGoResponse struct {
	Heading       string           `json:"Heading"`
	AbstractText  string           `json:"AbstractText"`
	AbstractURL   string           `json:"AbstractURL"`
	RelatedTopics []DuckDuckGoItem `json:"RelatedTopics"`
}
type DuckDuckGoItem struct {
	Text      string           `json:"Text"`
	FirstURL  string           `json:"FirstURL"`
	Topics    []DuckDuckGoItem `json:"Topics"`
	Icon      map[string]any   `json:"Icon"`
	Result    string           `json:"Result"`
	Name      string           `json:"Name"`
	MatchType string           `json:"MatchType"`
}

func DuckDuckGoResults(payload DuckDuckGoResponse, query string, maxResults int) []WebSearchResult {
	results := make([]WebSearchResult, 0, maxResults)
	if strings.TrimSpace(payload.AbstractURL) != "" {
		title := strings.TrimSpace(payload.Heading)
		if title == "" {
			title = query
		}
		results = append(results, WebSearchResult{Title: title, URL: payload.AbstractURL, Snippet: strings.TrimSpace(payload.AbstractText)})
	}
	// Iterative source-owner projection: no recursive walk of hostile nesting,
	// and stop after the requested results or a bounded metadata traversal.
	frames := [][]DuckDuckGoItem{payload.RelatedTopics}
	scanned := 0
	for len(frames) > 0 && len(results) < maxResults && scanned < 2000 {
		top := len(frames) - 1
		list := frames[top]
		if len(list) == 0 {
			frames = frames[:top]
			continue
		}
		item := list[0]
		frames[top] = list[1:]
		scanned++
		if len(item.Topics) > 0 {
			frames = append(frames, item.Topics)
			continue
		}
		target := strings.TrimSpace(item.FirstURL)
		if target == "" {
			continue
		}
		text := strings.TrimSpace(item.Text)
		if text == "" {
			text = "Related result"
		}
		title := text
		if len([]rune(title)) > 96 {
			title = strings.TrimSpace(string([]rune(title)[:96])) + "..."
		}
		results = append(results, WebSearchResult{Title: title, URL: target, Snippet: text})
	}
	return results
}
