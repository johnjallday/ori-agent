package http

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseJSONBody(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantOK     bool
		wantStatus int
	}{
		{
			name:   "valid JSON",
			body:   `{"name": "test"}`,
			wantOK: true,
		},
		{
			name:       "empty body",
			body:       "",
			wantOK:     false,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid JSON",
			body:       `{invalid}`,
			wantOK:     false,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewBufferString(tt.body))
			w := httptest.NewRecorder()

			var data struct {
				Name string `json:"name"`
			}

			ok := ParseJSONBody(w, req, &data)

			if ok != tt.wantOK {
				t.Errorf("ParseJSONBody() = %v, want %v", ok, tt.wantOK)
			}

			if !tt.wantOK && w.Code != tt.wantStatus {
				t.Errorf("status code = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestRequireMethod(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		required   string
		wantOK     bool
		wantStatus int
	}{
		{
			name:     "matching method",
			method:   http.MethodPost,
			required: http.MethodPost,
			wantOK:   true,
		},
		{
			name:       "non-matching method",
			method:     http.MethodGet,
			required:   http.MethodPost,
			wantOK:     false,
			wantStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/test", nil)
			w := httptest.NewRecorder()

			ok := RequireMethod(w, req, tt.required)

			if ok != tt.wantOK {
				t.Errorf("RequireMethod() = %v, want %v", ok, tt.wantOK)
			}

			if !tt.wantOK && w.Code != tt.wantStatus {
				t.Errorf("status code = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestRequireMethods(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		allowed []string
		wantOK  bool
	}{
		{
			name:    "method in list",
			method:  http.MethodGet,
			allowed: []string{http.MethodGet, http.MethodPost},
			wantOK:  true,
		},
		{
			name:    "method not in list",
			method:  http.MethodDelete,
			allowed: []string{http.MethodGet, http.MethodPost},
			wantOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/test", nil)
			w := httptest.NewRecorder()

			ok := RequireMethods(w, req, tt.allowed...)

			if ok != tt.wantOK {
				t.Errorf("RequireMethods() = %v, want %v", ok, tt.wantOK)
			}
		})
	}
}

func TestGetQueryParam(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test?name=foo&empty=", nil)

	tests := []struct {
		key    string
		defVal string
		want   string
	}{
		{"name", "default", "foo"},
		{"missing", "default", "default"},
		{"empty", "default", "default"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := GetQueryParam(req, tt.key, tt.defVal)
			if got != tt.want {
				t.Errorf("GetQueryParam(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestRequireQueryParam(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		param      string
		wantValue  string
		wantStatus int
	}{
		{
			name:      "param present",
			url:       "/test?name=foo",
			param:     "name",
			wantValue: "foo",
		},
		{
			name:       "param missing",
			url:        "/test",
			param:      "name",
			wantValue:  "",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			w := httptest.NewRecorder()

			got := RequireQueryParam(w, req, tt.param)

			if got != tt.wantValue {
				t.Errorf("RequireQueryParam() = %q, want %q", got, tt.wantValue)
			}

			if tt.wantValue == "" && w.Code != tt.wantStatus {
				t.Errorf("status code = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestParseJSONBody_SizeLimit(t *testing.T) {
	// Create a body larger than MaxJSONBodySize (1 MB)
	largeBody := make([]byte, MaxJSONBodySize+1)
	for i := range largeBody {
		largeBody[i] = 'a'
	}

	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(largeBody))
	w := httptest.NewRecorder()

	var data struct {
		Name string `json:"name"`
	}

	ok := ParseJSONBody(w, req, &data)

	if ok {
		t.Error("ParseJSONBody() should fail for oversized body")
	}

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestSecurityHeaders(t *testing.T) {
	handler := SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		path           string
		expectedHeader string
		expectedValue  string
	}{
		{"/api/test", "X-Content-Type-Options", "nosniff"},
		{"/api/test", "X-Frame-Options", "DENY"},
		{"/api/test", "X-XSS-Protection", "1; mode=block"},
		{"/api/test", "Referrer-Policy", "strict-origin-when-cross-origin"},
		{"/api/test", "Cache-Control", "no-store"},
		{"/page", "X-Content-Type-Options", "nosniff"},
	}

	for _, tt := range tests {
		t.Run(tt.path+"_"+tt.expectedHeader, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			got := w.Header().Get(tt.expectedHeader)
			if got != tt.expectedValue {
				t.Errorf("Header %s = %q, want %q", tt.expectedHeader, got, tt.expectedValue)
			}
		})
	}
}

func TestSecurityHeaders_NoCacheControlForNonAPI(t *testing.T) {
	handler := SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/page", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Non-API paths should not have Cache-Control set by SecurityHeaders
	got := w.Header().Get("Cache-Control")
	if got == "no-store" {
		t.Error("Cache-Control should not be 'no-store' for non-API paths")
	}
}
