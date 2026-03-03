package chathttp

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestExtractBrowserOpenURLFromArgs(t *testing.T) {
	tests := []struct {
		name string
		args string
		want string
		ok   bool
	}{
		{
			name: "open_url action",
			args: `{"action":"open_url","url":"https://instagram.com"}`,
			want: "https://instagram.com",
			ok:   true,
		},
		{
			name: "browser_navigate adapted payload",
			args: `{"url":"https://instagram.com"}`,
			want: "https://instagram.com",
			ok:   true,
		},
		{
			name: "unsupported action",
			args: `{"action":"click","url":"https://instagram.com"}`,
			ok:   false,
		},
		{
			name: "invalid payload",
			args: `{"action":`,
			ok:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := extractBrowserOpenURLFromArgs(tt.args)
			if ok != tt.ok {
				t.Fatalf("ok mismatch: got %v want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("url mismatch: got %q want %q", got, tt.want)
			}
		})
	}
}

func TestIsTransientBrowserNavigationError(t *testing.T) {
	err := errors.New("### Error Error: page._wrapApiCall: Execution context was destroyed, most likely because of a navigation.")
	if !isTransientBrowserNavigationError(err) {
		t.Fatal("expected transient navigation error to be detected")
	}
	if isTransientBrowserNavigationError(errors.New("selector not found")) {
		t.Fatal("did not expect non-navigation error to be transient")
	}
}

func TestMaybeRecoverBrowserNavigationResult(t *testing.T) {
	err := errors.New("Execution context was destroyed, most likely because of a navigation.")
	recovered, raw := maybeRecoverBrowserNavigationResult("browser", `{"action":"open_url","url":"https://instagram.com"}`, err)
	if !recovered {
		t.Fatal("expected recovery to succeed")
	}

	var payload BrowserResponse
	if decodeErr := json.Unmarshal([]byte(raw), &payload); decodeErr != nil {
		t.Fatalf("failed to decode recovered payload: %v", decodeErr)
	}
	if !payload.Success {
		t.Fatal("expected recovered payload success=true")
	}
	if payload.Action != "open_url" {
		t.Fatalf("expected open_url action, got %q", payload.Action)
	}
	if !strings.Contains(payload.Result, "instagram.com") {
		t.Fatalf("expected recovered message to mention URL, got %q", payload.Result)
	}
}
