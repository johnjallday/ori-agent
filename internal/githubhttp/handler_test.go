package githubhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/mcp"
)

func newTestHandler(t *testing.T, handler http.HandlerFunc) (*Handler, *fakeCredentialStore) {
	t.Helper()
	store := withFakeStore(t)
	conn, _ := newFakeGitHub(t, handler)
	return NewHandler(conn, nil), store
}

func do(t *testing.T, h *Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	h.Register(mux)

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response was not JSON: %v (body: %s)", err, rec.Body.String())
	}
	return payload
}

func TestConnectEndpoint_ReturnsIdentityNeverTheToken(t *testing.T) {
	h, store := newTestHandler(t, okUser())

	rec := do(t, h, http.MethodPost, "/api/connections/github/connect", `{"token":"`+testToken+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// The single most important assertion in this file: the token must not
	// appear anywhere in the response, in any field.
	if strings.Contains(rec.Body.String(), testToken) {
		t.Fatalf("the connect response echoed the token: %s", rec.Body.String())
	}

	payload := decode(t, rec)
	if payload["connected"] != true || payload["login"] != "octocat" {
		t.Fatalf("unexpected payload: %v", payload)
	}

	// It really was stored, so the absence above is redaction rather than
	// a silently failed connect.
	if store.byRef[mcp.NormalizedAuthRef(MCPServerConfig())].AccessToken != testToken {
		t.Fatal("expected the token to be stored")
	}
}

func TestConnectEndpoint_RejectsBadToken(t *testing.T) {
	h, store := newTestHandler(t, status(http.StatusUnauthorized))

	rec := do(t, h, http.MethodPost, "/api/connections/github/connect", `{"token":"`+testToken+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	payload := decode(t, rec)
	if payload["error"] != ErrorCategoryInvalidToken {
		t.Fatalf("error = %v, want %q", payload["error"], ErrorCategoryInvalidToken)
	}
	if strings.Contains(rec.Body.String(), testToken) {
		t.Fatalf("error response echoed the token: %s", rec.Body.String())
	}
	if len(store.byRef) != 0 {
		t.Fatal("a rejected token must not be stored")
	}
}

func TestConnectEndpoint_MapsCategoriesToStatusCodes(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    int
	}{
		{"invalid token", status(http.StatusUnauthorized), http.StatusBadRequest},
		{"insufficient scope", status(http.StatusForbidden), http.StatusBadRequest},
		{"rate limited", status(http.StatusTooManyRequests), http.StatusTooManyRequests},
		{"github unavailable", status(http.StatusInternalServerError), http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newTestHandler(t, tc.handler)
			rec := do(t, h, http.MethodPost, "/api/connections/github/connect", `{"token":"`+testToken+`"}`)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestConnectEndpoint_RejectsMalformedBody(t *testing.T) {
	h, _ := newTestHandler(t, okUser())
	rec := do(t, h, http.MethodPost, "/api/connections/github/connect", `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestConnectEndpoint_RejectsNonPost(t *testing.T) {
	h, _ := newTestHandler(t, okUser())
	rec := do(t, h, http.MethodGet, "/api/connections/github/connect", "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestStatusEndpoint_ReportsConnectionWithoutToken(t *testing.T) {
	h, store := newTestHandler(t, okUser())
	authRef := mcp.NormalizedAuthRef(MCPServerConfig())
	store.byRef[authRef] = mcp.RemoteCredential{
		AuthRef:     authRef,
		AccessToken: testToken,
		TokenType:   mcp.StaticBearerTokenType,
	}

	rec := do(t, h, http.MethodGet, "/api/connections/github/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), testToken) {
		t.Fatalf("the status response leaked the token: %s", rec.Body.String())
	}
	payload := decode(t, rec)
	if payload["connected"] != true || payload["login"] != "octocat" {
		t.Fatalf("unexpected payload: %v", payload)
	}
}

func TestStatusEndpoint_ReportsDisconnectedWhenNoToken(t *testing.T) {
	h, _ := newTestHandler(t, okUser())

	rec := do(t, h, http.MethodGet, "/api/connections/github/status", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	payload := decode(t, rec)
	if payload["connected"] != false {
		t.Fatalf("expected connected=false, got %v", payload)
	}
	if payload["error_category"] != ErrorCategoryNotConnected {
		t.Fatalf("error_category = %v, want %q", payload["error_category"], ErrorCategoryNotConnected)
	}
}

func TestDisconnectEndpoint_IsIdempotent(t *testing.T) {
	h, store := newTestHandler(t, okUser())

	if _, err := h.conn.Connect(context.Background(), testToken); err != nil {
		t.Fatalf("Connect error: %v", err)
	}

	for i := range 2 {
		rec := do(t, h, http.MethodPost, "/api/connections/github/disconnect", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("disconnect %d: status = %d, want 200", i+1, rec.Code)
		}
		payload := decode(t, rec)
		if payload["connected"] != false {
			t.Fatalf("disconnect %d: expected connected=false, got %v", i+1, payload)
		}
	}
	if len(store.byRef) != 0 {
		t.Fatal("expected the credential removed")
	}
}

// Every response on this surface carries a credential-adjacent payload and
// must not be cached.
func TestResponses_AreNoStore(t *testing.T) {
	h, _ := newTestHandler(t, okUser())
	for _, route := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/connections/github/status", ""},
		{http.MethodPost, "/api/connections/github/connect", `{"token":"` + testToken + `"}`},
		{http.MethodPost, "/api/connections/github/disconnect", ""},
	} {
		rec := do(t, h, route.method, route.path, route.body)
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("%s: Cache-Control = %q, want no-store", route.path, got)
		}
	}
}

// The guard must actually be applied when one is supplied.
func TestRoutes_RespectGuard(t *testing.T) {
	withFakeStore(t)
	conn, _ := newFakeGitHub(t, okUser())
	h := NewHandler(conn, blockingGuard{})

	mux := http.NewServeMux()
	h.Register(mux)

	for _, path := range []string{
		"/api/connections/github/status",
		"/api/connections/github/connect",
		"/api/connections/github/disconnect",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, strings.NewReader("")))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403 from the guard", path, rec.Code)
		}
	}
}

type blockingGuard struct{}

func (blockingGuard) Wrap(http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "cross-origin", http.StatusForbidden)
	})
}
