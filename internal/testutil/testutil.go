package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// TestServer represents a test HTTP server instance
type TestServer struct {
	Server     *httptest.Server
	Client     *http.Client
	TempDir    string
	Cleanup    func()
	t          *testing.T
}

// NewTestServer creates a new test server instance
func NewTestServer(t *testing.T, handler http.Handler) *TestServer {
	ts := httptest.NewServer(handler)

	// Create temporary directory for test data
	tempDir, err := os.MkdirTemp("", "ori-agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	return &TestServer{
		Server:  ts,
		Client:  ts.Client(),
		TempDir: tempDir,
		t:       t,
		Cleanup: func() {
			ts.Close()
			os.RemoveAll(tempDir)
		},
	}
}

// Get performs a GET request to the test server
func (ts *TestServer) Get(path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	rec := httptest.NewRecorder()
	ts.Server.Config.Handler.ServeHTTP(rec, req)
	return rec
}

// Post performs a POST request to the test server
func (ts *TestServer) Post(path string, body interface{}) *httptest.ResponseRecorder {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		ts.t.Fatalf("Failed to marshal body: %v", err)
	}

	req := httptest.NewRequest("POST", path, bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ts.Server.Config.Handler.ServeHTTP(rec, req)
	return rec
}

// Put performs a PUT request to the test server
func (ts *TestServer) Put(path string, body interface{}) *httptest.ResponseRecorder {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		ts.t.Fatalf("Failed to marshal body: %v", err)
	}

	req := httptest.NewRequest("PUT", path, bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ts.Server.Config.Handler.ServeHTTP(rec, req)
	return rec
}

// Delete performs a DELETE request to the test server
func (ts *TestServer) Delete(path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("DELETE", path, nil)
	rec := httptest.NewRecorder()
	ts.Server.Config.Handler.ServeHTTP(rec, req)
	return rec
}

// WaitForServer waits for the server to be ready
func WaitForServer(url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("server did not become ready within %v", timeout)
		case <-ticker.C:
			resp, err := http.Get(url + "/health")
			if err == nil && resp.StatusCode == http.StatusOK {
				resp.Body.Close()
				return nil
			}
			if resp != nil {
				resp.Body.Close()
			}
		}
	}
}

// AssertStatusCode asserts the HTTP status code
func AssertStatusCode(t *testing.T, expected, actual int) {
	t.Helper()
	if expected != actual {
		t.Errorf("Expected status code %d, got %d", expected, actual)
	}
}

// AssertJSONResponse asserts the response is valid JSON and unmarshals it
func AssertJSONResponse(t *testing.T, rec *httptest.ResponseRecorder, v interface{}) {
	t.Helper()

	if rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Expected Content-Type application/json, got %s", rec.Header().Get("Content-Type"))
	}

	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("Failed to unmarshal response: %v\nBody: %s", err, rec.Body.String())
	}
}

// AssertContains asserts that haystack contains needle
func AssertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !contains(haystack, needle) {
		t.Errorf("Expected %q to contain %q", haystack, needle)
	}
}

// AssertNotContains asserts that haystack does not contain needle
func AssertNotContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if contains(haystack, needle) {
		t.Errorf("Expected %q to not contain %q", haystack, needle)
	}
}

// CreateTempFile creates a temporary file with content
func CreateTempFile(t *testing.T, dir, pattern, content string) string {
	t.Helper()

	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}

	return f.Name()
}

// CreateTempDir creates a temporary directory
func CreateTempDir(t *testing.T, pattern string) string {
	t.Helper()

	dir, err := os.MkdirTemp("", pattern)
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	return dir
}

// SkipIfNoAPIKey skips the test if no API key is set
func SkipIfNoAPIKey(t *testing.T) {
	t.Helper()

	if os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("No API key set - skipping test")
	}
}

// LoadTestData loads test data from a JSON file
func LoadTestData(t *testing.T, filename string, v interface{}) {
	t.Helper()

	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("Failed to read test data file %s: %v", filename, err)
	}

	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("Failed to unmarshal test data: %v", err)
	}
}

// SaveTestData saves test data to a JSON file
func SaveTestData(t *testing.T, filename string, v interface{}) {
	t.Helper()

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal test data: %v", err)
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		t.Fatalf("Failed to write test data file %s: %v", filename, err)
	}
}

// HTTPRequest represents an HTTP request for testing
type HTTPRequest struct {
	Method  string
	Path    string
	Body    io.Reader
	Headers map[string]string
}

// MakeRequest makes an HTTP request and returns the response
func MakeRequest(t *testing.T, client *http.Client, baseURL string, req HTTPRequest) *http.Response {
	t.Helper()

	httpReq, err := http.NewRequest(req.Method, baseURL+req.Path, req.Body)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}

	return resp
}

// ReadJSONResponse reads and unmarshals a JSON response
func ReadJSONResponse(t *testing.T, resp *http.Response, v interface{}) {
	t.Helper()
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("Failed to unmarshal response: %v\nBody: %s", err, string(body))
	}
}

// MockPlugin represents a mock plugin for testing
type MockPlugin struct {
	Name        string
	Description string
	CallFunc    func(ctx context.Context, args string) (string, error)
}

// CreateMockPluginBinary creates a mock plugin binary for testing
func CreateMockPluginBinary(t *testing.T, dir, name string) string {
	t.Helper()

	// Create a simple Go file for the mock plugin
	pluginCode := fmt.Sprintf(`package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("Mock plugin: %s")
	os.Exit(0)
}
`, name)

	codeFile := CreateTempFile(t, dir, name+"-*.go", pluginCode)

	// Note: In a real test, you would compile this
	// For now, we just return the path
	return codeFile
}

// Helper functions

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) &&
		(haystack == needle ||
		 len(haystack) > len(needle) &&
		 indexOf(haystack, needle) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
