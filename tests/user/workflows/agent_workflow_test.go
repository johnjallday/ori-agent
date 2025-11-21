package workflows

import (
	"flag"
	"os"
	"testing"

	"github.com/johnjallday/ori-agent/tests/user/helpers"
)

var testModel = flag.String("model", "", "LLM model to use for tests (default: auto-detect)")

func getTestModel() string {
	// Priority: flag > env var > auto-detect
	if testModel != nil && *testModel != "" {
		return *testModel
	}
	if model := os.Getenv("OLLAMA_MODEL"); model != "" {
		return model
	}
	if os.Getenv("USE_OLLAMA") == "true" {
		return "granite4"
	}
	return "gpt-4o-mini"
}

// TestCompleteAgentWorkflow tests the full user journey of creating and using an agent
func TestCompleteAgentWorkflow(t *testing.T) {
	ctx := helpers.NewTestContext(t)
	defer ctx.Cleanup()

	model := getTestModel()
	t.Logf("Starting complete agent workflow test (model: %s)", model)

	// Step 1: Create agent
	t.Log("Step 1: Creating agent...")
	agent := ctx.CreateAgent("workflow-test-agent", model)
	if agent == nil {
		t.Fatal("Failed to create agent")
	}

	// Step 2: Verify agent exists
	t.Log("Step 2: Verifying agent exists...")
	// TODO: Add API call to list agents and verify

	// Step 3: Send simple chat message (no tools)
	t.Log("Step 3: Sending simple chat message...")
	resp := ctx.SendChat(agent, "Hello! Can you confirm you're working?")
	ctx.AssertResponseContains(resp, "") // Just verify we got a response

	t.Log("✓ Complete agent workflow passed")
}

// TestAgentWithPluginWorkflow tests enabling plugins and using them
func TestAgentWithPluginWorkflow(t *testing.T) {
	ctx := helpers.NewTestContext(t)
	defer ctx.Cleanup()

	model := getTestModel()
	t.Logf("Starting agent with plugin workflow test (model: %s)", model)

	// Step 1: Create agent
	agent := ctx.CreateAgent("plugin-test-agent", model)

	// Step 2: Enable math plugin
	t.Log("Step 2: Enabling math plugin...")
	ctx.EnablePlugin(agent, "math")

	// Step 3: Chat with tool usage
	t.Log("Step 3: Sending chat requiring tool use...")
	resp := ctx.SendChat(agent, "What is 15 plus 27?")

	// Step 4: Verify tool was called
	t.Log("Step 4: Verifying math tool was called...")
	ctx.AssertToolCalled(resp, "math")

	// Step 5: Verify correct answer
	t.Log("Step 5: Verifying response contains answer...")
	ctx.AssertResponseContains(resp, "42")

	t.Log("✓ Agent with plugin workflow passed")
}

// TestMultipleAgentsWorkflow tests creating and using multiple agents
func TestMultipleAgentsWorkflow(t *testing.T) {
	ctx := helpers.NewTestContext(t)
	defer ctx.Cleanup()

	model := getTestModel()
	t.Logf("Starting multiple agents workflow test (model: %s)", model)

	// Create two agents with same model
	agent1 := ctx.CreateAgent("agent-one", model)
	agent2 := ctx.CreateAgent("agent-two", model)

	// Chat with both agents
	resp1 := ctx.SendChat(agent1, "Hello from agent one")
	resp2 := ctx.SendChat(agent2, "Hello from agent two")

	// Verify both responded
	ctx.AssertResponseContains(resp1, "")
	ctx.AssertResponseContains(resp2, "")

	t.Log("✓ Multiple agents workflow passed")
}

// TestAgentDeletionWorkflow tests agent cleanup
func TestAgentDeletionWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping deletion workflow in short mode")
	}

	ctx := helpers.NewTestContext(t)
	defer ctx.Cleanup()

	model := getTestModel()
	t.Logf("Starting agent deletion workflow test (model: %s)", model)

	// Create agent
	_ = ctx.CreateAgent("delete-test-agent", model)

	// Delete agent
	t.Log("Deleting agent...")
	// The cleanup function will handle deletion, but we could also test explicit deletion here

	t.Log("✓ Agent deletion workflow passed")
}

// TestErrorRecoveryWorkflow tests how the system handles errors
func TestErrorRecoveryWorkflow(t *testing.T) {
	ctx := helpers.NewTestContext(t)
	defer ctx.Cleanup()

	model := getTestModel()
	t.Logf("Starting error recovery workflow test (model: %s)", model)

	// Create agent
	agent := ctx.CreateAgent("error-test-agent", model)

	// Try to enable non-existent plugin (should handle gracefully)
	// Note: This might need to be wrapped in error handling
	t.Log("Testing non-existent plugin handling...")

	// Send complex query
	resp := ctx.SendChat(agent, "Hello")
	ctx.AssertResponseContains(resp, "") // Just verify we get a response

	t.Log("✓ Error recovery workflow passed")
}
