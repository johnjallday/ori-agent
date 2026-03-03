package chathttp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnjallday/ori-agent/internal/client"
)

func TestIsTrustedChatRequestSource_NoBrowserHeadersAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://localhost:8765/api/chat", bytes.NewBufferString(`{}`))
	if !isTrustedChatRequestSource(req) {
		t.Fatalf("expected request without origin/referer headers to be allowed")
	}
}

func TestIsTrustedChatRequestSource_MatchingOriginAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://localhost:8765/api/chat", bytes.NewBufferString(`{}`))
	req.Header.Set("Origin", "http://localhost:8765")
	if !isTrustedChatRequestSource(req) {
		t.Fatalf("expected matching origin to be allowed")
	}
}

func TestIsTrustedChatRequestSource_LoopbackAliasAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://localhost:8765/api/chat", bytes.NewBufferString(`{}`))
	req.Header.Set("Origin", "http://127.0.0.1:8765")
	if !isTrustedChatRequestSource(req) {
		t.Fatalf("expected loopback alias origin to be allowed")
	}
}

func TestIsTrustedChatRequestSource_CrossOriginRejected(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://localhost:8765/api/chat", bytes.NewBufferString(`{}`))
	req.Header.Set("Origin", "http://evil.test")
	if isTrustedChatRequestSource(req) {
		t.Fatalf("expected cross-origin request to be rejected")
	}
}

func TestIsTrustedChatRequestSource_MalformedOriginRejected(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://localhost:8765/api/chat", bytes.NewBufferString(`{}`))
	req.Header.Set("Origin", "://invalid")
	if isTrustedChatRequestSource(req) {
		t.Fatalf("expected malformed origin to be rejected")
	}
}

func TestIsTrustedChatRequestSource_RefererMismatchRejected(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://localhost:8765/api/chat", bytes.NewBufferString(`{}`))
	req.Header.Set("Referer", "http://evil.test/path")
	if isTrustedChatRequestSource(req) {
		t.Fatalf("expected mismatched referer to be rejected")
	}
}

func TestChatHandler_BlocksCrossOriginRequest(t *testing.T) {
	h := NewHandler(nil, client.NewFactory(""))
	body := []byte(`{"question":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "http://localhost:8765/api/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.test")
	rr := httptest.NewRecorder()

	h.ChatHandler(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-origin request, got %d", rr.Code)
	}
}
