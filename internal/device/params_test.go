package device

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"
)

func TestCalculateMaxParams(t *testing.T) {
	tests := []struct {
		name           string
		gpu            *types.GPUInfo
		totalRAM       int64
		expectedParams string
		expectedTier   MemoryTier
	}{
		{
			name:           "No GPU, 4GB RAM",
			gpu:            nil,
			totalRAM:       4 * 1024 * 1024 * 1024,
			expectedParams: "1B", // 50% of 4GB = 2GB
			expectedTier:   TierBasic,
		},
		{
			name:           "No GPU, 16GB RAM",
			gpu:            nil,
			totalRAM:       16 * 1024 * 1024 * 1024,
			expectedParams: "7B", // 50% of 16GB = 8GB
			expectedTier:   TierAdvanced,
		},
		{
			name: "Discrete GPU with 8GB VRAM",
			gpu: &types.GPUInfo{
				Name:       "NVIDIA RTX 4070",
				Vendor:     "NVIDIA",
				VRAM:       8 * 1024 * 1024 * 1024,
				IsDiscrete: true,
			},
			totalRAM:       32 * 1024 * 1024 * 1024,
			expectedParams: "7B",
			expectedTier:   TierAdvanced,
		},
		{
			name: "Discrete GPU with 24GB VRAM",
			gpu: &types.GPUInfo{
				Name:       "NVIDIA RTX 4090",
				Vendor:     "NVIDIA",
				VRAM:       24 * 1024 * 1024 * 1024,
				IsDiscrete: true,
			},
			totalRAM:       64 * 1024 * 1024 * 1024,
			expectedParams: "30B",
			expectedTier:   TierProfessional,
		},
		{
			name: "Apple Silicon M2 Pro with 32GB",
			gpu: &types.GPUInfo{
				Name:           "Apple M2 Pro",
				Vendor:         "Apple",
				IsAppleSilicon: true,
				IsDiscrete:     false,
			},
			totalRAM:       32 * 1024 * 1024 * 1024,
			expectedParams: "20B", // 70% of 32GB = 22.4GB
			expectedTier:   TierProfessional,
		},
		{
			name: "Apple Silicon M2 Max with 64GB",
			gpu: &types.GPUInfo{
				Name:           "Apple M2 Max",
				Vendor:         "Apple",
				IsAppleSilicon: true,
				IsDiscrete:     false,
			},
			totalRAM:       64 * 1024 * 1024 * 1024,
			expectedParams: "70B", // 70% of 64GB = 44.8GB
			expectedTier:   TierEnterprise,
		},
		{
			name: "Integrated Intel GPU with 16GB RAM",
			gpu: &types.GPUInfo{
				Name:       "Intel UHD Graphics",
				Vendor:     "Intel",
				IsDiscrete: false,
			},
			totalRAM:       16 * 1024 * 1024 * 1024,
			expectedParams: "7B", // 50% of 16GB = 8GB
			expectedTier:   TierAdvanced,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateMaxParams(tt.gpu, tt.totalRAM)

			if result.MaxParams != tt.expectedParams {
				t.Errorf("MaxParams = %s, want %s", result.MaxParams, tt.expectedParams)
			}
			if result.Tier != tt.expectedTier {
				t.Errorf("Tier = %s, want %s", result.Tier, tt.expectedTier)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{0, "0 bytes"},
		{1024, "1024 bytes"},
		{1024 * 1024, "1 MB"},
		{1024 * 1024 * 1024, "1 GB"},
		{8 * 1024 * 1024 * 1024, "8 GB"},
		{16 * 1024 * 1024 * 1024, "16 GB"},
		{int64(15.5 * 1024 * 1024 * 1024), "15.5 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := FormatBytes(tt.bytes)
			if result != tt.expected {
				t.Errorf("FormatBytes(%d) = %s, want %s", tt.bytes, result, tt.expected)
			}
		})
	}
}

func TestMemoryTierDescription(t *testing.T) {
	tests := []struct {
		tier     MemoryTier
		contains string
	}{
		{TierBasic, "1B"},
		{TierStandard, "3B"},
		{TierAdvanced, "13B"},
		{TierProfessional, "30B"},
		{TierEnterprise, "70B"},
	}

	for _, tt := range tests {
		t.Run(string(tt.tier), func(t *testing.T) {
			desc := tt.tier.Description()
			if desc == "" {
				t.Error("Description should not be empty")
			}
		})
	}
}

func TestMemoryTierRecommendedModels(t *testing.T) {
	tiers := []MemoryTier{TierBasic, TierStandard, TierAdvanced, TierProfessional, TierEnterprise}

	for _, tier := range tiers {
		t.Run(string(tier), func(t *testing.T) {
			models := tier.RecommendedModels()
			if len(models) == 0 {
				t.Error("RecommendedModels should not be empty")
			}
		})
	}
}
