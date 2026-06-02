package workspace

import (
	"strings"
	"testing"
)

func TestNormalizeReferenceURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "empty", input: "", want: ""},
		{name: "trimmed", input: "  HTTPS://Example.com/spec?q=1  ", want: "https://Example.com/spec?q=1"},
		{name: "http", input: "http://example.com/path", want: "http://example.com/path"},
		{name: "relative", input: "/docs/spec", wantErr: true},
		{name: "file", input: "file:///tmp/spec", wantErr: true},
		{name: "javascript", input: "javascript:alert(1)", wantErr: true},
		{name: "data", input: "data:text/plain,hello", wantErr: true},
		{name: "credentials", input: "https://user:pass@example.com/spec", wantErr: true},
		{name: "host whitespace", input: "https://exa mple.com/spec", wantErr: true},
		{name: "too long", input: "https://example.com/" + strings.Repeat("a", ReferenceURLMaxLength), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeReferenceURL(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizeReferenceURL(%q) returned nil error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeReferenceURL(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeReferenceURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestReferenceURLAllowlistHost(t *testing.T) {
	got, err := ReferenceURLAllowlistHost("https://example.com:8443/spec")
	if err != nil {
		t.Fatalf("ReferenceURLAllowlistHost: %v", err)
	}
	if got != "example.com:8443" {
		t.Fatalf("host = %q, want example.com:8443", got)
	}
}
