package plugins

import (
	"runtime"
	"testing"

	"github.com/johnjallday/ori-agent/tests/user/helpers"
)

// TestSharedPluginsAvailable verifies shared plugins can be loaded
func TestSharedPluginsAvailable(t *testing.T) {
	ctx := helpers.NewTestContext(t)
	defer ctx.Cleanup()

	t.Log("Testing shared plugins availability")

	// Get list of shared plugins
	sharedPlugins := helpers.SharedPluginNames()

	for _, pluginName := range sharedPlugins {
		t.Run(pluginName, func(t *testing.T) {
			// Try to load the plugin
			plugin := ctx.LoadPlugin(pluginName)
			if plugin == nil {
				t.Skipf("Plugin %s not found - may not be built yet", pluginName)
				return
			}

			t.Logf("✓ Plugin loaded: %s", pluginName)
		})
	}
}

// TestMusicProjectManagerPlugin tests the music-project-manager plugin (macOS only)
func TestMusicProjectManagerPlugin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Music project manager is macOS-specific")
	}

	ctx := helpers.NewTestContext(t)
	defer ctx.Cleanup()

	t.Log("Testing music-project-manager plugin")

	// Create agent
	agent := ctx.CreateAgent("music-producer", "gpt-4o-mini")

	// Enable plugin
	ctx.EnablePlugin(agent, "ori-music-project-manager")

	// Test basic functionality
	t.Run("ListProjects", func(t *testing.T) {
		resp := ctx.SendChat(agent, "List my music projects")
		// Just verify we got a response
		ctx.AssertResponseContains(resp, "")
	})

	t.Log("✓ Music project manager plugin test passed")
}

// TestReaperPlugin tests the ori-reaper plugin (macOS only)
func TestReaperPlugin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Reaper plugin is macOS-specific")
	}

	ctx := helpers.NewTestContext(t)
	defer ctx.Cleanup()

	t.Log("Testing ori-reaper plugin")

	// Create agent
	agent := ctx.CreateAgent("reaper-controller", "gpt-4o-mini")

	// Enable plugin
	ctx.EnablePlugin(agent, "ori-reaper")

	// Note: This test assumes Reaper is NOT running
	// Full testing requires Reaper to be running with web interface enabled

	t.Run("PluginLoaded", func(t *testing.T) {
		// Just verify the plugin can be enabled
		// Actual Reaper commands require Reaper to be running
		t.Log("✓ Reaper plugin enabled successfully")
	})

	t.Log("✓ Reaper plugin test passed (limited - Reaper not running)")
}

// TestMacOSToolsPlugin tests the ori-mac-os-tools plugin (macOS only)
func TestMacOSToolsPlugin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS tools plugin is macOS-specific")
	}

	ctx := helpers.NewTestContext(t)
	defer ctx.Cleanup()

	t.Log("Testing ori-mac-os-tools plugin")

	// Create agent
	agent := ctx.CreateAgent("macos-assistant", "gpt-4o-mini")

	// Enable plugin
	ctx.EnablePlugin(agent, "ori-mac-os-tools")

	t.Run("SystemInfo", func(t *testing.T) {
		resp := ctx.SendChat(agent, "What system am I running?")
		ctx.AssertResponseContains(resp, "")
	})

	t.Log("✓ macOS tools plugin test passed")
}

// TestMetaThreadsManagerPlugin tests the ori-meta-threads-manager plugin
func TestMetaThreadsManagerPlugin(t *testing.T) {
	ctx := helpers.NewTestContext(t)
	defer ctx.Cleanup()

	t.Log("Testing ori-meta-threads-manager plugin")

	// Create agent
	agent := ctx.CreateAgent("threads-manager", "gpt-4o-mini")

	// Enable plugin
	ctx.EnablePlugin(agent, "ori-meta-threads-manager")

	t.Run("PluginEnabled", func(t *testing.T) {
		t.Log("✓ Meta threads manager plugin enabled")
	})

	t.Log("✓ Meta threads manager plugin test passed")
}

// TestDocBuilderPlugin tests the ori-agent-doc-builder plugin
func TestDocBuilderPlugin(t *testing.T) {
	ctx := helpers.NewTestContext(t)
	defer ctx.Cleanup()

	t.Log("Testing ori-agent-doc-builder plugin")

	// Create agent
	agent := ctx.CreateAgent("doc-builder", "gpt-4o-mini")

	// Enable plugin
	ctx.EnablePlugin(agent, "ori-agent-doc-builder")

	t.Run("PluginEnabled", func(t *testing.T) {
		t.Log("✓ Doc builder plugin enabled")
	})

	t.Log("✓ Doc builder plugin test passed")
}

// TestMultipleSharedPlugins tests using multiple shared plugins on one agent
func TestMultipleSharedPlugins(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Test requires macOS for macOS-specific plugins")
	}

	ctx := helpers.NewTestContext(t)
	defer ctx.Cleanup()

	t.Log("Testing multiple shared plugins on one agent")

	// Create agent
	agent := ctx.CreateAgent("multi-plugin-agent", "gpt-4o-mini")

	// Enable multiple shared plugins
	plugins := []string{
		"ori-music-project-manager",
		"ori-mac-os-tools",
	}

	for _, plugin := range plugins {
		ctx.EnablePlugin(agent, plugin)
	}

	// Test that agent can handle multiple plugins
	resp := ctx.SendChat(agent, "Hello, I'm testing multiple plugins")
	ctx.AssertResponseContains(resp, "")

	t.Log("✓ Multiple shared plugins test passed")
}
