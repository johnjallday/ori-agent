package blueprintintake

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/publictext"
	"github.com/johnjallday/ori-agent/internal/urlsafety"
)

const MaxLinksPerIntake = 10

type LinkSnapshot struct {
	URL         string
	Title       string
	Content     string
	Body        []byte
	ContentType string
}

type LinkFetcher interface {
	Fetch(context.Context, string) (LinkSnapshot, error)
}

type HTTPLinkFetcher struct {
	client   *http.Client
	maxBytes int64
}

func NewHTTPLinkFetcher(client *http.Client) *HTTPLinkFetcher {
	if client == nil {
		client = &http.Client{Timeout: urlsafety.DefaultTimeout, Transport: urlsafety.NewSafeTransport()}
	}
	clone := *client
	priorRedirect := clone.CheckRedirect
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		if _, err := urlsafety.Parse(req.URL.String(), urlsafety.Policy{BlockPrivateHosts: true}); err != nil {
			return err
		}
		if priorRedirect != nil {
			return priorRedirect(req, via)
		}
		return nil
	}
	return &HTTPLinkFetcher{client: &clone, maxBytes: urlsafety.DefaultMaxResponseBytes}
}

func (f *HTTPLinkFetcher) Fetch(ctx context.Context, rawURL string) (LinkSnapshot, error) {
	if len(strings.TrimSpace(rawURL)) > 2000 {
		return LinkSnapshot{}, fmt.Errorf("url is longer than 2000 characters")
	}
	if f == nil || f.client == nil {
		return LinkSnapshot{}, fmt.Errorf("link fetcher is unavailable")
	}
	target, err := urlsafety.Parse(rawURL, urlsafety.Policy{BlockPrivateHosts: true})
	if err != nil {
		return LinkSnapshot{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return LinkSnapshot{}, err
	}
	req.Header.Set("User-Agent", urlsafety.DefaultUserAgent)
	resp, err := f.client.Do(req)
	if err != nil {
		return LinkSnapshot{}, fmt.Errorf("fetch page: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return LinkSnapshot{}, fmt.Errorf("page returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
	if err != nil {
		return LinkSnapshot{}, fmt.Errorf("read page: %w", err)
	}
	if int64(len(body)) > f.maxBytes {
		return LinkSnapshot{}, fmt.Errorf("page exceeded the %d byte limit", f.maxBytes)
	}
	title, content := extractPageText(body, resp.Header.Get("Content-Type"))
	if strings.TrimSpace(content) == "" {
		return LinkSnapshot{}, fmt.Errorf("page contained no readable text")
	}
	finalURL := resp.Request.URL
	if _, err := urlsafety.Parse(finalURL.String(), urlsafety.Policy{BlockPrivateHosts: true}); err != nil {
		return LinkSnapshot{}, err
	}
	return LinkSnapshot{URL: finalURL.String(), Title: title, Content: content, Body: body, ContentType: resp.Header.Get("Content-Type")}, nil
}

func extractPageText(body []byte, contentType string) (string, string) {
	title, text, _ := publictext.Extract(body, contentType, 0)
	return title, text
}
