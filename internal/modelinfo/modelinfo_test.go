package modelinfo

import (
	"testing"
)

func TestGetGoodFor(t *testing.T) {
	tests := []struct {
		modelID     string
		wantMatches []string // At least one of these should be present
	}{
		{"gpt-4o", []string{"general-purpose high quality chat"}},
		{"gpt-4o-mini", []string{"low latency"}},
		{"o1-preview", []string{"complex reasoning"}},
		{"claude-3-opus-20240229", []string{"complex reasoning"}},
		{"claude-3-sonnet-20240229", []string{"balanced performance"}},
		{"claude-3-haiku-20240307", []string{"fast responses"}},
		{"llama3:70b", []string{"high-quality local"}},
		{"llama3:8b", []string{"fast local chat"}},
		{"mistral:latest", []string{"fast local chat"}},
		{"unknown-model", nil}, // No matches expected
	}

	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			got := GetGoodFor(tt.modelID)

			if tt.wantMatches == nil {
				if len(got) != 0 {
					t.Errorf("GetGoodFor(%q) = %v, want empty", tt.modelID, got)
				}
				return
			}

			// Check that at least one expected match is present
			found := false
			for _, want := range tt.wantMatches {
				for _, g := range got {
					if containsSubstring(g, want) {
						found = true
						break
					}
				}
				if found {
					break
				}
			}

			if !found {
				t.Errorf("GetGoodFor(%q) = %v, want one of %v", tt.modelID, got, tt.wantMatches)
			}
		})
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || contains(s, substr))
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestGetPricing(t *testing.T) {
	tests := []struct {
		modelID string
		wantNil bool
	}{
		{"gpt-4o", false},
		{"gpt-4o-mini", false},
		{"gpt-4o-2024-11-20", false}, // Dated version should match
		{"claude-3-opus-20240229", false},
		{"claude-3-haiku-20240307", false},
		{"o1", false},
		{"o1-mini", false},
		{"llama3:8b", true}, // Local model, no pricing
		{"unknown-model", true},
	}

	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			got := GetPricing(tt.modelID)

			if tt.wantNil {
				if got != nil {
					t.Errorf("GetPricing(%q) = %v, want nil", tt.modelID, got)
				}
				return
			}

			if got == nil {
				t.Errorf("GetPricing(%q) = nil, want pricing info", tt.modelID)
				return
			}

			// Just verify we got some pricing (exact values from JSON)
			if got.InputPer1M <= 0 {
				t.Errorf("GetPricing(%q).InputPer1M = %v, want > 0", tt.modelID, got.InputPer1M)
			}
		})
	}
}

func TestFormatPricing(t *testing.T) {
	tests := []struct {
		pricing *ModelPricing
		want    string
	}{
		{nil, "Local (Free)"},
		{&ModelPricing{InputPer1M: 0.15, OutputPer1M: 0.60}, "15¢ in / 60¢ out"},
		{&ModelPricing{InputPer1M: 2.50, OutputPer1M: 10.00}, "$2.5 in / $10 out"},
		{&ModelPricing{InputPer1M: 15.00, OutputPer1M: 75.00}, "$15 in / $75 out"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := FormatPricing(tt.pricing)
			if got != tt.want {
				t.Errorf("FormatPricing(%v) = %q, want %q", tt.pricing, got, tt.want)
			}
		})
	}
}
