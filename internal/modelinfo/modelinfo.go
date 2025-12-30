// Package modelinfo provides model metadata including use-case recommendations.
// It embeds curated model use-case mappings and applies regex-based matching
// to provide "good for" recommendations for each model.
package modelinfo

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"time"
)

//go:embed model_use_cases.json
var useCasesJSON []byte

//go:embed model_pricing.json
var pricingJSON []byte

// UseCaseRule represents a regex-based rule for matching model use cases
type UseCaseRule struct {
	Match   string   `json:"match"`
	GoodFor []string `json:"good_for"`
}

// ModelInfo contains enriched metadata about a model
type ModelInfo struct {
	GoodFor []string `json:"good_for"`
}

// Matcher provides model metadata lookups using the embedded use case rules
type Matcher struct {
	rules []compiledRule
	mu    sync.RWMutex
}

type compiledRule struct {
	pattern *regexp.Regexp
	goodFor []string
}

var (
	defaultMatcher *Matcher
	initOnce       sync.Once
)

// DefaultMatcher returns the singleton matcher instance
func DefaultMatcher() *Matcher {
	initOnce.Do(func() {
		defaultMatcher = NewMatcher()
	})
	return defaultMatcher
}

// NewMatcher creates a new Matcher from the embedded use cases JSON
func NewMatcher() *Matcher {
	m := &Matcher{}
	m.loadRules()
	return m
}

func (m *Matcher) loadRules() {
	var rawRules []UseCaseRule
	if err := json.Unmarshal(useCasesJSON, &rawRules); err != nil {
		// If parsing fails, we'll just have no rules
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.rules = make([]compiledRule, 0, len(rawRules))
	for _, rule := range rawRules {
		if rule.Match == "" || len(rule.GoodFor) == 0 {
			continue
		}

		// Compile the regex pattern (case-insensitive)
		pattern, err := regexp.Compile("(?i)" + rule.Match)
		if err != nil {
			continue
		}

		m.rules = append(m.rules, compiledRule{
			pattern: pattern,
			goodFor: rule.GoodFor,
		})
	}
}

// GetModelInfo returns metadata for a model ID
func (m *Matcher) GetModelInfo(modelID string) ModelInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var goodFor []string
	seen := make(map[string]bool)

	for _, rule := range m.rules {
		if rule.pattern.MatchString(modelID) {
			for _, item := range rule.goodFor {
				if !seen[item] {
					seen[item] = true
					goodFor = append(goodFor, item)
				}
			}
		}
	}

	return ModelInfo{
		GoodFor: goodFor,
	}
}

// GetGoodFor is a convenience function to get use cases for a model
func GetGoodFor(modelID string) []string {
	return DefaultMatcher().GetModelInfo(modelID).GoodFor
}

// ModelPricing contains pricing information for models
type ModelPricing struct {
	InputPer1M      float64 `json:"input"`            // Cost per 1M input tokens
	OutputPer1M     float64 `json:"output"`           // Cost per 1M output tokens
	DeprecationDate string  `json:"deprecation_date"` // When the model will be deprecated (YYYY-MM-DD)
}

// pricingEntry is the JSON structure for each model in the pricing file
type pricingEntry struct {
	Input           float64 `json:"input"`
	Output          float64 `json:"output"`
	DeprecationDate string  `json:"deprecation_date,omitempty"`
}

// pricingData holds the loaded pricing information
type pricingData struct {
	Models map[string]pricingEntry `json:"models"`
	Claude map[string]pricingEntry `json:"claude"`
}

var (
	loadedPricing *pricingData
	pricingOnce   sync.Once
)

func loadPricing() *pricingData {
	pricingOnce.Do(func() {
		loadedPricing = &pricingData{
			Models: make(map[string]pricingEntry),
			Claude: make(map[string]pricingEntry),
		}
		if err := json.Unmarshal(pricingJSON, loadedPricing); err != nil {
			// If parsing fails, we'll have empty maps
			return
		}
	})
	return loadedPricing
}

// GetPricing returns pricing information for known models
func GetPricing(modelID string) *ModelPricing {
	pricing := loadPricing()

	// Check exact match first (OpenAI models)
	if p, ok := pricing.Models[modelID]; ok {
		return &ModelPricing{InputPer1M: p.Input, OutputPer1M: p.Output, DeprecationDate: p.DeprecationDate}
	}

	// Check Claude models
	if p, ok := pricing.Claude[modelID]; ok {
		return &ModelPricing{InputPer1M: p.Input, OutputPer1M: p.Output, DeprecationDate: p.DeprecationDate}
	}

	// Try prefix matching for versioned models
	modelLower := strings.ToLower(modelID)

	// Check OpenAI models by prefix (longest match first)
	bestMatch := ""
	var bestPricing *ModelPricing
	for name, p := range pricing.Models {
		if strings.HasPrefix(modelLower, strings.ToLower(name)) && len(name) > len(bestMatch) {
			bestMatch = name
			bestPricing = &ModelPricing{InputPer1M: p.Input, OutputPer1M: p.Output, DeprecationDate: p.DeprecationDate}
		}
		// Also check if model contains the pattern (for dated versions)
		if strings.Contains(modelLower, strings.ToLower(name)) && len(name) > len(bestMatch) {
			bestMatch = name
			bestPricing = &ModelPricing{InputPer1M: p.Input, OutputPer1M: p.Output, DeprecationDate: p.DeprecationDate}
		}
	}
	if bestPricing != nil {
		return bestPricing
	}

	// Check Claude models by prefix
	for name, p := range pricing.Claude {
		if strings.HasPrefix(modelLower, strings.ToLower(name)) && len(name) > len(bestMatch) {
			bestMatch = name
			bestPricing = &ModelPricing{InputPer1M: p.Input, OutputPer1M: p.Output, DeprecationDate: p.DeprecationDate}
		}
		if strings.Contains(modelLower, strings.ToLower(name)) && len(name) > len(bestMatch) {
			bestMatch = name
			bestPricing = &ModelPricing{InputPer1M: p.Input, OutputPer1M: p.Output, DeprecationDate: p.DeprecationDate}
		}
	}
	if bestPricing != nil {
		return bestPricing
	}

	// Local models (Ollama) - no cost
	return nil
}

// IsLegacy returns true if the model is deprecated (past its deprecation date)
func (p *ModelPricing) IsLegacy() bool {
	if p == nil || p.DeprecationDate == "" {
		return false
	}
	// Parse the deprecation date and compare with current date
	// Format is YYYY-MM-DD
	today := strings.Split(currentDate(), "T")[0]
	return p.DeprecationDate <= today
}

// IsDeprecatingSoon returns true if the model will be deprecated within 90 days
func (p *ModelPricing) IsDeprecatingSoon() bool {
	if p == nil || p.DeprecationDate == "" {
		return false
	}
	// Simple check: if deprecation year is current year or next, it's "soon"
	// This is a rough approximation without complex date parsing
	return !p.IsLegacy() && p.DeprecationDate != ""
}

func currentDate() string {
	return time.Now().Format("2006-01-02")
}

// GetOpenAIModels returns a list of OpenAI model IDs from the pricing data.
// This provides a curated list of models without requiring an API call.
func GetOpenAIModels() []string {
	pricing := loadPricing()
	models := make([]string, 0, len(pricing.Models))
	for name := range pricing.Models {
		// Filter to only include chat-capable models
		if isChatModel(name) {
			models = append(models, name)
		}
	}
	return models
}

// GetClaudeModels returns a list of Claude model IDs from the pricing data.
// This provides a curated list of models without requiring an API call.
func GetClaudeModels() []string {
	pricing := loadPricing()
	models := make([]string, 0, len(pricing.Claude))
	for name := range pricing.Claude {
		models = append(models, name)
	}
	return models
}

// isChatModel checks if a model ID is a chat model (not embedding, tts, etc.)
func isChatModel(modelID string) bool {
	// Exclude non-chat models
	excludePrefixes := []string{
		"text-embedding",
		"text-moderation",
		"whisper",
		"tts-",
		"dall-e",
		"davinci",
		"babbage",
		"omni-moderation",
		"ft:", // fine-tuned models
	}
	modelLower := strings.ToLower(modelID)
	for _, prefix := range excludePrefixes {
		if strings.HasPrefix(modelLower, prefix) {
			return false
		}
	}

	// Include GPT and O-series models
	chatPrefixes := []string{
		"gpt-3.5",
		"gpt-4",
		"gpt-5",
		"o1",
		"o3",
		"o4",
		"chatgpt",
		"codex",
		"computer-use",
	}
	for _, prefix := range chatPrefixes {
		if strings.HasPrefix(modelLower, prefix) {
			return true
		}
	}

	return false
}

// FormatPricing returns a human-readable pricing string
func FormatPricing(pricing *ModelPricing) string {
	if pricing == nil {
		return "Local (Free)"
	}
	return formatPrice(pricing.InputPer1M) + " in / " + formatPrice(pricing.OutputPer1M) + " out"
}

func formatPrice(price float64) string {
	if price < 1.0 {
		// Show cents for prices under $1
		cents := price * 100
		if cents == float64(int(cents)) {
			return itoa(int(cents)) + "¢"
		}
		return formatDecimal(cents, 1) + "¢"
	}
	// Show dollars for prices $1 and up
	if price == float64(int(price)) {
		return "$" + itoa(int(price))
	}
	return "$" + formatDecimal(price, 2)
}

func formatDecimal(f float64, precision int) string {
	intPart := int(f)
	fracPart := f - float64(intPart)

	result := itoa(intPart) + "."
	for i := 0; i < precision; i++ {
		fracPart *= 10
		digit := int(fracPart)
		result += string(rune('0' + digit))
		fracPart -= float64(digit)
	}
	// Trim trailing zeros
	result = strings.TrimRight(result, "0")
	result = strings.TrimRight(result, ".")
	return result
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if negative {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
