package llm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PricingModel defines the cost structure for a model
type PricingModel struct {
	ModelName string `json:"model_name"`
	Provider  string `json:"provider"`
	// Cost per million tokens
	InputCostPerMillion  float64 `json:"input_cost_per_million"`
	OutputCostPerMillion float64 `json:"output_cost_per_million"`
	Currency             string  `json:"currency"` // e.g., "USD"
}

// UsageRecord represents a single API call's usage and cost
type UsageRecord struct {
	Timestamp        time.Time `json:"timestamp"`
	Provider         string    `json:"provider"`
	Model            string    `json:"model"`
	AgentName        string    `json:"agent_name"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	Cost             float64   `json:"cost"`
	Currency         string    `json:"currency"`
	RequestID        string    `json:"request_id,omitempty"`
}

// UsageStats provides aggregated usage statistics
type UsageStats struct {
	TotalRequests int                      `json:"total_requests"`
	TotalTokens   int                      `json:"total_tokens"`
	TotalCost     float64                  `json:"total_cost"`
	Currency      string                   `json:"currency"`
	ByProvider    map[string]ProviderStats `json:"by_provider"`
	ByAgent       map[string]AgentStats    `json:"by_agent"`
	ByModel       map[string]ModelStats    `json:"by_model"`
	RecentRecords []UsageRecord            `json:"recent_records,omitempty"`
	TimeRange     TimeRange                `json:"time_range"`
}

// ProviderStats tracks stats per provider
type ProviderStats struct {
	Provider            string  `json:"provider"`
	Requests            int     `json:"requests"`
	TotalTokens         int     `json:"total_tokens"`
	TotalCost           float64 `json:"total_cost"`
	AvgTokensPerRequest int     `json:"avg_tokens_per_request"`
}

// AgentStats tracks stats per agent
type AgentStats struct {
	AgentName   string  `json:"agent_name"`
	Requests    int     `json:"requests"`
	TotalTokens int     `json:"total_tokens"`
	TotalCost   float64 `json:"total_cost"`
}

// ModelStats tracks stats per model
type ModelStats struct {
	Model       string  `json:"model"`
	Provider    string  `json:"provider"`
	Requests    int     `json:"requests"`
	TotalTokens int     `json:"total_tokens"`
	TotalCost   float64 `json:"total_cost"`
}

// TimeRange represents a time period for stats
type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// CostTracker tracks usage and calculates costs
type CostTracker struct {
	pricingModels map[string]PricingModel // key: "provider:model"
	records       []UsageRecord
	mu            sync.RWMutex
	dataFile      string
	maxRecords    int // Maximum records to keep in memory
}

// NewCostTracker creates a new cost tracker
func NewCostTracker(dataDir string) *CostTracker {
	ct := &CostTracker{
		pricingModels: make(map[string]PricingModel),
		records:       make([]UsageRecord, 0),
		dataFile:      filepath.Join(dataDir, "usage_records.json"),
		maxRecords:    10000, // Keep last 10k records in memory
	}

	// Initialize default pricing models
	ct.initializePricingModels()

	// Load existing records
	ct.loadRecords()

	return ct
}

// initializePricingModels sets up default pricing for supported models
func (ct *CostTracker) initializePricingModels() {
	// OpenAI Pricing (as of 2024)
	ct.addPricingModel(PricingModel{
		ModelName:            "gpt-4o",
		Provider:             "openai",
		InputCostPerMillion:  2.50,  // $2.50 per 1M input tokens
		OutputCostPerMillion: 10.00, // $10.00 per 1M output tokens
		Currency:             "USD",
	})

	ct.addPricingModel(PricingModel{
		ModelName:            "gpt-4o-mini",
		Provider:             "openai",
		InputCostPerMillion:  0.15, // $0.15 per 1M input tokens
		OutputCostPerMillion: 0.60, // $0.60 per 1M output tokens
		Currency:             "USD",
	})

	ct.addPricingModel(PricingModel{
		ModelName:            "gpt-4-turbo",
		Provider:             "openai",
		InputCostPerMillion:  10.00, // $10 per 1M input tokens
		OutputCostPerMillion: 30.00, // $30 per 1M output tokens
		Currency:             "USD",
	})

	ct.addPricingModel(PricingModel{
		ModelName:            "gpt-4",
		Provider:             "openai",
		InputCostPerMillion:  30.00, // $30 per 1M input tokens
		OutputCostPerMillion: 60.00, // $60 per 1M output tokens
		Currency:             "USD",
	})

	ct.addPricingModel(PricingModel{
		ModelName:            "gpt-3.5-turbo",
		Provider:             "openai",
		InputCostPerMillion:  0.50, // $0.50 per 1M input tokens
		OutputCostPerMillion: 1.50, // $1.50 per 1M output tokens
		Currency:             "USD",
	})

	// Claude Pricing (Anthropic)
	ct.addPricingModel(PricingModel{
		ModelName:            "claude-3-5-sonnet-20241022",
		Provider:             "claude",
		InputCostPerMillion:  3.00,  // $3 per 1M input tokens
		OutputCostPerMillion: 15.00, // $15 per 1M output tokens
		Currency:             "USD",
	})

	ct.addPricingModel(PricingModel{
		ModelName:            "claude-3-opus-20240229",
		Provider:             "claude",
		InputCostPerMillion:  15.00, // $15 per 1M input tokens
		OutputCostPerMillion: 75.00, // $75 per 1M output tokens
		Currency:             "USD",
	})

	ct.addPricingModel(PricingModel{
		ModelName:            "claude-3-sonnet-20240229",
		Provider:             "claude",
		InputCostPerMillion:  3.00,  // $3 per 1M input tokens
		OutputCostPerMillion: 15.00, // $15 per 1M output tokens
		Currency:             "USD",
	})

	ct.addPricingModel(PricingModel{
		ModelName:            "claude-3-haiku-20240307",
		Provider:             "claude",
		InputCostPerMillion:  0.25, // $0.25 per 1M input tokens
		OutputCostPerMillion: 1.25, // $1.25 per 1M output tokens
		Currency:             "USD",
	})

	// Ollama models (free but track tokens)
	ct.addPricingModel(PricingModel{
		ModelName:            "llama3.2",
		Provider:             "ollama",
		InputCostPerMillion:  0.0,
		OutputCostPerMillion: 0.0,
		Currency:             "USD",
	})

	ct.addPricingModel(PricingModel{
		ModelName:            "mistral",
		Provider:             "ollama",
		InputCostPerMillion:  0.0,
		OutputCostPerMillion: 0.0,
		Currency:             "USD",
	})

	// Generic fallback for unknown models
	ct.addPricingModel(PricingModel{
		ModelName:            "default",
		Provider:             "unknown",
		InputCostPerMillion:  1.00,
		OutputCostPerMillion: 2.00,
		Currency:             "USD",
	})
}

// addPricingModel adds a pricing model to the tracker
func (ct *CostTracker) addPricingModel(pm PricingModel) {
	key := fmt.Sprintf("%s:%s", pm.Provider, pm.ModelName)
	ct.pricingModels[key] = pm
}

// TrackUsage records usage from a chat response
func (ct *CostTracker) TrackUsage(provider, model, agentName string, usage Usage, requestID string) error {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	// Calculate cost
	cost, currency := ct.calculateCost(provider, model, usage)

	// Create record
	record := UsageRecord{
		Timestamp:        time.Now(),
		Provider:         provider,
		Model:            model,
		AgentName:        agentName,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
		Cost:             cost,
		Currency:         currency,
		RequestID:        requestID,
	}

	// Add to records
	ct.records = append(ct.records, record)

	// Trim old records if exceeding max
	if len(ct.records) > ct.maxRecords {
		ct.records = ct.records[len(ct.records)-ct.maxRecords:]
	}

	// Make a copy of records for async save to avoid race condition
	recordsCopy := make([]UsageRecord, len(ct.records))
	copy(recordsCopy, ct.records)

	// Save asynchronously with copied data
	go ct.saveRecordsCopy(recordsCopy)

	return nil
}

// calculateCost calculates the cost for a usage record
func (ct *CostTracker) calculateCost(provider, model string, usage Usage) (float64, string) {
	// Try exact match first
	key := fmt.Sprintf("%s:%s", provider, model)
	pm, found := ct.pricingModels[key]

	if !found {
		// Try provider-level default
		key = fmt.Sprintf("%s:default", provider)
		pm, found = ct.pricingModels[key]
	}

	if !found {
		// Use generic default
		pm = ct.pricingModels["unknown:default"]
	}

	// Calculate cost (pricing is per million tokens)
	inputCost := float64(usage.PromptTokens) * pm.InputCostPerMillion / 1000000.0
	outputCost := float64(usage.CompletionTokens) * pm.OutputCostPerMillion / 1000000.0
	totalCost := inputCost + outputCost

	return totalCost, pm.Currency
}

// GetStats returns usage statistics for a time range
func (ct *CostTracker) GetStats(start, end time.Time) UsageStats {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	stats := UsageStats{
		ByProvider:    make(map[string]ProviderStats),
		ByAgent:       make(map[string]AgentStats),
		ByModel:       make(map[string]ModelStats),
		Currency:      "USD",
		TimeRange:     TimeRange{Start: start, End: end},
		RecentRecords: make([]UsageRecord, 0),
	}

	// Filter records by time range
	for _, record := range ct.records {
		if record.Timestamp.Before(start) || record.Timestamp.After(end) {
			continue
		}

		// Update totals
		stats.TotalRequests++
		stats.TotalTokens += record.TotalTokens
		stats.TotalCost += record.Cost

		// Update provider stats
		pKey := record.Provider
		pStats := stats.ByProvider[pKey]
		pStats.Provider = record.Provider
		pStats.Requests++
		pStats.TotalTokens += record.TotalTokens
		pStats.TotalCost += record.Cost
		stats.ByProvider[pKey] = pStats

		// Update agent stats
		aStats := stats.ByAgent[record.AgentName]
		aStats.AgentName = record.AgentName
		aStats.Requests++
		aStats.TotalTokens += record.TotalTokens
		aStats.TotalCost += record.Cost
		stats.ByAgent[record.AgentName] = aStats

		// Update model stats
		mKey := fmt.Sprintf("%s:%s", record.Provider, record.Model)
		mStats := stats.ByModel[mKey]
		mStats.Model = record.Model
		mStats.Provider = record.Provider
		mStats.Requests++
		mStats.TotalTokens += record.TotalTokens
		mStats.TotalCost += record.Cost
		stats.ByModel[mKey] = mStats
	}

	// Calculate averages
	for key, pStats := range stats.ByProvider {
		if pStats.Requests > 0 {
			pStats.AvgTokensPerRequest = pStats.TotalTokens / pStats.Requests
			stats.ByProvider[key] = pStats
		}
	}

	// Get recent records (last 50)
	recentStart := len(ct.records) - 50
	if recentStart < 0 {
		recentStart = 0
	}
	for i := recentStart; i < len(ct.records); i++ {
		if ct.records[i].Timestamp.After(start) && ct.records[i].Timestamp.Before(end) {
			stats.RecentRecords = append(stats.RecentRecords, ct.records[i])
		}
	}

	return stats
}

// GetAllTimeStats returns stats for all recorded time
func (ct *CostTracker) GetAllTimeStats() UsageStats {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	if len(ct.records) == 0 {
		return UsageStats{
			ByProvider: make(map[string]ProviderStats),
			ByAgent:    make(map[string]AgentStats),
			ByModel:    make(map[string]ModelStats),
			Currency:   "USD",
		}
	}

	start := ct.records[0].Timestamp
	end := ct.records[len(ct.records)-1].Timestamp

	return ct.GetStats(start, end.Add(time.Second))
}

// GetTodayStats returns stats for today
func (ct *CostTracker) GetTodayStats() UsageStats {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	end := start.Add(24 * time.Hour)
	return ct.GetStats(start, end)
}

// GetThisMonthStats returns stats for the current month
func (ct *CostTracker) GetThisMonthStats() UsageStats {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	end := start.AddDate(0, 1, 0)
	return ct.GetStats(start, end)
}

// saveRecordsCopy saves a copy of records to disk (thread-safe for async calls)
func (ct *CostTracker) saveRecordsCopy(records []UsageRecord) error {
	// Ensure directory exists
	dir := filepath.Dir(ct.dataFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Marshal records
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal records: %w", err)
	}

	// Write to file
	if err := os.WriteFile(ct.dataFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write records: %w", err)
	}

	return nil
}

// saveRecords saves records to disk (synchronous, requires lock)
func (ct *CostTracker) saveRecords() error {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	return ct.saveRecordsCopy(ct.records)
}

// loadRecords loads records from disk
func (ct *CostTracker) loadRecords() error {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	// Check if file exists
	if _, err := os.Stat(ct.dataFile); os.IsNotExist(err) {
		return nil // No records to load
	}

	// Read file
	data, err := os.ReadFile(ct.dataFile)
	if err != nil {
		return fmt.Errorf("failed to read records: %w", err)
	}

	// Unmarshal records
	if err := json.Unmarshal(data, &ct.records); err != nil {
		return fmt.Errorf("failed to unmarshal records: %w", err)
	}

	return nil
}

// GetPricingModels returns all pricing models
func (ct *CostTracker) GetPricingModels() []PricingModel {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	models := make([]PricingModel, 0, len(ct.pricingModels))
	for _, pm := range ct.pricingModels {
		models = append(models, pm)
	}
	return models
}

// UpdatePricingModel updates or adds a pricing model
func (ct *CostTracker) UpdatePricingModel(pm PricingModel) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	key := fmt.Sprintf("%s:%s", pm.Provider, pm.ModelName)
	ct.pricingModels[key] = pm
}
