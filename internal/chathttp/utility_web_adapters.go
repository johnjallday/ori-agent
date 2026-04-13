package chathttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

const (
	duckDuckGoSearchURL = "https://api.duckduckgo.com/"
	braveSearchURL      = "https://api.search.brave.com/res/v1/web/search"
	utilityDefaultUA    = "ori-agent/utility-tools"
)

// URLSafetyPolicy controls URL-level access restrictions.
type URLSafetyPolicy struct {
	AllowedDomains    []string
	BlockPrivateHosts bool
}

// WebFetchAdapterConfig controls HTTP fetch behavior.
type WebFetchAdapterConfig struct {
	UserAgent        string
	MaxResponseBytes int64
	Safety           URLSafetyPolicy
}

// BrowserAutomationPolicy controls browser automation safety limits.
type BrowserAutomationPolicy struct {
	UserAgent         string
	MaxResponseBytes  int64
	AllowedDomains    []string
	BlockPrivateHosts bool
}

// DefaultWebFetchAdapterConfig returns default fetch settings.
func DefaultWebFetchAdapterConfig() WebFetchAdapterConfig {
	return WebFetchAdapterConfig{
		UserAgent:        utilityDefaultUA,
		MaxResponseBytes: 1 << 20, // 1 MiB
		Safety: URLSafetyPolicy{
			AllowedDomains:    nil,
			BlockPrivateHosts: true,
		},
	}
}

// DefaultBrowserAutomationPolicy returns default browser automation settings.
func DefaultBrowserAutomationPolicy() BrowserAutomationPolicy {
	return BrowserAutomationPolicy{
		UserAgent:         utilityDefaultUA,
		MaxResponseBytes:  1 << 20, // 1 MiB
		AllowedDomains:    nil,
		BlockPrivateHosts: true,
	}
}

// NewDefaultWebSearchAdapter returns default search adapter.
func NewDefaultWebSearchAdapter(client *http.Client) WebSearchAdapter {
	braveKey := strings.TrimSpace(os.Getenv("BRAVE_API_KEY"))
	if braveKey != "" {
		return NewBraveWebSearchAdapter(client, braveKey)
	}
	return NewDuckDuckGoWebSearchAdapter(client)
}

// -----------------------------------------------------------------------------
// Web search adapters
// -----------------------------------------------------------------------------

// DuckDuckGoWebSearchAdapter provides keyless web search via DuckDuckGo Instant Answer.
type DuckDuckGoWebSearchAdapter struct {
	HTTPClient *http.Client
	Endpoint   string
	MaxResults int
}

// NewDuckDuckGoWebSearchAdapter creates a DuckDuckGo search adapter.
func NewDuckDuckGoWebSearchAdapter(client *http.Client) *DuckDuckGoWebSearchAdapter {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	return &DuckDuckGoWebSearchAdapter{
		HTTPClient: client,
		Endpoint:   duckDuckGoSearchURL,
		MaxResults: 5,
	}
}

type duckDuckGoResponse struct {
	Heading       string           `json:"Heading"`
	AbstractText  string           `json:"AbstractText"`
	AbstractURL   string           `json:"AbstractURL"`
	RelatedTopics []duckDuckGoItem `json:"RelatedTopics"`
}

type duckDuckGoItem struct {
	Text      string           `json:"Text"`
	FirstURL  string           `json:"FirstURL"`
	Topics    []duckDuckGoItem `json:"Topics"`
	Icon      map[string]any   `json:"Icon"`
	Result    string           `json:"Result"`
	Name      string           `json:"Name"`
	MatchType string           `json:"MatchType"`
}

// WebSearch performs a keyless search using DuckDuckGo.
func (a *DuckDuckGoWebSearchAdapter) WebSearch(ctx context.Context, req WebSearchRequest) (WebSearchResponse, error) {
	if a == nil || a.HTTPClient == nil {
		return WebSearchResponse{}, fmt.Errorf("%w: duckduckgo search adapter unavailable", ErrUtilityProviderUnavailable)
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return WebSearchResponse{}, fmt.Errorf("%w: query is required", ErrUtilityInvalidInput)
	}

	endpoint, err := url.Parse(a.Endpoint)
	if err != nil {
		return WebSearchResponse{}, fmt.Errorf("invalid duckduckgo endpoint: %w", err)
	}
	values := endpoint.Query()
	values.Set("q", query)
	values.Set("format", "json")
	values.Set("no_redirect", "1")
	values.Set("no_html", "1")
	endpoint.RawQuery = values.Encode()

	body, _, _, err := doUtilityHTTPGet(ctx, a.HTTPClient, endpoint, utilityDefaultUA, 1<<20)
	if err != nil {
		return WebSearchResponse{}, err
	}

	var payload duckDuckGoResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return WebSearchResponse{}, fmt.Errorf("failed to parse duckduckgo response: %w", err)
	}

	results := make([]WebSearchResult, 0, a.MaxResults)
	if strings.TrimSpace(payload.AbstractURL) != "" {
		title := strings.TrimSpace(payload.Heading)
		if title == "" {
			title = query
		}
		results = append(results, WebSearchResult{
			Title:   title,
			URL:     payload.AbstractURL,
			Snippet: strings.TrimSpace(payload.AbstractText),
		})
	}

	for _, item := range flattenDuckDuckGoItems(payload.RelatedTopics) {
		if len(results) >= a.MaxResults {
			break
		}
		u := strings.TrimSpace(item.FirstURL)
		if u == "" {
			continue
		}
		text := strings.TrimSpace(item.Text)
		if text == "" {
			text = "Related result"
		}
		results = append(results, WebSearchResult{
			Title:   truncateRunes(text, 96),
			URL:     u,
			Snippet: text,
		})
	}

	return WebSearchResponse{
		Query:   query,
		Results: results,
		Source:  "duckduckgo.com",
	}, nil
}

func flattenDuckDuckGoItems(items []duckDuckGoItem) []duckDuckGoItem {
	out := make([]duckDuckGoItem, 0, len(items))
	var visit func([]duckDuckGoItem)
	visit = func(list []duckDuckGoItem) {
		for _, item := range list {
			if len(item.Topics) > 0 {
				visit(item.Topics)
				continue
			}
			out = append(out, item)
		}
	}
	visit(items)
	return out
}

// BraveWebSearchAdapter provides web search via Brave Search API.
type BraveWebSearchAdapter struct {
	HTTPClient *http.Client
	Endpoint   string
	APIKey     string
	MaxResults int
}

// NewBraveWebSearchAdapter creates a Brave Search adapter.
func NewBraveWebSearchAdapter(client *http.Client, apiKey string) *BraveWebSearchAdapter {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	return &BraveWebSearchAdapter{
		HTTPClient: client,
		Endpoint:   braveSearchURL,
		APIKey:     strings.TrimSpace(apiKey),
		MaxResults: 5,
	}
}

type braveSearchResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}

// WebSearch performs a Brave API search.
func (a *BraveWebSearchAdapter) WebSearch(ctx context.Context, req WebSearchRequest) (WebSearchResponse, error) {
	if a == nil || a.HTTPClient == nil || strings.TrimSpace(a.APIKey) == "" {
		return WebSearchResponse{}, fmt.Errorf("%w: brave api key missing", ErrUtilityProviderUnavailable)
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return WebSearchResponse{}, fmt.Errorf("%w: query is required", ErrUtilityInvalidInput)
	}

	endpoint, err := url.Parse(a.Endpoint)
	if err != nil {
		return WebSearchResponse{}, fmt.Errorf("invalid brave endpoint: %w", err)
	}
	values := endpoint.Query()
	values.Set("q", query)
	values.Set("count", "5")
	endpoint.RawQuery = values.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return WebSearchResponse{}, fmt.Errorf("failed to create brave request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("X-Subscription-Token", a.APIKey)

	resp, err := a.HTTPClient.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return WebSearchResponse{}, ctx.Err()
		}
		return WebSearchResponse{}, fmt.Errorf("brave request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return WebSearchResponse{}, fmt.Errorf("failed to read brave response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return WebSearchResponse{}, fmt.Errorf("brave search returned HTTP %d", resp.StatusCode)
	}

	var payload braveSearchResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return WebSearchResponse{}, fmt.Errorf("failed to parse brave response: %w", err)
	}

	results := make([]WebSearchResult, 0, a.MaxResults)
	for _, item := range payload.Web.Results {
		if len(results) >= a.MaxResults {
			break
		}
		results = append(results, WebSearchResult{
			Title:   strings.TrimSpace(item.Title),
			URL:     strings.TrimSpace(item.URL),
			Snippet: strings.TrimSpace(item.Description),
		})
	}

	return WebSearchResponse{
		Query:   query,
		Results: results,
		Source:  "search.brave.com",
	}, nil
}

// -----------------------------------------------------------------------------
// Web fetch adapter
// -----------------------------------------------------------------------------

// HTTPWebFetchAdapter fetches URLs and extracts readable content.
type HTTPWebFetchAdapter struct {
	HTTPClient *http.Client
	Config     WebFetchAdapterConfig
}

// NewHTTPWebFetchAdapter creates a web fetch adapter with safety defaults.
// When BlockPrivateHosts is enabled and no custom client is provided, the
// default client uses an SSRF-safe transport that validates resolved IPs at
// dial time to prevent DNS rebinding attacks.
func NewHTTPWebFetchAdapter(client *http.Client, cfg WebFetchAdapterConfig) *HTTPWebFetchAdapter {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
		if cfg.Safety.BlockPrivateHosts {
			client.Transport = newSSRFSafeTransport()
		}
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = DefaultWebFetchAdapterConfig().MaxResponseBytes
	}
	if strings.TrimSpace(cfg.UserAgent) == "" {
		cfg.UserAgent = utilityDefaultUA
	}
	return &HTTPWebFetchAdapter{
		HTTPClient: client,
		Config:     cfg,
	}
}

// WebFetch fetches and extracts page content from URL.
func (a *HTTPWebFetchAdapter) WebFetch(ctx context.Context, req WebFetchRequest) (WebFetchResponse, error) {
	if a == nil || a.HTTPClient == nil {
		return WebFetchResponse{}, fmt.Errorf("%w: web fetch adapter unavailable", ErrUtilityProviderUnavailable)
	}
	targetURL, err := parseAndValidateUtilityURL(strings.TrimSpace(req.URL), a.Config.Safety)
	if err != nil {
		return WebFetchResponse{}, err
	}

	body, contentType, finalURL, err := doUtilityHTTPGet(ctx, a.HTTPClient, targetURL, a.Config.UserAgent, a.Config.MaxResponseBytes)
	if err != nil {
		return WebFetchResponse{}, err
	}

	title := ""
	content := ""
	if looksLikeHTML(contentType, body) {
		title = extractHTMLTitle(body)
		content = extractVisibleTextFromHTML(body, nil)
	} else {
		content = normalizeWhitespace(string(body))
	}
	content = truncateRunes(content, 4000)

	return WebFetchResponse{
		URL:     finalURL.String(),
		Title:   title,
		Content: content,
		Summary: summarizeText(content, 600),
		Source:  finalURL.Hostname(),
	}, nil
}

// -----------------------------------------------------------------------------
// Browser automation adapter (lightweight HTML/session model)
// -----------------------------------------------------------------------------

type browserState struct {
	URL         *url.URL
	HTML        []byte
	Title       string
	TypedInputs map[string]string
}

// SimpleBrowserAutomationAdapter provides basic browser-like actions with URL safety.
type SimpleBrowserAutomationAdapter struct {
	HTTPClient *http.Client
	Policy     BrowserAutomationPolicy

	mu    sync.Mutex
	state browserState
}

// NewSimpleBrowserAutomationAdapter creates a browser automation adapter.
// When BlockPrivateHosts is enabled and no custom client is provided, the
// default client uses an SSRF-safe transport to prevent DNS rebinding attacks.
func NewSimpleBrowserAutomationAdapter(client *http.Client, policy BrowserAutomationPolicy) *SimpleBrowserAutomationAdapter {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
		if policy.BlockPrivateHosts {
			client.Transport = newSSRFSafeTransport()
		}
	}
	if policy.MaxResponseBytes <= 0 {
		policy.MaxResponseBytes = DefaultBrowserAutomationPolicy().MaxResponseBytes
	}
	if strings.TrimSpace(policy.UserAgent) == "" {
		policy.UserAgent = utilityDefaultUA
	}
	return &SimpleBrowserAutomationAdapter{
		HTTPClient: client,
		Policy:     policy,
		state: browserState{
			TypedInputs: make(map[string]string),
		},
	}
}

// BrowserAction executes open/click/type/extract actions in a safe session model.
func (a *SimpleBrowserAutomationAdapter) BrowserAction(ctx context.Context, req BrowserRequest) (BrowserResponse, error) {
	if a == nil || a.HTTPClient == nil {
		return BrowserResponse{}, fmt.Errorf("%w: browser adapter unavailable", ErrUtilityProviderUnavailable)
	}

	action := strings.ToLower(strings.TrimSpace(req.Action))
	switch action {
	case "open_url":
		return a.openURL(ctx, req.URL)
	case "click":
		return a.click(ctx, req.Selector)
	case "type":
		return a.typeIntoSelector(req.Selector, req.Text)
	case "extract_text":
		return a.extractText(req.Selector)
	default:
		return BrowserResponse{}, fmt.Errorf("%w: unsupported action %q", ErrUtilityInvalidInput, req.Action)
	}
}

func (a *SimpleBrowserAutomationAdapter) openURL(ctx context.Context, rawURL string) (BrowserResponse, error) {
	targetURL, err := parseAndValidateUtilityURL(strings.TrimSpace(rawURL), URLSafetyPolicy{
		AllowedDomains:    a.Policy.AllowedDomains,
		BlockPrivateHosts: a.Policy.BlockPrivateHosts,
	})
	if err != nil {
		return BrowserResponse{}, err
	}

	body, contentType, finalURL, err := doUtilityHTTPGet(ctx, a.HTTPClient, targetURL, a.Policy.UserAgent, a.Policy.MaxResponseBytes)
	if err != nil {
		return BrowserResponse{}, err
	}

	if !looksLikeHTML(contentType, body) {
		return BrowserResponse{}, fmt.Errorf("%w: open_url expects HTML content", ErrUtilityInvalidInput)
	}

	title := extractHTMLTitle(body)

	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.URL = finalURL
	a.state.HTML = body
	a.state.Title = title
	if a.state.TypedInputs == nil {
		a.state.TypedInputs = make(map[string]string)
	}

	msg := "Opened " + finalURL.String()
	if title != "" {
		msg += " (" + title + ")"
	}
	return BrowserResponse{
		Action:  "open_url",
		Success: true,
		Result:  msg,
	}, nil
}

func (a *SimpleBrowserAutomationAdapter) click(ctx context.Context, selector string) (BrowserResponse, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return BrowserResponse{}, fmt.Errorf("%w: selector is required", ErrUtilityInvalidInput)
	}

	a.mu.Lock()
	currentURL := a.state.URL
	currentHTML := append([]byte(nil), a.state.HTML...)
	a.mu.Unlock()

	if currentURL == nil || len(currentHTML) == 0 {
		return BrowserResponse{}, fmt.Errorf("%w: open_url must be called before click", ErrUtilityInvalidInput)
	}

	root, err := html.Parse(bytes.NewReader(currentHTML))
	if err != nil {
		return BrowserResponse{}, fmt.Errorf("failed to parse current page: %w", err)
	}
	spec, err := parseSimpleSelector(selector)
	if err != nil {
		return BrowserResponse{}, err
	}
	node := findFirstNodeBySelector(root, spec)
	if node == nil {
		return BrowserResponse{}, fmt.Errorf("%w: selector %q not found", ErrUtilityInvalidInput, selector)
	}

	if strings.EqualFold(node.Data, "a") {
		href := strings.TrimSpace(getNodeAttr(node, "href"))
		if href != "" {
			nextURL, resolveErr := currentURL.Parse(href)
			if resolveErr != nil {
				return BrowserResponse{}, fmt.Errorf("%w: invalid link target", ErrUtilityInvalidInput)
			}
			return a.openURL(ctx, nextURL.String())
		}
	}

	return BrowserResponse{
		Action:  "click",
		Success: true,
		Result:  "Clicked selector " + selector,
	}, nil
}

func (a *SimpleBrowserAutomationAdapter) typeIntoSelector(selector, text string) (BrowserResponse, error) {
	selector = strings.TrimSpace(selector)
	text = strings.TrimSpace(text)
	if selector == "" || text == "" {
		return BrowserResponse{}, fmt.Errorf("%w: selector and text are required", ErrUtilityInvalidInput)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.URL == nil || len(a.state.HTML) == 0 {
		return BrowserResponse{}, fmt.Errorf("%w: open_url must be called before type", ErrUtilityInvalidInput)
	}
	if a.state.TypedInputs == nil {
		a.state.TypedInputs = make(map[string]string)
	}
	a.state.TypedInputs[selector] = text

	return BrowserResponse{
		Action:  "type",
		Success: true,
		Result:  "Typed text into " + selector,
	}, nil
}

func (a *SimpleBrowserAutomationAdapter) extractText(selector string) (BrowserResponse, error) {
	selector = strings.TrimSpace(selector)

	a.mu.Lock()
	currentHTML := append([]byte(nil), a.state.HTML...)
	a.mu.Unlock()

	if len(currentHTML) == 0 {
		return BrowserResponse{}, fmt.Errorf("%w: open_url must be called before extract_text", ErrUtilityInvalidInput)
	}

	root, err := html.Parse(bytes.NewReader(currentHTML))
	if err != nil {
		return BrowserResponse{}, fmt.Errorf("failed to parse current page: %w", err)
	}

	if selector == "" {
		text := truncateRunes(extractVisibleTextFromHTML(currentHTML, nil), 2000)
		return BrowserResponse{
			Action:  "extract_text",
			Success: true,
			Result:  text,
		}, nil
	}

	spec, err := parseSimpleSelector(selector)
	if err != nil {
		return BrowserResponse{}, err
	}
	nodes := findAllNodesBySelector(root, spec)
	if len(nodes) == 0 {
		return BrowserResponse{}, fmt.Errorf("%w: selector %q not found", ErrUtilityInvalidInput, selector)
	}

	parts := make([]string, 0, len(nodes))
	for _, node := range nodes {
		text := normalizeWhitespace(extractVisibleTextFromNode(node))
		if text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return BrowserResponse{}, fmt.Errorf("%w: no visible text for selector %q", ErrUtilityInvalidInput, selector)
	}

	return BrowserResponse{
		Action:  "extract_text",
		Success: true,
		Result:  truncateRunes(strings.Join(parts, "\n"), 2000),
	}, nil
}

// -----------------------------------------------------------------------------
// Shared utility helpers
// -----------------------------------------------------------------------------

func doUtilityHTTPGet(ctx context.Context, client *http.Client, targetURL *url.URL, userAgent string, maxBytes int64) ([]byte, string, *url.URL, error) {
	if client == nil {
		return nil, "", nil, fmt.Errorf("%w: http client unavailable", ErrUtilityProviderUnavailable)
	}
	if targetURL == nil {
		return nil, "", nil, fmt.Errorf("%w: target url is required", ErrUtilityInvalidInput)
	}
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL.String(), nil)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to create request: %w", err)
	}
	if strings.TrimSpace(userAgent) != "" {
		req.Header.Set("User-Agent", userAgent)
	}

	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", nil, ctx.Err()
		}
		return nil, "", nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", nil, fmt.Errorf("request returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to read response body: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, "", nil, fmt.Errorf("response exceeded size limit")
	}

	return body, resp.Header.Get("Content-Type"), resp.Request.URL, nil
}

func parseAndValidateUtilityURL(rawURL string, policy URLSafetyPolicy) (*url.URL, error) {
	candidate := strings.TrimSpace(rawURL)
	if candidate == "" {
		return nil, fmt.Errorf("%w: url is required", ErrUtilityInvalidInput)
	}
	if !strings.Contains(candidate, "://") {
		candidate = "https://" + candidate
	}

	parsed, err := url.Parse(candidate)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid url", ErrUtilityInvalidInput)
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return nil, fmt.Errorf("%w: url must use http or https", ErrUtilityInvalidInput)
	}

	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return nil, fmt.Errorf("%w: url host is required", ErrUtilityInvalidInput)
	}

	if policy.BlockPrivateHosts && isPrivateUtilityHost(host) {
		return nil, fmt.Errorf("%w: private or local hosts are blocked", ErrUtilityInvalidInput)
	}
	if len(policy.AllowedDomains) > 0 && !matchesAllowedUtilityDomain(host, policy.AllowedDomains) {
		return nil, fmt.Errorf("%w: host %q is not in allowed domains", ErrUtilityInvalidInput, host)
	}

	return parsed, nil
}

func isPrivateUtilityHost(host string) bool {
	if host == "" {
		return true
	}
	if host == "localhost" || strings.HasSuffix(host, ".local") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return isPrivateIP(addr)
}

func isPrivateIP(addr netip.Addr) bool {
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified()
}

// newSSRFSafeTransport returns an http.Transport that rejects connections to
// private/loopback IPs at dial time, preventing DNS rebinding SSRF attacks.
func newSSRFSafeTransport() *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("ssrf check: invalid address %q: %w", addr, err)
			}

			// Resolve the hostname to IPs and reject private addresses.
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, fmt.Errorf("ssrf check: dns lookup failed for %q: %w", host, err)
			}
			for _, ip := range ips {
				if isPrivateIP(ip) {
					return nil, fmt.Errorf("ssrf check: resolved to private IP %s", ip)
				}
			}

			// All IPs are public — proceed with the connection.
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(host, port))
		},
	}
}

func matchesAllowedUtilityDomain(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, raw := range allowed {
		domain := strings.ToLower(strings.TrimSpace(raw))
		if domain == "" {
			continue
		}
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func looksLikeHTML(contentType string, body []byte) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml+xml") {
		return true
	}
	peek := strings.ToLower(strings.TrimSpace(string(body[:min(len(body), 128)])))
	return strings.Contains(peek, "<html") || strings.Contains(peek, "<!doctype html")
}

func extractHTMLTitle(raw []byte) string {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return ""
	}
	var title string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if title != "" || n == nil {
			return
		}
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "title") && n.FirstChild != nil {
			title = normalizeWhitespace(n.FirstChild.Data)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
			if title != "" {
				return
			}
		}
	}
	walk(doc)
	return title
}

func extractVisibleTextFromHTML(raw []byte, selector *simpleSelector) string {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return normalizeWhitespace(string(raw))
	}

	nodes := []*html.Node{doc}
	if selector != nil {
		nodes = findAllNodesBySelector(doc, *selector)
	}
	parts := make([]string, 0, len(nodes))
	for _, node := range nodes {
		text := normalizeWhitespace(extractVisibleTextFromNode(node))
		if text != "" {
			parts = append(parts, text)
		}
	}
	return normalizeWhitespace(strings.Join(parts, " "))
}

func extractVisibleTextFromNode(root *html.Node) string {
	if root == nil {
		return ""
	}
	var builder strings.Builder
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, skip bool) {
		if n == nil {
			return
		}
		if n.Type == html.ElementNode {
			tag := strings.ToLower(strings.TrimSpace(n.Data))
			if tag == "script" || tag == "style" || tag == "noscript" {
				skip = true
			}
		}
		if !skip && n.Type == html.TextNode {
			chunk := normalizeWhitespace(n.Data)
			if chunk != "" {
				if builder.Len() > 0 {
					builder.WriteByte(' ')
				}
				builder.WriteString(chunk)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, skip)
		}
	}
	walk(root, false)
	return builder.String()
}

func normalizeWhitespace(input string) string {
	fields := strings.Fields(strings.TrimSpace(input))
	return strings.Join(fields, " ")
}

func summarizeText(input string, maxRunes int) string {
	text := normalizeWhitespace(input)
	if text == "" {
		return ""
	}
	return truncateRunes(text, maxRunes)
}

func truncateRunes(input string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(input)
	if len(runes) <= maxRunes {
		return input
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type simpleSelector struct {
	kind     string // tag | id | class | attr
	name     string
	attrName string
	attrVal  string
}

func parseSimpleSelector(raw string) (simpleSelector, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return simpleSelector{}, fmt.Errorf("%w: selector is required", ErrUtilityInvalidInput)
	}

	if strings.HasPrefix(s, "#") {
		id := strings.TrimSpace(strings.TrimPrefix(s, "#"))
		if id == "" {
			return simpleSelector{}, fmt.Errorf("%w: invalid id selector", ErrUtilityInvalidInput)
		}
		return simpleSelector{kind: "id", name: id}, nil
	}
	if strings.HasPrefix(s, ".") {
		className := strings.TrimSpace(strings.TrimPrefix(s, "."))
		if className == "" {
			return simpleSelector{}, fmt.Errorf("%w: invalid class selector", ErrUtilityInvalidInput)
		}
		return simpleSelector{kind: "class", name: className}, nil
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		payload := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "["), "]"))
		parts := strings.SplitN(payload, "=", 2)
		if len(parts) != 2 {
			return simpleSelector{}, fmt.Errorf("%w: invalid attribute selector", ErrUtilityInvalidInput)
		}
		attrName := strings.TrimSpace(parts[0])
		attrValue := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		if attrName == "" || attrValue == "" {
			return simpleSelector{}, fmt.Errorf("%w: invalid attribute selector", ErrUtilityInvalidInput)
		}
		return simpleSelector{kind: "attr", attrName: strings.ToLower(attrName), attrVal: attrValue}, nil
	}

	tag := strings.ToLower(strings.TrimSpace(s))
	return simpleSelector{kind: "tag", name: tag}, nil
}

func findFirstNodeBySelector(root *html.Node, selector simpleSelector) *html.Node {
	if root == nil {
		return nil
	}
	if matchesSelector(root, selector) {
		return root
	}
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if found := findFirstNodeBySelector(c, selector); found != nil {
			return found
		}
	}
	return nil
}

func findAllNodesBySelector(root *html.Node, selector simpleSelector) []*html.Node {
	if root == nil {
		return nil
	}
	out := []*html.Node{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil {
			return
		}
		if matchesSelector(n, selector) {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

func matchesSelector(node *html.Node, selector simpleSelector) bool {
	if node == nil || node.Type != html.ElementNode {
		return false
	}
	switch selector.kind {
	case "tag":
		return strings.EqualFold(node.Data, selector.name)
	case "id":
		return strings.EqualFold(getNodeAttr(node, "id"), selector.name)
	case "class":
		classes := strings.Fields(strings.ToLower(getNodeAttr(node, "class")))
		needle := strings.ToLower(selector.name)
		for _, className := range classes {
			if className == needle {
				return true
			}
		}
		return false
	case "attr":
		return strings.EqualFold(getNodeAttr(node, selector.attrName), selector.attrVal)
	default:
		return false
	}
}

func getNodeAttr(node *html.Node, attrName string) string {
	if node == nil {
		return ""
	}
	attrName = strings.ToLower(strings.TrimSpace(attrName))
	for _, attr := range node.Attr {
		if strings.EqualFold(strings.TrimSpace(attr.Key), attrName) {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}
