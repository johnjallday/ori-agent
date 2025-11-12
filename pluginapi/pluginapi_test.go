package pluginapi

import (
	"testing"
)

// TestAgentContext verifies that AgentContext can be created with all fields including location
func TestAgentContext(t *testing.T) {
	ctx := AgentContext{
		Name:            "test-agent",
		ConfigPath:      "/path/to/config.json",
		SettingsPath:    "/path/to/settings.json",
		AgentDir:        "/path/to/agent/",
		CurrentLocation: "Home",
	}

	if ctx.Name != "test-agent" {
		t.Errorf("Expected Name to be 'test-agent', got '%s'", ctx.Name)
	}

	if ctx.ConfigPath != "/path/to/config.json" {
		t.Errorf("Expected ConfigPath to be '/path/to/config.json', got '%s'", ctx.ConfigPath)
	}

	if ctx.SettingsPath != "/path/to/settings.json" {
		t.Errorf("Expected SettingsPath to be '/path/to/settings.json', got '%s'", ctx.SettingsPath)
	}

	if ctx.AgentDir != "/path/to/agent/" {
		t.Errorf("Expected AgentDir to be '/path/to/agent/', got '%s'", ctx.AgentDir)
	}

	if ctx.CurrentLocation != "Home" {
		t.Errorf("Expected CurrentLocation to be 'Home', got '%s'", ctx.CurrentLocation)
	}
}

// TestAgentContextWithEmptyLocation verifies that AgentContext works with empty location
func TestAgentContextWithEmptyLocation(t *testing.T) {
	ctx := AgentContext{
		Name:            "test-agent",
		ConfigPath:      "/path/to/config.json",
		SettingsPath:    "/path/to/settings.json",
		AgentDir:        "/path/to/agent/",
		CurrentLocation: "",
	}

	if ctx.CurrentLocation != "" {
		t.Errorf("Expected CurrentLocation to be empty, got '%s'", ctx.CurrentLocation)
	}
}
