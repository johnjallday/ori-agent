package pluginapi

import (
	"runtime"
	"strings"
	"testing"
)

func TestToConfigVariables(t *testing.T) {
	yaml := `
name: test-plugin
version: 1.0.0
description: Test plugin
license: MIT
repository: https://github.com/test/test
maintainers:
  - name: Test
    email: test@test.com
platforms:
  - os: darwin
    architectures: [amd64, arm64]

config:
  variables:
    - key: scripts_dir
      name: Scripts Directory
      description: Directory where scripts are stored
      type: dirpath
      required: true
      default_value: "~/Library/Application Support/Scripts"
      placeholder: "~/Library/Application Support/Scripts"
      platform_defaults:
        windows: "%APPDATA%/Scripts"
        linux: "~/.config/Scripts"

    - key: api_key
      name: API Key
      description: Your API key
      type: string
      required: true
      default_value: "{{OS}}_{{ARCH}}"
`

	config, err := readPluginConfig(yaml)
	if err != nil {
		t.Fatalf("readPluginConfig error: %v", err)
	}
	vars := config.ToConfigVariables()

	if len(vars) != 2 {
		t.Fatalf("expected 2 config variables, got %d", len(vars))
	}

	// Test first variable (scripts_dir with platform defaults)
	scriptsDir := vars[0]
	if scriptsDir.Key != "scripts_dir" {
		t.Errorf("expected key 'scripts_dir', got '%s'", scriptsDir.Key)
	}
	if scriptsDir.Type != ConfigTypeDirPath {
		t.Errorf("expected type ConfigTypeDirPath, got '%s'", scriptsDir.Type)
	}
	if !scriptsDir.Required {
		t.Error("expected scripts_dir to be required")
	}

	// Check that default value was expanded and platform-specific value applied
	defaultVal, ok := scriptsDir.DefaultValue.(string)
	if !ok {
		t.Fatalf("expected default value to be string, got %T", scriptsDir.DefaultValue)
	}

	// On darwin, should use default value; on windows/linux, should use platform default
	switch runtime.GOOS {
	case "darwin":
		if !strings.Contains(defaultVal, "Library/Application Support/Scripts") {
			t.Errorf("expected darwin default to contain Library path, got: %s", defaultVal)
		}
	case "windows":
		if !strings.Contains(defaultVal, "Scripts") {
			t.Errorf("expected windows default to contain Scripts, got: %s", defaultVal)
		}
	case "linux":
		if !strings.Contains(defaultVal, ".config/Scripts") {
			t.Errorf("expected linux default to contain .config/Scripts, got: %s", defaultVal)
		}
	}

	// Test second variable (api_key with template expansion)
	apiKey := vars[1]
	if apiKey.Key != "api_key" {
		t.Errorf("expected key 'api_key', got '%s'", apiKey.Key)
	}

	// Check that template variables were expanded
	defaultAPIKey, ok := apiKey.DefaultValue.(string)
	if !ok {
		t.Fatalf("expected default value to be string, got %T", apiKey.DefaultValue)
	}

	expectedValue := runtime.GOOS + "_" + runtime.GOARCH
	if defaultAPIKey != expectedValue {
		t.Errorf("expected template expansion to be '%s', got '%s'", expectedValue, defaultAPIKey)
	}
}

func TestExpandTemplates(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		contains string // substring that should be in result
	}{
		{
			name:     "OS template",
			input:    "{{OS}}",
			contains: runtime.GOOS,
		},
		{
			name:     "ARCH template",
			input:    "{{ARCH}}",
			contains: runtime.GOARCH,
		},
		{
			name:     "USER_HOME template",
			input:    "{{USER_HOME}}/test",
			contains: "/test",
		},
		{
			name:     "tilde expansion",
			input:    "~/Documents",
			contains: "Documents",
		},
		{
			name:     "non-string passthrough",
			input:    42,
			contains: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := expandTemplates(tt.input)

			if tt.name == "non-string passthrough" {
				if result != 42 {
					t.Errorf("expected non-string to pass through unchanged, got %v", result)
				}
				return
			}

			str, ok := result.(string)
			if !ok {
				t.Fatalf("expected string result, got %T", result)
			}

			if tt.contains != "" && !strings.Contains(str, tt.contains) {
				t.Errorf("expected result to contain '%s', got '%s'", tt.contains, str)
			}
		})
	}
}

func TestToMetadata_IncludesTags(t *testing.T) {
	yaml := `
name: test-plugin
version: 1.0.0
description: Test plugin
tags: ["dev_tools", "DevTools", "audio"]
license: MIT
repository: https://github.com/test/test
maintainers:
  - name: Test
    email: test@test.com
platforms:
  - os: darwin
    architectures: [amd64, arm64]
`

	config, err := readPluginConfig(yaml)
	if err != nil {
		t.Fatalf("readPluginConfig error: %v", err)
	}
	meta, err := config.ToMetadata()
	if err != nil {
		t.Fatalf("ToMetadata error: %v", err)
	}
	if meta == nil {
		t.Fatalf("ToMetadata returned nil metadata")
	}
	if len(meta.Tags) != 3 {
		t.Fatalf("expected 3 tags, got %d (%v)", len(meta.Tags), meta.Tags)
	}
}
