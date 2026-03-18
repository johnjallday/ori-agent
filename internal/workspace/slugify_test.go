package workspace

import (
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple name", "My Project", "my-project"},
		{"already slug", "my-project", "my-project"},
		{"special characters", "Hello, World! @#$%", "hello-world"},
		{"unicode accents", "café résumé", "cafe-resume"},
		{"leading trailing spaces", "  hello world  ", "hello-world"},
		{"multiple spaces", "hello   world", "hello-world"},
		{"empty string", "", "untitled"},
		{"whitespace only", "   ", "untitled"},
		{"special chars only", "!@#$%^&*()", "untitled"},
		{"numbers", "project 123", "project-123"},
		{"mixed case", "My Cool PROJECT", "my-cool-project"},
		{"hyphens preserved", "my-cool-project", "my-cool-project"},
		{"multiple hyphens collapsed", "my---project", "my-project"},
		{"leading trailing hyphens", "-my-project-", "my-project"},
		{"unicode emoji", "my project 🚀", "my-project"},
		{"long name truncated", "this-is-a-very-long-workspace-name-that-should-be-truncated-at-sixty-four-characters-exactly", "this-is-a-very-long-workspace-name-that-should-be-truncated-at-s"},
		{"dots and underscores", "my.project_name", "my-project-name"},
		{"tabs and newlines", "my\tproject\nname", "my-project-name"},
		{"german umlauts", "über straße", "uber-stra-e"},
		{"single character", "a", "a"},
		{"single hyphen", "-", "untitled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Slugify(tt.input)
			if got != tt.expected {
				t.Errorf("Slugify(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSlugifyMaxLength(t *testing.T) {
	// Generate a string longer than MaxSlugLength
	long := strings.Repeat("a", MaxSlugLength+20)
	got := Slugify(long)
	if len(got) > MaxSlugLength {
		t.Errorf("Slugify produced slug of length %d, want <= %d", len(got), MaxSlugLength)
	}
}
