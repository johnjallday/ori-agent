package llm

import (
	"errors"
	"strings"
	"testing"
)

// TestRequireOpenAIDecision exercises the provider-integration gate with
// injected inputs only. It must never dial a live endpoint or require a real
// credential: every case below stays local so this test itself cannot
// contact or bill a provider, regardless of what is in the environment.
func TestRequireOpenAIDecision(t *testing.T) {
	unreachable := errors.New("dial tcp: i/o timeout")

	cases := []struct {
		name       string
		short      bool
		optIn      string
		apiKey     string
		dialErr    error
		wantSkip   bool
		wantReason string
		wantDial   bool
	}{
		{
			name:       "no key",
			short:      false,
			optIn:      "1",
			apiKey:     "",
			wantSkip:   true,
			wantReason: "OPENAI_API_KEY",
			wantDial:   false,
		},
		{
			name:       "key without opt-in",
			short:      false,
			optIn:      "",
			apiKey:     "sk-fake",
			wantSkip:   true,
			wantReason: providerIntegrationOptIn,
			wantDial:   false,
		},
		{
			name:       "short mode with opt-in and key",
			short:      true,
			optIn:      "1",
			apiKey:     "sk-fake",
			wantSkip:   true,
			wantReason: "short mode",
			wantDial:   false,
		},
		{
			name:       "unreachable endpoint",
			short:      false,
			optIn:      "1",
			apiKey:     "sk-fake",
			dialErr:    unreachable,
			wantSkip:   true,
			wantReason: "unreachable",
			wantDial:   true,
		},
		{
			name:     "explicit enabled mode proceeds",
			short:    false,
			optIn:    "1",
			apiKey:   "sk-fake",
			wantSkip: false,
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

			reason := requireOpenAIDecision(tc.short, tc.optIn, tc.apiKey, dial)

			if tc.wantSkip && reason == "" {
				t.Fatal("requireOpenAIDecision() = \"\", want a skip reason")
			}
			if !tc.wantSkip && reason != "" {
				t.Fatalf("requireOpenAIDecision() = %q, want no skip", reason)
			}
			if tc.wantReason != "" && !strings.Contains(reason, tc.wantReason) {
				t.Fatalf("requireOpenAIDecision() = %q, want it to mention %q", reason, tc.wantReason)
			}
			if dialed != tc.wantDial {
				t.Fatalf("dial called = %v, want %v (opt-in must be checked before any reachability probe)", dialed, tc.wantDial)
			}
		})
	}
}
