package featureflags

import (
	"os"
	"testing"
)

func TestEconomyEnabledDefaultsOnAndRespectsTheEnvironment(t *testing.T) {
	tests := []struct {
		name  string
		unset bool
		raw   string
		want  bool
	}{
		{name: "unset defaults enabled", unset: true, want: true},
		{name: "empty defaults enabled", raw: "", want: true},
		{name: "false disables", raw: "false", want: false},
		{name: "off disables", raw: "off", want: false},
		{name: "zero disables", raw: "0", want: false},
		{name: "true enables", raw: "true", want: true},
		{name: "unknown defaults enabled", raw: "maybe", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setenv first even when testing the unset case: it is what
			// registers the cleanup that restores whatever the developer's own
			// shell had exported.
			t.Setenv(envEconomyEnabled, tt.raw)
			if tt.unset {
				if err := os.Unsetenv(envEconomyEnabled); err != nil {
					t.Fatalf("unset %s: %v", envEconomyEnabled, err)
				}
			}
			if got := EconomyEnabled(); got != tt.want {
				t.Fatalf("EconomyEnabled() with %s=%q (unset=%v) = %v, want %v",
					envEconomyEnabled, tt.raw, tt.unset, got, tt.want)
			}
		})
	}
}

func TestParseBoolDefaultTrue(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "empty defaults enabled", raw: "", want: true},
		{name: "true", raw: "true", want: true},
		{name: "yes", raw: "yes", want: true},
		{name: "on", raw: "on", want: true},
		{name: "enabled", raw: "enabled", want: true},
		{name: "one", raw: "1", want: true},
		{name: "false", raw: "false", want: false},
		{name: "no", raw: "no", want: false},
		{name: "off", raw: "off", want: false},
		{name: "disabled", raw: "disabled", want: false},
		{name: "zero", raw: "0", want: false},
		{name: "unknown defaults enabled", raw: "unexpected", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseBoolDefaultTrue(tt.raw); got != tt.want {
				t.Fatalf("parseBoolDefaultTrue(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}
