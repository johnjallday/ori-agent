package mcp

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestValidateRemoteEndpoint(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid https", "https://calendarmcp.googleapis.com/mcp/v1", false},
		{"empty", "", true},
		{"http scheme rejected", "http://example.com/mcp", true},
		{"userinfo rejected", "https://user:pass@example.com/mcp", true},
		{"fragment rejected", "https://example.com/mcp#frag", true},
		{"no host", "https:///mcp", true},
		{"localhost rejected", "https://localhost/mcp", true},
		{"dot-local rejected", "https://myserver.local/mcp", true},
		{"loopback ip rejected", "https://127.0.0.1/mcp", true},
		{"ipv6 loopback rejected", "https://[::1]/mcp", true},
		{"private ip rejected", "https://10.0.0.5/mcp", true},
		{"link-local rejected", "https://169.254.1.1/mcp", true},
		{"malformed url", "https://exa mple.com/mcp", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateRemoteEndpoint(tc.url)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for %q, got nil", tc.url)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.url, err)
			}
		})
	}
}

func TestValidateServerConfig(t *testing.T) {
	cases := []struct {
		name    string
		cfg     ServerConfig
		wantErr bool
	}{
		{
			name:    "missing name",
			cfg:     ServerConfig{Command: "npx"},
			wantErr: true,
		},
		{
			name:    "stdio with command ok",
			cfg:     ServerConfig{Name: "fs", Command: "npx", Args: []string{"-y"}},
			wantErr: false,
		},
		{
			name:    "stdio omitted transport ok",
			cfg:     ServerConfig{Name: "fs", Command: "npx"},
			wantErr: false,
		},
		{
			name:    "stdio with url rejected",
			cfg:     ServerConfig{Name: "fs", Command: "npx", URL: "https://example.com"},
			wantErr: true,
		},
		{
			name:    "remote ok",
			cfg:     ServerConfig{Name: "gcal", Transport: TransportStreamableHTTP, URL: "https://calendarmcp.googleapis.com/mcp/v1"},
			wantErr: false,
		},
		{
			name:    "remote with command rejected",
			cfg:     ServerConfig{Name: "gcal", Transport: TransportStreamableHTTP, URL: "https://example.com/mcp", Command: "npx"},
			wantErr: true,
		},
		{
			name:    "remote with args rejected",
			cfg:     ServerConfig{Name: "gcal", Transport: TransportStreamableHTTP, URL: "https://example.com/mcp", Args: []string{"-y"}},
			wantErr: true,
		},
		{
			name:    "remote with env rejected",
			cfg:     ServerConfig{Name: "gcal", Transport: TransportStreamableHTTP, URL: "https://example.com/mcp", Env: map[string]string{"A": "B"}},
			wantErr: true,
		},
		{
			name:    "remote missing url rejected",
			cfg:     ServerConfig{Name: "gcal", Transport: TransportStreamableHTTP},
			wantErr: true,
		},
		{
			name:    "remote private host rejected",
			cfg:     ServerConfig{Name: "gcal", Transport: TransportStreamableHTTP, URL: "https://127.0.0.1/mcp"},
			wantErr: true,
		},
		{
			name:    "unsupported transport rejected",
			cfg:     ServerConfig{Name: "gcal", Transport: "sse", Command: "npx"},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateServerConfig(tc.cfg)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestNormalizedAuthRef(t *testing.T) {
	if got := NormalizedAuthRef(ServerConfig{Name: "gcal"}); got != "mcp:gcal" {
		t.Fatalf("expected derived auth ref, got %q", got)
	}
	if got := NormalizedAuthRef(ServerConfig{Name: "gcal", AuthRef: "custom-ref"}); got != "custom-ref" {
		t.Fatalf("expected explicit auth ref to win, got %q", got)
	}
}

// fakeRoundTripper lets tests control the response independent of any real
// dial, for testing sizeLimitedRoundTripper in isolation.
type fakeRoundTripper struct {
	contentType string
	body        string
}

func (f *fakeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{f.contentType}},
		Body:       io.NopCloser(strings.NewReader(f.body)),
		Request:    req,
	}, nil
}

func TestSizeLimitedRoundTripper_CapsJSONResponses(t *testing.T) {
	rt := &sizeLimitedRoundTripper{
		next:     &fakeRoundTripper{contentType: "application/json", body: strings.Repeat("a", 100)},
		maxBytes: 10,
	}
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip error: %v", err)
	}
	_, readErr := io.ReadAll(resp.Body)
	if readErr == nil {
		t.Fatal("expected size-limit error, got nil")
	}
	if !strings.Contains(readErr.Error(), "size limit") {
		t.Fatalf("expected size-limit error, got: %v", readErr)
	}
}

func TestSizeLimitedRoundTripper_AllowsSmallBody(t *testing.T) {
	rt := &sizeLimitedRoundTripper{
		next:     &fakeRoundTripper{contentType: "application/json", body: "ok"},
		maxBytes: 10,
	}
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip error: %v", err)
	}
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		t.Fatalf("unexpected read error: %v", readErr)
	}
	if string(body) != "ok" {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestSizeLimitedRoundTripper_DoesNotCapEventStream(t *testing.T) {
	rt := &sizeLimitedRoundTripper{
		next:     &fakeRoundTripper{contentType: "text/event-stream", body: strings.Repeat("a", 100)},
		maxBytes: 10,
	}
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip error: %v", err)
	}
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		t.Fatalf("SSE response should not be size-capped: %v", readErr)
	}
	if len(body) != 100 {
		t.Fatalf("expected full 100-byte SSE body, got %d bytes", len(body))
	}
}

func TestNewRemoteHTTPClient_BlocksLoopbackDial(t *testing.T) {
	client := newRemoteHTTPClient()
	req, _ := http.NewRequest(http.MethodGet, "https://127.0.0.1:1/x", nil)
	_, err := client.Do(req)
	if err == nil {
		t.Fatal("expected dial to loopback to be blocked")
	}
	if !strings.Contains(err.Error(), "blocked private address") {
		t.Fatalf("expected blocked-private-address error, got: %v", err)
	}
}

func TestNewRemoteHTTPClient_RedirectValidatesTarget(t *testing.T) {
	client := newRemoteHTTPClient()
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	err := client.CheckRedirect(&http.Request{URL: req.URL}, nil)
	if err != nil {
		t.Fatalf("unexpected error validating a same-origin https redirect: %v", err)
	}

	badReq, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if err := client.CheckRedirect(&http.Request{URL: badReq.URL}, nil); err == nil {
		t.Fatal("expected redirect to non-https target to be rejected")
	}
}

func TestIsOAuthReconnectError(t *testing.T) {
	if !isOAuthReconnectError(ErrOAuthCredentialsRequired) {
		t.Fatal("expected credentials-required to be a reconnect error")
	}
	if !isOAuthReconnectError(errors.Join(errors.New("wrap"), ErrOAuthDenied)) {
		t.Fatal("expected wrapped denied error to be a reconnect error")
	}
	if isOAuthReconnectError(errors.New("some other error")) {
		t.Fatal("expected unrelated error not to be a reconnect error")
	}
}
