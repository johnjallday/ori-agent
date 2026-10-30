package helpers

import "testing"

// clearProviderEnv removes every variable the selection reads so each case
// starts from the same empty environment. t.Setenv restores them afterwards.
func clearProviderEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{TestProviderEnv, TestModelEnv, "USE_OLLAMA", "OLLAMA_MODEL", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
		t.Setenv(name, "")
	}
	previousProvider, previousModel := *TestProvider, *TestModel
	*TestProvider, *TestModel = "", ""
	t.Cleanup(func() { *TestProvider, *TestModel = previousProvider, previousModel })
}

func TestGetTestProvider(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		flag string
		want string
	}{
		{name: "nothing set falls back to openai", want: "openai"},
		{name: "anthropic key alone selects claude", env: map[string]string{"ANTHROPIC_API_KEY": "x"}, want: "claude"},
		{name: "anthropic key wins over openai key", env: map[string]string{"ANTHROPIC_API_KEY": "x", "OPENAI_API_KEY": "y"}, want: "claude"},
		{name: "openai key alone selects openai", env: map[string]string{"OPENAI_API_KEY": "y"}, want: "openai"},
		{name: "USE_OLLAMA beats keys", env: map[string]string{"USE_OLLAMA": "true", "ANTHROPIC_API_KEY": "x"}, want: "ollama"},
		{name: "ORI_TEST_PROVIDER beats everything in the environment", env: map[string]string{TestProviderEnv: "gemini", "ANTHROPIC_API_KEY": "x", "USE_OLLAMA": "true"}, want: "gemini"},
		{name: "flag beats the environment", env: map[string]string{TestProviderEnv: "gemini"}, flag: "openai", want: "openai"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearProviderEnv(t)
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			*TestProvider = tc.flag
			if got := GetTestProvider(); got != tc.want {
				t.Fatalf("GetTestProvider() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGetTestModel(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		flag string
		want string
	}{
		{name: "openai default", want: DefaultOpenAITestModel},
		{name: "claude default follows the provider", env: map[string]string{"ANTHROPIC_API_KEY": "x"}, want: DefaultClaudeTestModel},
		{name: "ollama default", env: map[string]string{"USE_OLLAMA": "true"}, want: DefaultOllamaTestModel},
		{name: "OLLAMA_MODEL applies to ollama", env: map[string]string{"USE_OLLAMA": "true", "OLLAMA_MODEL": "llama3.1"}, want: "llama3.1"},
		{name: "OLLAMA_MODEL is ignored for other providers", env: map[string]string{"OLLAMA_MODEL": "llama3.1", "ANTHROPIC_API_KEY": "x"}, want: DefaultClaudeTestModel},
		{name: "ORI_TEST_MODEL overrides the default", env: map[string]string{"ANTHROPIC_API_KEY": "x", TestModelEnv: "claude-sonnet-5-5"}, want: "claude-sonnet-5-5"},
		{name: "flag beats the environment", env: map[string]string{TestModelEnv: "claude-sonnet-5-5"}, flag: "gpt-5-nano", want: "gpt-5-nano"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearProviderEnv(t)
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			*TestModel = tc.flag
			if got := GetTestModel(); got != tc.want {
				t.Fatalf("GetTestModel() = %q, want %q", got, tc.want)
			}
		})
	}
}
