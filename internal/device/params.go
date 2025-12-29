package device

import (
	"fmt"

	"github.com/johnjallday/ori-agent/internal/types"
)

// MemoryTier represents the memory capability tier
type MemoryTier string

const (
	TierBasic        MemoryTier = "Basic"        // <4GB - Can run 1B models
	TierStandard     MemoryTier = "Standard"     // 4-7GB - Can run up to 3B models
	TierAdvanced     MemoryTier = "Advanced"     // 8-15GB - Can run up to 13B models
	TierProfessional MemoryTier = "Professional" // 16-31GB - Can run up to 30B models
	TierEnterprise   MemoryTier = "Enterprise"   // 32GB+ - Can run 70B+ models
)

// ModelParams represents recommended model parameter sizes
type ModelParams struct {
	MaxParams   string     // Maximum parameter size (e.g., "7B", "13B", "70B")
	Tier        MemoryTier // Memory tier classification
	UsableBytes int64      // Usable memory for models in bytes
}

// CalculateMaxParams determines the maximum model parameter size based on hardware
// Logic:
// - Discrete GPU: use VRAM directly
// - Apple Silicon: use 70% of unified RAM (shared with GPU)
// - CPU-only (integrated GPU): use 50% of RAM
func CalculateMaxParams(gpu *types.GPUInfo, totalRAM int64) ModelParams {
	var usableBytes int64

	if gpu != nil {
		if gpu.IsDiscrete && gpu.VRAM > 0 {
			// Discrete GPU - use VRAM
			usableBytes = gpu.VRAM
		} else if gpu.IsAppleSilicon {
			// Apple Silicon - unified memory, use 70% for ML
			usableBytes = int64(float64(totalRAM) * 0.70)
		} else {
			// Integrated GPU - use 50% of system RAM
			usableBytes = int64(float64(totalRAM) * 0.50)
		}
	} else {
		// No GPU detected - CPU only, use 50% of RAM
		usableBytes = int64(float64(totalRAM) * 0.50)
	}

	return calculateFromBytes(usableBytes)
}

// calculateFromBytes converts usable memory to model params and tier
func calculateFromBytes(usableBytes int64) ModelParams {
	usableGB := float64(usableBytes) / float64(GB)

	var maxParams string
	var tier MemoryTier

	switch {
	case usableGB < 4:
		maxParams = "1B"
		tier = TierBasic
	case usableGB < 8:
		maxParams = "3B"
		tier = TierStandard
	case usableGB < 12:
		maxParams = "7B"
		tier = TierAdvanced
	case usableGB < 16:
		maxParams = "13B"
		tier = TierAdvanced
	case usableGB < 24:
		maxParams = "20B"
		tier = TierProfessional
	case usableGB < 32:
		maxParams = "30B"
		tier = TierProfessional
	case usableGB < 48:
		maxParams = "70B"
		tier = TierEnterprise
	default:
		maxParams = "70B+"
		tier = TierEnterprise
	}

	return ModelParams{
		MaxParams:   maxParams,
		Tier:        tier,
		UsableBytes: usableBytes,
	}
}

// FormatBytes formats bytes as a human-readable string (e.g., "16 GB")
func FormatBytes(bytes int64) string {
	gb := float64(bytes) / float64(GB)
	if gb >= 1 {
		if gb == float64(int64(gb)) {
			return fmt.Sprintf("%d GB", int64(gb))
		}
		return fmt.Sprintf("%.1f GB", gb)
	}

	mb := float64(bytes) / float64(MB)
	if mb >= 1 {
		return fmt.Sprintf("%.0f MB", mb)
	}

	return fmt.Sprintf("%d bytes", bytes)
}

// TierDescription returns a human-readable description of the memory tier
func (t MemoryTier) Description() string {
	switch t {
	case TierBasic:
		return "Suitable for small models (1B parameters)"
	case TierStandard:
		return "Suitable for lightweight models (up to 3B parameters)"
	case TierAdvanced:
		return "Suitable for mid-size models (up to 13B parameters)"
	case TierProfessional:
		return "Suitable for large models (up to 30B parameters)"
	case TierEnterprise:
		return "Suitable for the largest models (70B+ parameters)"
	default:
		return "Unknown tier"
	}
}

// RecommendedModels returns a list of recommended Ollama models for this tier
func (t MemoryTier) RecommendedModels() []string {
	switch t {
	case TierBasic:
		return []string{"tinyllama", "phi"}
	case TierStandard:
		return []string{"phi3", "gemma:2b", "llama3.2:1b", "qwen2.5:3b"}
	case TierAdvanced:
		return []string{"llama3.2", "mistral", "gemma2", "qwen2.5:7b", "codellama:7b"}
	case TierProfessional:
		return []string{"llama3.1:8b", "codellama:13b", "qwen2.5:14b", "deepseek-coder:6.7b"}
	case TierEnterprise:
		return []string{"llama3.1:70b", "qwen2.5:72b", "codellama:70b", "mixtral:8x7b"}
	default:
		return []string{}
	}
}
