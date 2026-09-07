package resetfixture

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// HTTPServer owns its loopback listener. It cannot wrap a running server or a
// browser base URL. The test must wire the handler from this fixture's stores.
type HTTPServer struct {
	server *httptest.Server
	client *http.Client
	mu     sync.RWMutex
	closed bool
}

// StartHTTP allocates a new listener on an OS-assigned port, never a free-port
// probe followed by reuseExistingServer. Register store cleanup before this call
// so requests drain before stores close and the sandbox is removed.
func (f *Fixture) StartHTTP(t testing.TB, handler http.Handler) *HTTPServer {
	t.Helper()
	server := httptest.NewServer(handler)
	transport := &http.Transport{Proxy: nil}
	s := &HTTPServer{
		server: server,
		client: &http.Client{
			Transport: transport, Timeout: 5 * time.Second,
			// Even same-origin redirects are returned for explicit assertions.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	t.Cleanup(func() {
		s.Close()
		transport.CloseIdleConnections()
	})
	return s
}

// URL is derived solely from the listener owned by this test.
func (s *HTTPServer) URL() string { return s.server.URL }

// Close drains requests; it is safe to call again during test cleanup.
func (s *HTTPServer) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		s.server.Close()
	}
}

// Do accepts only a root-relative path, never a URL or alternate authority.
// It does not use the shared browser base URL, proxies, or follow redirects.
func (s *HTTPServer) Do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, fmt.Errorf("reset fixture server is closed")
	}
	u, err := url.Parse(path)
	if err != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") ||
		strings.Contains(path, "\\") || u.IsAbs() || u.Host != "" || u.Fragment != "" {
		return nil, fmt.Errorf("reset fixture requests require a root-relative path")
	}
	req, err := http.NewRequestWithContext(ctx, method, s.URL()+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	return s.client.Do(req)
}
