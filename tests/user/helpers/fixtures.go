package helpers

import (
	"fmt"
	"math/rand"
	"time"
)

// Test fixture helpers for generating test data

func init() {
	rand.Seed(time.Now().UnixNano())
}

// RandomAgentName generates a random agent name for testing
func RandomAgentName() string {
	prefixes := []string{"test", "demo", "sample", "trial", "dev"}
	suffixes := []string{"agent", "bot", "assistant", "helper", "ai"}

	prefix := prefixes[rand.Intn(len(prefixes))]
	suffix := suffixes[rand.Intn(len(suffixes))]
	id := rand.Intn(9999)

	return fmt.Sprintf("%s-%s-%d", prefix, suffix, id)
}

// RandomModel returns a random test-appropriate model
func RandomModel() string {
	models := []string{
		"gpt-4o-mini",
		"gpt-4o",
		"gpt-3.5-turbo",
	}
	return models[rand.Intn(len(models))]
}

// SampleChatMessages returns a set of sample chat messages for testing
func SampleChatMessages() []string {
	return []string{
		"Hello! How are you?",
		"What can you do?",
		"Tell me a joke.",
		"What is the weather like?",
		"Can you help me with math?",
	}
}

// MathQuestions returns math questions for plugin testing
func MathQuestions() []struct {
	Question string
	Answer   string
} {
	return []struct {
		Question string
		Answer   string
	}{
		{"What is 5 + 3?", "8"},
		{"Calculate 10 times 5", "50"},
		{"What is 100 divided by 4?", "25"},
		{"Subtract 15 from 30", "15"},
		{"What is 12 + 8?", "20"},
	}
}

// WeatherQueries returns weather-related queries
func WeatherQueries() []string {
	return []string{
		"What's the weather in Tokyo?",
		"How's the weather in New York?",
		"Tell me the weather in London",
		"What's the temperature in Paris?",
		"Is it raining in Seattle?",
	}
}

// PluginNames returns common plugin names for testing
func PluginNames() []string {
	return []string{
		// Built-in plugins
		"math",
		"weather",
		"result-handler",
		// Shared plugins (from ../plugins)
		"ori-music-project-manager",
		"ori-reaper",
		"ori-mac-os-tools",
		"ori-meta-threads-manager",
		"ori-agent-doc-builder",
	}
}

// SharedPluginNames returns plugins from ../plugins directory
func SharedPluginNames() []string {
	return []string{
		"ori-music-project-manager",
		"ori-reaper",
		"ori-mac-os-tools",
		"ori-meta-threads-manager",
		"ori-agent-doc-builder",
	}
}

// BuiltInPluginNames returns built-in example plugins
func BuiltInPluginNames() []string {
	return []string{
		"math",
		"weather",
		"result-handler",
	}
}

// AgentConfigurations returns sample agent configurations
func AgentConfigurations() []struct {
	Name        string
	Model       string
	Description string
	Plugins     []string
} {
	return []struct {
		Name        string
		Model       string
		Description string
		Plugins     []string
	}{
		{
			Name:        "math-helper",
			Model:       "gpt-4o-mini",
			Description: "An agent specialized in mathematics",
			Plugins:     []string{"math"},
		},
		{
			Name:        "weather-bot",
			Model:       "gpt-4o-mini",
			Description: "An agent for weather information",
			Plugins:     []string{"weather"},
		},
		{
			Name:        "multi-tool",
			Model:       "gpt-4o",
			Description: "An agent with multiple capabilities",
			Plugins:     []string{"math", "weather"},
		},
	}
}

// TestScenarios returns common test scenarios
func TestScenarios() []struct {
	Name        string
	Description string
	Steps       []string
} {
	return []struct {
		Name        string
		Description string
		Steps       []string
	}{
		{
			Name:        "Basic Chat",
			Description: "Test basic chat functionality without tools",
			Steps: []string{
				"Create agent",
				"Send simple message",
				"Verify response received",
			},
		},
		{
			Name:        "Tool Usage",
			Description: "Test agent using plugins/tools",
			Steps: []string{
				"Create agent",
				"Enable plugin",
				"Send message requiring tool",
				"Verify tool was called",
				"Verify correct response",
			},
		},
		{
			Name:        "Multi-turn Conversation",
			Description: "Test conversation context retention",
			Steps: []string{
				"Create agent",
				"Send initial message with context",
				"Send follow-up referencing previous message",
				"Verify agent remembers context",
			},
		},
	}
}

// ErrorScenarios returns scenarios for error testing
func ErrorScenarios() []struct {
	Name        string
	Description string
	Trigger     string
	Expected    string
} {
	return []struct {
		Name        string
		Description string
		Trigger     string
		Expected    string
	}{
		{
			Name:        "Invalid Plugin",
			Description: "Enable non-existent plugin",
			Trigger:     "Enable plugin 'nonexistent'",
			Expected:    "Plugin not found error",
		},
		{
			Name:        "Missing API Key",
			Description: "Attempt to use LLM without API key",
			Trigger:     "Unset API keys and send message",
			Expected:    "API key error",
		},
	}
}

// PerformanceTargets returns performance benchmarks
func PerformanceTargets() struct {
	ServerStartTime   time.Duration
	PluginLoadTime    time.Duration
	ChatResponseTime  time.Duration
	AgentCreationTime time.Duration
} {
	return struct {
		ServerStartTime   time.Duration
		PluginLoadTime    time.Duration
		ChatResponseTime  time.Duration
		AgentCreationTime time.Duration
	}{
		ServerStartTime:   10 * time.Second,
		PluginLoadTime:    2 * time.Second,
		ChatResponseTime:  30 * time.Second,
		AgentCreationTime: 1 * time.Second,
	}
}
