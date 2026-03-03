package chathttp

import "testing"

func TestIsCodexProviderOrModel(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		want     bool
	}{
		{
			name:     "provider is codex",
			provider: "codex",
			model:    "",
			want:     true,
		},
		{
			name:     "provider is codex mixed case",
			provider: "CoDeX",
			model:    "",
			want:     true,
		},
		{
			name:     "model contains codex family name",
			provider: "",
			model:    "gpt-5.3-codex",
			want:     true,
		},
		{
			name:     "non-codex provider and model",
			provider: "openai",
			model:    "gpt-4.1",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCodexProviderOrModel(tt.provider, tt.model)
			if got != tt.want {
				t.Fatalf("isCodexProviderOrModel(%q, %q) = %v, want %v", tt.provider, tt.model, got, tt.want)
			}
		})
	}
}
