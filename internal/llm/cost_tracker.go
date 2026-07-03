package llm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/modelinfo"
)

// isLocalProvider reports whether the provider runs locally and should not
// be billed via the curated pricing fallback.
func isLocalProvider(provider string) bool {
	switch provider {
	case "ollama", "lmstudio", "mlx_lm":
		return true
	}
	return false
}

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

	// saveCh signals the writer goroutine that records have changed.
	// Buffered (size 1) so concurrent TrackUsage calls coalesce into a
	// single subsequent write, eliminating interleaved/torn writes.
	saveCh   chan struct{}
	stopCh   chan struct{}
	saveDone chan struct{}
}

// NewCostTracker creates a new cost tracker
func NewCostTracker(dataDir string) *CostTracker {
	ct := &CostTracker{
		pricingModels: make(map[string]PricingModel),
		records:       make([]UsageRecord, 0),
		dataFile:      filepath.Join(dataDir, "usage_records.json"),
		maxRecords:    10000, // Keep last 10k records in memory
		saveCh:        make(chan struct{}, 1),
		stopCh:        make(chan struct{}),
		saveDone:      make(chan struct{}),
	}

	// Initialize default pricing models
	ct.initializePricingModels()

	// Load existing records
	_ = ct.loadRecords() // Ignore error on init, will retry later

	go ct.saveLoop()

	return ct
}

// Close stops the background writer goroutine after flushing any pending save.
func (ct *CostTracker) Close() {
	select {
	case <-ct.stopCh:
		return
	default:
	}
	close(ct.stopCh)
	<-ct.saveDone
}

// saveLoop is the single writer goroutine. It coalesces save signals so that
// bursts of TrackUsage calls produce one subsequent write rather than N
// racing writes.
func (ct *CostTracker) saveLoop() {
	defer close(ct.saveDone)
	for {
		select {
		case <-ct.stopCh:
			// Flush one final time if a save is pending
			select {
			case <-ct.saveCh:
				ct.persistSnapshot()
			default:
			}
			return
		case <-ct.saveCh:
			ct.persistSnapshot()
		}
	}
}

// persistSnapshot snapshots records under read lock and writes to disk.
func (ct *CostTracker) persistSnapshot() {
	ct.mu.RLock()
	snapshot := make([]UsageRecord, len(ct.records))
	copy(snapshot, ct.records)
	ct.mu.RUnlock()

	if err := ct.saveRecordsCopy(snapshot); err != nil {
		logger.Warn("Failed to save cost tracking records", logger.Fields{"error": err})
	}
}

// initializePricingModels sets up default pricing for supported models
func (ct *CostTracker) initializePricingModels() {
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

	ct.addPricingModel(PricingModel{
		ModelName:            "default",
		Provider:             "ollama",
		InputCostPerMillion:  0.0,
		OutputCostPerMillion: 0.0,
		Currency:             "USD",
	})

	ct.addPricingModel(PricingModel{
		ModelName:            "default",
		Provider:             "lmstudio",
		InputCostPerMillion:  0.0,
		OutputCostPerMillion: 0.0,
		Currency:             "USD",
	})

	ct.addPricingModel(PricingModel{
		ModelName:            "default",
		Provider:             "mlx_lm",
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

	// Signal the writer goroutine. Non-blocking: if a save is already
	// queued, this call coalesces into it.
	select {
	case ct.saveCh <- struct{}{}:
	default:
	}

	return nil
}

// calculateCost calculates the cost for a usage record
func (ct *CostTracker) calculateCost(provider, model string, usage Usage) (float64, string) {
	// Try exact match first
	key := fmt.Sprintf("%s:%s", provider, model)
	pm, found := ct.pricingModels[key]

	// Fall back to the curated modelinfo pricing data (covers OpenAI, Gemini,
	// and many Claude variants the static seed doesn't list). Skip this for
	// known-free local providers so we don't accidentally bill ollama runs.
	if !found && !isLocalProvider(provider) {
		if mp := modelinfo.GetPricing(model); mp != nil {
			return float64(usage.PromptTokens)*mp.InputPer1M/1_000_000.0 +
					float64(usage.CompletionTokens)*mp.OutputPer1M/1_000_000.0,
				"USD"
		}
	}

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
	return ct.getStatsLocked(start, end)
}

// getStatsLocked computes usage statistics for a time range. Callers must
// hold ct.mu (read or write); it exists so lock-holding methods can reuse
// the computation without recursively acquiring the RLock, which can
// deadlock when a writer is queued between the two acquisitions.
func (ct *CostTracker) getStatsLocked(start, end time.Time) UsageStats {
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

	// Recent records: take the last 50 records that fall within the range.
	// Walk the records list in reverse so we can stop once we have 50 hits;
	// then reverse the slice to restore chronological order.
	const maxRecent = 50
	for i := len(ct.records) - 1; i >= 0 && len(stats.RecentRecords) < maxRecent; i-- {
		r := ct.records[i]
		if r.Timestamp.Before(start) || r.Timestamp.After(end) {
			continue
		}
		stats.RecentRecords = append(stats.RecentRecords, r)
	}
	for i, j := 0, len(stats.RecentRecords)-1; i < j; i, j = i+1, j-1 {
		stats.RecentRecords[i], stats.RecentRecords[j] = stats.RecentRecords[j], stats.RecentRecords[i]
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

	return ct.getStatsLocked(start, end.Add(time.Second))
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

	// Atomic write: temp file + rename. Avoids leaving a truncated/corrupt
	// file on crash mid-write.
	tmp, err := os.CreateTemp(dir, "usage_records-*.json.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write records: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, ct.dataFile); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
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

	// Unmarshal records. On parse failure, quarantine the corrupt file so
	// the next save does not silently overwrite the only copy of history.
	if err := json.Unmarshal(data, &ct.records); err != nil {
		ct.records = nil
		quarantinePath := fmt.Sprintf("%s.corrupt-%d", ct.dataFile, time.Now().UnixNano())
		if renameErr := os.Rename(ct.dataFile, quarantinePath); renameErr != nil {
			logger.Error("Failed to quarantine corrupt usage records", logger.Fields{
				"file":         ct.dataFile,
				"parse_error":  err.Error(),
				"rename_error": renameErr.Error(),
			})
		} else {
			logger.Error("Quarantined corrupt usage records", logger.Fields{
				"file":           ct.dataFile,
				"quarantined_to": quarantinePath,
				"parse_error":    err.Error(),
			})
		}
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
