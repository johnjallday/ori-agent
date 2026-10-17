package linkcontext

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/johnjallday/ori-agent/internal/publicread"
	"github.com/johnjallday/ori-agent/internal/urlsafety"
)

const (
	pageTimeout      = 10 * time.Second
	pageMaxRedirects = 3
	// pageMaxBytes bounds what is read of a response. Only the document head
	// is parsed, and parsing stops at <body>, so this is a ceiling for a long
	// head, not an allowance for the page.
	pageMaxBytes  = 512 << 10
	pageUserAgent = "ori-agent/link-context"
)

// ErrPageRedirect is a redirect the reader refused: too many hops, or a hop
// to an address that is not public.
var ErrPageRedirect = errors.New("link context: redirect refused")

// HTTPPages reads what a public page says about itself: its <title> and its
// meta description (or the Open Graph equivalents). It parses the document
// head only and stops at <body>; nothing in the page's body is read or kept.
//
// Every request and every redirect hop must pass publicread.ValidateURL (http
// or https on the default port, a public host, no credential-like material in
// the address), and the transport dials only public addresses it resolved
// itself. Unlike publicread's reader, a redirect may change scheme or host
// (http to https, a www host, a DOI resolver), up to pageMaxRedirects hops,
// because the address a person pastes is very often one of those.
type HTTPPages struct {
	client *http.Client
}

// NewHTTPPages builds the reader on urlsafety's public transport. A non-nil
// client is for tests: it must bring its own transport.
func NewHTTPPages(client *http.Client) *HTTPPages {
	if client == nil {
		client = &http.Client{Transport: urlsafety.NewPublicTransport()}
	}
	clone := *client
	clone.Timeout = pageTimeout
	clone.Jar = nil
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > pageMaxRedirects {
			return ErrPageRedirect
		}
		if _, err := publicread.ValidateURL(req.URL.String()); err != nil {
			return ErrPageRedirect
		}
		// No cookies, credentials or referrer survive a hop.
		clear(req.Header)
		req.Header.Set("User-Agent", pageUserAgent)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.8,*/*;q=0.5")
		return nil
	}
	return &HTTPPages{client: &clone}
}

// Fetch implements PageFetcher. A page that cannot be reached or says nothing
// about itself is Readable false, not an error; only a refused redirect is an
// error, because it means the address led somewhere Ori must not go.
func (p *HTTPPages) Fetch(ctx context.Context, target *url.URL) (Page, error) {
	page := Page{URL: target.String(), Kind: kindFromPath(target)}
	if p == nil || p.client == nil {
		return page, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return page, nil
	}
	req.Header.Set("User-Agent", pageUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.8,*/*;q=0.5")
	resp, err := p.client.Do(req)
	if err != nil {
		if errors.Is(err, ErrPageRedirect) {
			// Bare: the client's error names the refused target, which may be
			// a signed address that must not reach a log.
			return Page{}, ErrPageRedirect
		}
		return page, nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Request != nil && resp.Request.URL != nil {
		page.URL = resp.Request.URL.String()
		page.Kind = kindFromPath(resp.Request.URL)
	}
	if resp.StatusCode != http.StatusOK {
		return page, nil
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	mediaType = strings.ToLower(mediaType)
	switch mediaType {
	case "application/pdf":
		// A document is known by its address. It is never downloaded here.
		page.Kind = KindDocument
		page.Title = documentTitle(resp.Request.URL)
		return page, nil
	case "text/html", "application/xhtml+xml":
		page.Kind = KindPage
		page.Title, page.Description = readHead(io.LimitReader(resp.Body, pageMaxBytes))
	case "text/markdown", "text/x-markdown":
		page.Kind = KindPage
		page.Title = markdownTitle(io.LimitReader(resp.Body, pageMaxBytes))
	case "text/plain":
		page.Kind = KindPage
	default:
		return page, nil
	}
	page.Title, page.Description = Text(page.Title), Text(page.Description)
	page.Readable = page.Title != "" || page.Description != ""
	return page, nil
}

// readHead tokenizes the document until <body> (or the end of <head>) and
// keeps the title and the description. It never reads text outside <title>.
func readHead(body io.Reader) (title, description string) {
	tokens := html.NewTokenizer(body)
	var ogTitle, ogDescription string
	inTitle := false
	var titleText strings.Builder
	for {
		switch tokens.Next() {
		case html.ErrorToken:
			return pickHead(titleText.String(), ogTitle, description, ogDescription)
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := tokens.TagName()
			switch string(name) {
			case "body":
				return pickHead(titleText.String(), ogTitle, description, ogDescription)
			case "title":
				inTitle = true
			case "meta":
				if !hasAttr {
					continue
				}
				key, content := metaPair(tokens)
				switch key {
				case "description":
					if description == "" {
						description = content
					}
				case "og:description", "twitter:description":
					if ogDescription == "" {
						ogDescription = content
					}
				case "og:title", "twitter:title":
					if ogTitle == "" {
						ogTitle = content
					}
				}
			}
		case html.EndTagToken:
			name, _ := tokens.TagName()
			switch string(name) {
			case "title":
				inTitle = false
			case "head":
				return pickHead(titleText.String(), ogTitle, description, ogDescription)
			}
		case html.TextToken:
			if inTitle && titleText.Len() < 4*MaxTextRunes {
				titleText.Write(tokens.Text())
			}
		}
	}
}

func pickHead(title, ogTitle, description, ogDescription string) (string, string) {
	if strings.TrimSpace(title) == "" {
		title = ogTitle
	}
	if strings.TrimSpace(description) == "" {
		description = ogDescription
	}
	return html.UnescapeString(strings.TrimSpace(title)), html.UnescapeString(strings.TrimSpace(description))
}

// metaPair reads a <meta> tag's name (or property) and content.
func metaPair(tokens *html.Tokenizer) (key, content string) {
	for {
		name, value, more := tokens.TagAttr()
		switch strings.ToLower(string(name)) {
		case "name", "property":
			key = strings.ToLower(strings.TrimSpace(string(value)))
		case "content":
			content = string(value)
		}
		if !more {
			return key, content
		}
	}
}

// markdownTitle is a Markdown document's first heading, from its first few
// lines only.
func markdownTitle(body io.Reader) string {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 4096), 16<<10)
	for line := 0; line < 20 && scanner.Scan(); line++ {
		text := bytes.TrimSpace(scanner.Bytes())
		if heading, ok := bytes.CutPrefix(text, []byte("# ")); ok {
			return string(heading)
		}
	}
	return ""
}

// kindFromPath calls an address ending in .pdf a document before anything is
// fetched, so a document that cannot be reached is still known as one.
func kindFromPath(target *url.URL) Kind {
	if target != nil && strings.EqualFold(path.Ext(target.Path), ".pdf") {
		return KindDocument
	}
	return KindPage
}

// documentTitle names a document by the last segment of its address.
func documentTitle(target *url.URL) string {
	if target == nil {
		return ""
	}
	segment, err := url.PathUnescape(path.Base(target.Path))
	if err != nil || segment == "/" || segment == "." {
		return ""
	}
	return segment
}
