package llm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newTestTracker(t *testing.T) (*CostTracker, string) {
	t.Helper()
	dir := t.TempDir()
	ct := NewCostTracker(dir)
	t.Cleanup(ct.Close)
	return ct, filepath.Join(dir, "usage_records.json")
}

func TestCalculateCost_KnownClaudeModel(t *testing.T) {
	ct, _ := newTestTracker(t)
	cost, currency := ct.calculateCost("claude", "claude-3-5-sonnet-20241022", Usage{
		PromptTokens:     1_000_000,
		CompletionTokens: 1_000_000,
	})
	if currency != "USD" {
		t.Fatalf("currency = %q, want USD", currency)
	}
	// $3/M input + $15/M output = $18 for 1M each
	if cost < 17.99 || cost > 18.01 {
		t.Fatalf("cost = %v, want ~18", cost)
	}
}

func TestCalculateCost_OpenAIPricingFallback(t *testing.T) {
	ct, _ := newTestTracker(t)
	// gpt-4o is not in the static seed; should fall back to modelinfo.
	cost, currency := ct.calculateCost("openai", "gpt-4o", Usage{
		PromptTokens:     1_000_000,
		CompletionTokens: 1_000_000,
	})
	if currency != "USD" {
		t.Fatalf("currency = %q, want USD", currency)
	}
	// gpt-4o is $2.50/M in + $10/M out = $12.50.
	if cost < 12.49 || cost > 12.51 {
		t.Fatalf("cost = %v, want ~12.50 (modelinfo fallback)", cost)
	}
}

func TestCalculateCost_LocalProviderSkipsFallback(t *testing.T) {
	ct, _ := newTestTracker(t)
	// Even if modelinfo happened to have a hit for the model name, local
	// providers must record $0 and never bill via the fallback.
	cost, _ := ct.calculateCost("ollama", "gpt-4o", Usage{
		PromptTokens:     1_000_000,
		CompletionTokens: 1_000_000,
	})
	if cost != 0 {
		t.Fatalf("local provider cost = %v, want 0", cost)
	}
}

func TestIsLocalProvider(t *testing.T) {
	for _, p := range []string{"ollama", "lmstudio", "mlx_lm"} {
		if !isLocalProvider(p) {
			t.Errorf("isLocalProvider(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"openai", "claude", "gemini", "unknown"} {
		if isLocalProvider(p) {
			t.Errorf("isLocalProvider(%q) = true, want false", p)
		}
	}
}

func TestTrackUsage_PersistsToDisk(t *testing.T) {
	ct, dataFile := newTestTracker(t)
	if err := ct.TrackUsage("claude", "claude-3-5-sonnet-20241022", "agent-1",
		Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150}, "req-1"); err != nil {
		t.Fatalf("TrackUsage: %v", err)
	}

	// Close flushes pending save.
	ct.Close()

	data, err := os.ReadFile(dataFile)
	if err != nil {
		t.Fatalf("read records: %v", err)
	}
	var records []UsageRecord
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	if records[0].RequestID != "req-1" || records[0].AgentName != "agent-1" {
		t.Errorf("unexpected record: %+v", records[0])
	}
}

func TestTrackUsage_ConcurrentWritesNoDataLoss(t *testing.T) {
	ct, dataFile := newTestTracker(t)

	const goroutines = 16
	const perGoroutine = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				_ = ct.TrackUsage("claude", "claude-3-5-sonnet-20241022", "agent",
					Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
					"req")
			}
		}()
	}
	wg.Wait()

	// Close flushes the final coalesced save and stops the goroutine.
	ct.Close()

	data, err := os.ReadFile(dataFile)
	if err != nil {
		t.Fatalf("read records: %v", err)
	}
	var records []UsageRecord
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, want := len(records), goroutines*perGoroutine; got != want {
		t.Fatalf("len(records) = %d, want %d", got, want)
	}
}

func TestGetStats_RecentRecordsRespectsTimeRange(t *testing.T) {
	ct, _ := newTestTracker(t)

	// Inject 80 in-range records followed by 30 out-of-range records.
	// Old GetStats walked "last 50" of all records and filtered, so it
	// would return only the 50-30=20 in-range hits. New implementation
	// must return 50 in-range hits.
	now := time.Now()
	start := now.Add(-time.Hour)
	end := now.Add(time.Hour)

	ct.mu.Lock()
	for i := 0; i < 80; i++ {
		ct.records = append(ct.records, UsageRecord{
			Timestamp: now.Add(-30 * time.Minute).Add(time.Duration(i) * time.Millisecond),
			Provider:  "claude", Model: "claude-3-haiku-20240307",
		})
	}
	for i := 0; i < 30; i++ {
		ct.records = append(ct.records, UsageRecord{
			Timestamp: now.Add(2 * time.Hour).Add(time.Duration(i) * time.Millisecond),
			Provider:  "claude", Model: "claude-3-haiku-20240307",
		})
	}
	ct.mu.Unlock()

	stats := ct.GetStats(start, end)
	if len(stats.RecentRecords) != 50 {
		t.Fatalf("RecentRecords = %d, want 50", len(stats.RecentRecords))
	}
	// All returned records must be in range.
	for _, r := range stats.RecentRecords {
		if r.Timestamp.Before(start) || r.Timestamp.After(end) {
			t.Fatalf("record outside range: %v", r.Timestamp)
		}
	}
	// Records should be in chronological order.
	for i := 1; i < len(stats.RecentRecords); i++ {
		if stats.RecentRecords[i].Timestamp.Before(stats.RecentRecords[i-1].Timestamp) {
			t.Fatalf("records out of order at %d", i)
		}
	}
}

func TestLoadRecords_QuarantinesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	dataFile := filepath.Join(dir, "usage_records.json")
	if err := os.WriteFile(dataFile, []byte("{not valid json"), 0644); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}

	ct := NewCostTracker(dir)
	t.Cleanup(ct.Close)

	// Original file should have been renamed; tracker starts with empty records.
	if _, err := os.Stat(dataFile); !os.IsNotExist(err) {
		t.Fatalf("expected original file removed, got err=%v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var sawQuarantine bool
	const prefix = "usage_records.json.corrupt-"
	for _, e := range entries {
		if len(e.Name()) > len(prefix) && e.Name()[:len(prefix)] == prefix {
			sawQuarantine = true
			break
		}
	}
	if !sawQuarantine {
		t.Fatalf("expected quarantined .corrupt-* file in %s", dir)
	}
	if got := len(ct.records); got != 0 {
		t.Fatalf("records after corrupt load = %d, want 0", got)
	}
}
