package featureflags

import "testing"

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

func TestWorkspaceFloatingAssistantEnabledDefaultsOn(t *testing.T) {
	t.Setenv(envWorkspaceFloatingAssistantEnabled, "")

	if !WorkspaceFloatingAssistantEnabled() {
		t.Fatal("WorkspaceFloatingAssistantEnabled() = false, want true by default")
	}
}

func TestWorkspaceFloatingAssistantEnabledCanBeDisabled(t *testing.T) {
	t.Setenv(envWorkspaceFloatingAssistantEnabled, "false")

	if WorkspaceFloatingAssistantEnabled() {
		t.Fatal("WorkspaceFloatingAssistantEnabled() = true, want false when disabled")
	}
}
