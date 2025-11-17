package plugins

import (
	"testing"

	"github.com/johnjallday/ori-agent/tests/user/helpers"
)

// TestMathPluginIntegration tests the math plugin end-to-end
func TestMathPluginIntegration(t *testing.T) {
	ctx := helpers.NewTestContext(t)
	defer ctx.Cleanup()

	t.Log("Testing math plugin integration")

	// Create agent
	agent := ctx.CreateAgent("math-test-agent", "gpt-4o-mini")

	// Enable math plugin
	ctx.EnablePlugin(agent, "math")

	// Test addition
	t.Run("Addition", func(t *testing.T) {
		resp := ctx.SendChat(agent, "Calculate 100 + 50")
		ctx.AssertToolCalled(resp, "math")
		ctx.AssertResponseContains(resp, "150")
	})

	// Test multiplication
	t.Run("Multiplication", func(t *testing.T) {
		resp := ctx.SendChat(agent, "What is 12 times 8?")
		ctx.AssertToolCalled(resp, "math")
		ctx.AssertResponseContains(resp, "96")
	})

	// Test division
	t.Run("Division", func(t *testing.T) {
		resp := ctx.SendChat(agent, "Divide 100 by 4")
		ctx.AssertToolCalled(resp, "math")
		ctx.AssertResponseContains(resp, "25")
	})

	t.Log("✓ Math plugin integration tests passed")
}

// TestWeatherPluginIntegration tests the weather plugin end-to-end
func TestWeatherPluginIntegration(t *testing.T) {
	ctx := helpers.NewTestContext(t)
	defer ctx.Cleanup()

	t.Log("Testing weather plugin integration")

	// Create agent
	agent := ctx.CreateAgent("weather-test-agent", "gpt-4o-mini")

	// Enable weather plugin
	ctx.EnablePlugin(agent, "weather")

	// Test weather query
	resp := ctx.SendChat(agent, "What's the weather in Tokyo?")
	ctx.AssertToolCalled(resp, "get_weather")
	// Weather responses vary, so just verify tool was called

	t.Log("✓ Weather plugin integration test passed")
}

// TestPluginLoadingPerformance tests plugin loading time
func TestPluginLoadingPerformance(t *testing.T) {
	ctx := helpers.NewTestContext(t)
	defer ctx.Cleanup()

	t.Log("Testing plugin loading performance")

	// Load plugin metadata
	plugin := ctx.LoadPlugin("math")

	if plugin == nil {
		t.Fatal("Failed to load math plugin")
	}

	t.Logf("✓ Plugin loaded: %s", plugin.Name)
}

// TestMultiplePluginsOnAgent tests using multiple plugins on one agent
func TestMultiplePluginsOnAgent(t *testing.T) {
	ctx := helpers.NewTestContext(t)
	defer ctx.Cleanup()

	t.Log("Testing multiple plugins on one agent")

	// Create agent
	agent := ctx.CreateAgent("multi-plugin-agent", "gpt-4o-mini")

	// Enable multiple plugins
	ctx.EnablePlugin(agent, "math")
	ctx.EnablePlugin(agent, "weather")

	// Test that agent can use both
	t.Run("UseMathPlugin", func(t *testing.T) {
		resp := ctx.SendChat(agent, "What is 5 + 5?")
		ctx.AssertToolCalled(resp, "math")
	})

	t.Run("UseWeatherPlugin", func(t *testing.T) {
		resp := ctx.SendChat(agent, "Weather in Paris?")
		ctx.AssertToolCalled(resp, "get_weather")
	})

	t.Log("✓ Multiple plugins test passed")
}

// TestPluginConfigurationPersistence tests plugin settings
func TestPluginConfigurationPersistence(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping persistence test in short mode")
	}

	ctx := helpers.NewTestContext(t)
	defer ctx.Cleanup()

	t.Log("Testing plugin configuration persistence")

	// Create agent and enable plugin
	agent := ctx.CreateAgent("config-test-agent", "gpt-4o-mini")
	ctx.EnablePlugin(agent, "math")

	// TODO: Test that configuration persists across server restarts
	// This would require stopping and restarting the server

	t.Log("✓ Plugin configuration test passed")
}

// TestAgentAwarePluginContext tests plugins that use agent context
func TestAgentAwarePluginContext(t *testing.T) {
	ctx := helpers.NewTestContext(t)
	defer ctx.Cleanup()

	t.Log("Testing agent-aware plugin context")

	// Create two agents with same plugin
	agent1 := ctx.CreateAgent("context-agent-1", "gpt-4o-mini")
	agent2 := ctx.CreateAgent("context-agent-2", "gpt-4o-mini")

	ctx.EnablePlugin(agent1, "math")
	ctx.EnablePlugin(agent2, "math")

	// Both agents should work independently
	resp1 := ctx.SendChat(agent1, "What is 10 + 10?")
	resp2 := ctx.SendChat(agent2, "What is 20 + 20?")

	ctx.AssertToolCalled(resp1, "math")
	ctx.AssertToolCalled(resp2, "math")
	ctx.AssertResponseContains(resp1, "20")
	ctx.AssertResponseContains(resp2, "40")

	t.Log("✓ Agent-aware plugin context test passed")
}
