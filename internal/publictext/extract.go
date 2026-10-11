// Package publictext extracts plain reference text, never executable markup.
package publictext

import (
	"bytes"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Extract shares blueprint intake's existing visible-text behavior. limit=0 is
// its legacy full bounded-page path; research uses a positive excerpt ceiling.
// A streaming tokenizer avoids an unbounded recursion stack on hostile HTML.
func Extract(body []byte, contentType string, limit int) (title, content string, truncated bool) {
	if !strings.Contains(strings.ToLower(contentType), "html") && !bytes.Contains(bytes.ToLower(body[:min(len(body), 128)]), []byte("<html")) {
		content = strings.Join(strings.Fields(string(body)), " ")
		if limit > 0 && utf8.RuneCountInString(content) > limit {
			content = string([]rune(content)[:limit])
			truncated = true
		}
		return "", content, truncated
	}
	tokens := html.NewTokenizer(bytes.NewReader(body))
	var out strings.Builder
	runes := 0
	inTitle, hidden := false, ""
	for {
		kind := tokens.Next()
		if kind == html.ErrorToken {
			if tokens.Err() != io.EOF {
				return "", "", false
			}
			break
		}
		switch kind {
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := tokens.TagName()
			tag := string(name)
			if hidden == "" && (tag == "script" || tag == "style" || tag == "noscript") {
				hidden = tag
			}
			if tag == "title" {
				inTitle = true
			}
		case html.EndTagToken:
			name, _ := tokens.TagName()
			tag := string(name)
			if tag == hidden {
				hidden = ""
			}
			if tag == "title" {
				inTitle = false
			}
		case html.TextToken:
			if hidden != "" {
				continue
			}
			text := strings.Join(strings.Fields(string(tokens.Text())), " ")
			if text == "" {
				continue
			}
			if inTitle && title == "" {
				title = text
			}
			if out.Len() > 0 {
				if limit > 0 && runes >= limit {
					return title, out.String(), true
				}
				out.WriteByte(' ')
				runes++
			}
			for _, ch := range text {
				if limit > 0 && runes >= limit {
					return title, out.String(), true
				}
				out.WriteRune(ch)
				runes++
			}
		}
	}
	return title, out.String(), false
}
