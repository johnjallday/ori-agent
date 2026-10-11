package publicread

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func response(req *http.Request, status int, contentType, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: req}
}

func TestPublicReadValidatesExactRequestAndSendsNoCredentialsOrEnvironment(t *testing.T) {
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "OPENAI_API_KEY"} {
		t.Setenv(key, "PRIVATE_ENV_SENTINEL")
	}
	calls := 0
	client := &Client{transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodGet || req.URL.String() != "https://example.com/docs?lang=en" || len(req.Header) != 1 || req.Header.Get("User-Agent") != "ori-agent/public-research" {
			t.Fatalf("outbound request: %s %s %v", req.Method, req.URL, req.Header)
		}
		resp := response(req, 200, "text/plain; charset=utf-8", "public instructions")
		resp.Header.Set("Set-Cookie", "private=must-not-be-forwarded")
		resp.Header.Set("ETag", `"v1"`)
		return resp, nil
	})}
	result, err := client.Read(context.Background(), Request{URL: "https://example.com/docs?lang=en", ContentTypes: []string{"text/plain"}})
	if err != nil || calls != 1 || result.URL != "https://example.com/docs?lang=en" || string(result.Body) != "public instructions" || result.ReadAt.IsZero() || len(result.Hash) != 64 || result.ETag != `"v1"` {
		t.Fatalf("response: %+v %v", result, err)
	}
	if NewClient().transport.(*http.Transport).Proxy != nil {
		t.Fatal("production reader inherits environment proxy")
	}
}

func TestPublicReadRejectsUnsafeURLBeforeNetwork(t *testing.T) {
	client := &Client{transport: transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unsafe URL reached transport")
		return nil, errors.New("not reached")
	})}
	for _, target := range []string{"file:///tmp/private", "https://name:password@example.com/doc", "http://127.0.0.1/doc", "http://192.0.2.1/doc", "http://100.64.0.1/doc", "http://[::ffff:127.0.0.1]/doc", "https://example.com:8443/doc", "https://example.com:/doc", "https://foo.localhost/doc", "https://example.com/?%61pi_key=secret", "https://example.com/?a;sig=untrusted", "https://example.com/?a;x-amz-signature=untrusted", "https://example.com/?q=api_key%3Dsecret", "https://example.com/sk-privatecredential123456", "https://example.com/\n", "example.com", strings.Repeat("x", 2001)} {
		if _, err := client.Read(context.Background(), Request{URL: target, ContentTypes: []string{"text/plain"}}); !errors.Is(err, ErrUnsafeURL) {
			t.Fatalf("unsafe %q: %v", target, err)
		}
	}
}

func TestPublicReadFollowsOnlyThreeSameOriginDocumentRedirectsWithoutCookies(t *testing.T) {
	for _, target := range []string{"http://127.0.0.1/private", "https://other.example.com/docs", "http://example.com/docs", "https://user:secret@example.com/docs", "https://example.com:8443/docs"} {
		calls := 0
		client := &Client{transport: transportFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			if calls > 1 {
				t.Fatal("unsafe redirect reached transport")
			}
			resp := response(req, 302, "text/plain", "")
			resp.Header.Set("Location", target)
			return resp, nil
		})}
		if _, err := client.Read(context.Background(), Request{URL: "https://example.com/start", ContentTypes: []string{"text/plain"}, SameOriginRedirects: true}); err == nil {
			t.Fatalf("unsafe redirect allowed: %s", target)
		}
	}
	calls := 0
	client := &Client{transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		for _, key := range []string{"Cookie", "Authorization", "Referer"} {
			if req.Header.Get(key) != "" {
				t.Fatalf("redirect forwarded %s", key)
			}
		}
		if calls <= 3 {
			resp := response(req, 302, "text/plain", "")
			resp.Header.Set("Location", fmt.Sprintf("/step%d", calls))
			resp.Header.Set("Set-Cookie", "secret=value")
			return resp, nil
		}
		return response(req, 200, "text/plain", "readable page"), nil
	})}
	if got, err := client.Read(context.Background(), Request{URL: "https://example.com/start", ContentTypes: []string{"text/plain"}, SameOriginRedirects: true}); err != nil || calls != 4 || got.URL != "https://example.com/step3" {
		t.Fatalf("safe bounded redirects: %+v %d %v", got, calls, err)
	}
	calls = 0
	client.transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		resp := response(req, 302, "text/plain", "")
		resp.Header.Set("Location", fmt.Sprintf("/step%d", calls))
		return resp, nil
	})
	if _, err := client.Read(context.Background(), Request{URL: "https://example.com/start", ContentTypes: []string{"text/plain"}, SameOriginRedirects: true}); err == nil || calls != 4 {
		t.Fatalf("redirect cap: %d %v", calls, err)
	}
	calls = 0
	if _, err := client.Read(context.Background(), Request{URL: "https://example.com/start", ContentTypes: []string{"application/json"}}); err == nil || calls != 1 {
		t.Fatalf("catalog redirect changed exact egress: %d %v", calls, err)
	}
}

func TestPublicReadBoundsHTTPTypesBytesCancellationAndPrivateDiagnostics(t *testing.T) {
	for _, fixture := range []struct {
		status            int
		contentType, body string
		length            int64
	}{
		{404, "text/plain", "PRIVATE_SERVER_SENTINEL", 0},
		{200, "application/octet-stream", "binary", 0},
		{200, "application/pdf", "%PDF-binary", 0},
		{200, "text/plain", strings.Repeat("x", MaxResponseBytes+1), -1},
		{200, "text/plain", "not read", MaxResponseBytes + 1},
	} {
		client := &Client{transport: transportFunc(func(req *http.Request) (*http.Response, error) {
			resp := response(req, fixture.status, fixture.contentType, fixture.body)
			resp.ContentLength = fixture.length
			return resp, nil
		})}
		got, err := client.Read(context.Background(), Request{URL: "https://example.com/docs", ContentTypes: []string{"text/plain"}})
		if !errors.Is(err, ErrUnavailable) || len(got.Body) != 0 || got.Hash != "" || !got.ReadAt.IsZero() {
			t.Fatalf("failed response became evidence: %+v %v", got, err)
		}
	}
	client := &Client{transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, errors.New("PRIVATE_DNS_SENTINEL /private/path")
	})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := client.Read(ctx, Request{URL: "https://example.com/docs", ContentTypes: []string{"text/plain"}}); !errors.Is(err, ErrUnavailable) || len(got.Body) != 0 || strings.Contains(err.Error(), "PRIVATE_") {
		t.Fatalf("cancel/diagnostic leaked: %+v %v", got, err)
	}
}
