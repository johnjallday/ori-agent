package llm

import (
	"errors"
	"strings"
	"testing"
)

// TestRequireProviderDecision exercises the provider-integration gate with
// injected inputs only. It must never dial a live endpoint or require a real
// credential: every case below stays local so this test itself cannot
// contact or bill a provider, regardless of what is in the environment.
func TestRequireProviderDecision(t *testing.T) {
	unreachable := errors.New("dial tcp: i/o timeout")

	cases := []struct {
		name       string
		short      bool
		optIn      string
		keyEnv     string
		apiKey     string
		endpoint   string
		dialErr    error
		wantSkip   bool
		wantReason string
		wantDial   bool
	}{
		{
			name:       "no openai key",
			optIn:      "1",
			keyEnv:     "OPENAI_API_KEY",
			endpoint:   "api.openai.com:443",
			wantSkip:   true,
			wantReason: "OPENAI_API_KEY",
		},
		{
			name:       "no anthropic key names its own variable",
			optIn:      "1",
			keyEnv:     "ANTHROPIC_API_KEY",
			endpoint:   "api.anthropic.com:443",
			wantSkip:   true,
			wantReason: "ANTHROPIC_API_KEY",
		},
		{
			name:       "key without opt-in",
			optIn:      "",
			keyEnv:     "OPENAI_API_KEY",
			apiKey:     "sk-fake",
			endpoint:   "api.openai.com:443",
			wantSkip:   true,
			wantReason: providerIntegrationOptIn,
		},
		{
			name:       "short mode with opt-in and key",
			short:      true,
			optIn:      "1",
			keyEnv:     "OPENAI_API_KEY",
			apiKey:     "sk-fake",
			endpoint:   "api.openai.com:443",
			wantSkip:   true,
			wantReason: "short mode",
		},
		{
			name:       "unreachable endpoint is named",
			optIn:      "1",
			keyEnv:     "ANTHROPIC_API_KEY",
			apiKey:     "sk-fake",
			endpoint:   "api.anthropic.com:443",
			dialErr:    unreachable,
			wantSkip:   true,
			wantReason: "api.anthropic.com:443 unreachable",
			wantDial:   true,
		},
		{
			name:     "explicit enabled mode proceeds",
			optIn:    "1",
			keyEnv:   "OPENAI_API_KEY",
			apiKey:   "sk-fake",
			endpoint: "api.openai.com:443",
			wantDial: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dialed := false
			dial := func() error {
				dialed = true
				return tc.dialErr
			}

			reason := requireProviderDecision(tc.short, tc.optIn, tc.keyEnv, tc.apiKey, tc.endpoint, dial)

			if tc.wantSkip && reason == "" {
				t.Fatal("requireProviderDecision() = \"\", want a skip reason")
			}
			if !tc.wantSkip && reason != "" {
				t.Fatalf("requireProviderDecision() = %q, want no skip", reason)
			}
			if tc.wantReason != "" && !strings.Contains(reason, tc.wantReason) {
				t.Fatalf("requireProviderDecision() = %q, want it to mention %q", reason, tc.wantReason)
			}
			if dialed != tc.wantDial {
				t.Fatalf("dial called = %v, want %v (opt-in must be checked before any reachability probe)", dialed, tc.wantDial)
			}
		})
	}
}

// TestSelectIntegrationProvider pins the vendor choice: an explicit
// ORI_TEST_PROVIDER wins, otherwise an Anthropic key selects claude and the
// absence of one keeps the historical openai default.
func TestSelectIntegrationProvider(t *testing.T) {
	cases := []struct {
		name         string
		explicit     string
		anthropicKey string
		want         string
	}{
		{name: "nothing set keeps openai", want: "openai"},
		{name: "anthropic key selects claude", anthropicKey: "sk-ant-fake", want: "claude"},
		{name: "explicit provider wins over the key", explicit: "openai", anthropicKey: "sk-ant-fake", want: "openai"},
		{name: "explicit unknown provider is passed through for the caller to reject", explicit: "gemini", want: "gemini"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := selectIntegrationProvider(tc.explicit, tc.anthropicKey); got != tc.want {
				t.Fatalf("selectIntegrationProvider(%q, key set=%v) = %q, want %q", tc.explicit, tc.anthropicKey != "", got, tc.want)
			}
		})
	}
	for name := range integrationProviders {
		spec := integrationProviders[name]
		if spec.name != name || spec.keyEnv == "" || spec.endpoint == "" || spec.model == "" || spec.build == nil {
			t.Fatalf("integration provider %q is incompletely described: %+v", name, spec)
		}
	}
}
