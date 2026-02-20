package utilitytelemetry

import (
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultMaxEvents   = 200
	defaultMaxSamples  = 256
	eventTypeRoute     = "route_decision"
	eventTypeInvoke    = "tool_invocation"
	eventTypeResult    = "tool_result"
	eventTypeDelegate  = "delegation_event"
	defaultRouteMode   = "assistant_chat"
	defaultToolName    = "unknown_tool"
	defaultProviderKey = "unknown"
)

// Event captures a utility telemetry event emitted during request handling.
type Event struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	RouteMode string    `json:"route_mode,omitempty"`
	ToolName  string    `json:"tool_name,omitempty"`
	Provider  string    `json:"provider,omitempty"`
	Success   bool      `json:"success,omitempty"`
	LatencyMs int64     `json:"latency_ms,omitempty"`
	Error     string    `json:"error,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Target    string    `json:"target,omitempty"`
}

// Totals summarizes utility call outcomes.
type Totals struct {
	Calls        int64   `json:"calls"`
	Successes    int64   `json:"successes"`
	Failures     int64   `json:"failures"`
	SuccessRate  float64 `json:"success_rate"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	P95LatencyMs int64   `json:"p95_latency_ms"`
}

// ToolMetrics is an aggregated view of per-tool utility telemetry.
type ToolMetrics struct {
	ToolName      string    `json:"tool_name"`
	Provider      string    `json:"provider,omitempty"`
	Calls         int64     `json:"calls"`
	Successes     int64     `json:"successes"`
	Failures      int64     `json:"failures"`
	SuccessRate   float64   `json:"success_rate"`
	AvgLatencyMs  float64   `json:"avg_latency_ms"`
	P95LatencyMs  int64     `json:"p95_latency_ms"`
	LastLatencyMs int64     `json:"last_latency_ms,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	LastUpdatedAt time.Time `json:"last_updated_at,omitempty"`
}

// Snapshot is the payload returned by usage endpoints for utility telemetry.
type Snapshot struct {
	GeneratedAt  time.Time              `json:"generated_at"`
	EventCounts  map[string]int64       `json:"event_counts"`
	RouteCounts  map[string]int64       `json:"route_counts"`
	Tools        map[string]ToolMetrics `json:"tools"`
	Totals       Totals                 `json:"totals"`
	RecentEvents []Event                `json:"recent_events,omitempty"`
}

type toolAccumulator struct {
	ToolName     string
	Provider     string
	Calls        int64
	Successes    int64
	Failures     int64
	LastError    string
	LastLatency  int64
	LastUpdated  time.Time
	latencyItems []int64
}

// Tracker stores in-memory utility telemetry and produces aggregate snapshots.
type Tracker struct {
	mu          sync.RWMutex
	maxEvents   int
	maxSamples  int
	eventCounts map[string]int64
	routeCounts map[string]int64
	toolStats   map[string]*toolAccumulator
	events      []Event
}

// NewTracker creates a utility telemetry tracker.
func NewTracker(maxEvents int) *Tracker {
	if maxEvents <= 0 {
		maxEvents = defaultMaxEvents
	}
	return &Tracker{
		maxEvents:   maxEvents,
		maxSamples:  defaultMaxSamples,
		eventCounts: make(map[string]int64),
		routeCounts: make(map[string]int64),
		toolStats:   make(map[string]*toolAccumulator),
		events:      make([]Event, 0, maxEvents),
	}
}

// RecordRouteDecision records a route classification decision.
func (t *Tracker) RecordRouteDecision(mode, reason string) {
	mode = normalizeRouteMode(mode)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.routeCounts[mode]++
	t.appendEventLocked(Event{
		Type:      eventTypeRoute,
		Timestamp: time.Now(),
		RouteMode: mode,
		Reason:    strings.TrimSpace(reason),
	})
}

// RecordDelegationEvent records delegation behavior for non-direct utility routes.
func (t *Tracker) RecordDelegationEvent(mode, reason, target string) {
	mode = normalizeRouteMode(mode)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.appendEventLocked(Event{
		Type:      eventTypeDelegate,
		Timestamp: time.Now(),
		RouteMode: mode,
		Reason:    strings.TrimSpace(reason),
		Target:    strings.TrimSpace(target),
	})
}

// RecordToolInvocation records the start of a utility tool call.
func (t *Tracker) RecordToolInvocation(toolName, provider string) {
	toolName = normalizeToolName(toolName)
	provider = normalizeProvider(provider)
	t.mu.Lock()
	defer t.mu.Unlock()

	acc := t.ensureToolLocked(toolName)
	acc.Calls++
	if provider != "" && provider != defaultProviderKey {
		acc.Provider = provider
	}
	acc.LastUpdated = time.Now()

	t.appendEventLocked(Event{
		Type:      eventTypeInvoke,
		Timestamp: acc.LastUpdated,
		ToolName:  toolName,
		Provider:  acc.Provider,
	})
}

// RecordToolResult records the outcome of a utility tool call.
func (t *Tracker) RecordToolResult(toolName, provider string, success bool, latency time.Duration, errMsg string) {
	toolName = normalizeToolName(toolName)
	provider = normalizeProvider(provider)
	latencyMs := latency.Milliseconds()
	if latencyMs < 0 {
		latencyMs = 0
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	acc := t.ensureToolLocked(toolName)
	if acc.Calls == 0 {
		acc.Calls = 1
	}
	if provider != "" && provider != defaultProviderKey {
		acc.Provider = provider
	}
	if success {
		acc.Successes++
		acc.LastError = ""
	} else {
		acc.Failures++
		acc.LastError = strings.TrimSpace(errMsg)
	}
	acc.LastLatency = latencyMs
	acc.LastUpdated = time.Now()
	acc.latencyItems = appendBoundedSample(acc.latencyItems, latencyMs, t.maxSamples)

	t.appendEventLocked(Event{
		Type:      eventTypeResult,
		Timestamp: acc.LastUpdated,
		ToolName:  toolName,
		Provider:  acc.Provider,
		Success:   success,
		LatencyMs: latencyMs,
		Error:     strings.TrimSpace(errMsg),
	})
}

// Snapshot returns an aggregate view of current utility telemetry.
func (t *Tracker) Snapshot() Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()

	toolMetrics := make(map[string]ToolMetrics, len(t.toolStats))
	allLatencies := make([]int64, 0, len(t.toolStats)*8)
	totals := Totals{}

	for key, acc := range t.toolStats {
		if acc == nil {
			continue
		}
		samples := append([]int64(nil), acc.latencyItems...)
		avg := averageLatency(samples)
		p95 := percentile95(samples)
		totalCalls := acc.Calls
		if totalCalls == 0 {
			totalCalls = acc.Successes + acc.Failures
		}
		successRate := 0.0
		if totalCalls > 0 {
			successRate = float64(acc.Successes) / float64(totalCalls)
		}

		toolMetrics[key] = ToolMetrics{
			ToolName:      acc.ToolName,
			Provider:      acc.Provider,
			Calls:         totalCalls,
			Successes:     acc.Successes,
			Failures:      acc.Failures,
			SuccessRate:   successRate,
			AvgLatencyMs:  avg,
			P95LatencyMs:  p95,
			LastLatencyMs: acc.LastLatency,
			LastError:     acc.LastError,
			LastUpdatedAt: acc.LastUpdated,
		}

		totals.Calls += totalCalls
		totals.Successes += acc.Successes
		totals.Failures += acc.Failures
		allLatencies = append(allLatencies, samples...)
	}

	if totals.Calls > 0 {
		totals.SuccessRate = float64(totals.Successes) / float64(totals.Calls)
	}
	totals.AvgLatencyMs = averageLatency(allLatencies)
	totals.P95LatencyMs = percentile95(allLatencies)

	return Snapshot{
		GeneratedAt:  time.Now(),
		EventCounts:  cloneIntMap(t.eventCounts),
		RouteCounts:  cloneIntMap(t.routeCounts),
		Tools:        toolMetrics,
		Totals:       totals,
		RecentEvents: append([]Event(nil), t.events...),
	}
}

func (t *Tracker) ensureToolLocked(toolName string) *toolAccumulator {
	key := normalizeToolName(toolName)
	acc, ok := t.toolStats[key]
	if ok && acc != nil {
		return acc
	}
	acc = &toolAccumulator{
		ToolName:     key,
		Provider:     defaultProviderKey,
		latencyItems: make([]int64, 0, 16),
	}
	t.toolStats[key] = acc
	return acc
}

func (t *Tracker) appendEventLocked(ev Event) {
	t.eventCounts[ev.Type]++
	if t.maxEvents <= 0 {
		return
	}
	if len(t.events) >= t.maxEvents {
		copy(t.events, t.events[1:])
		t.events[len(t.events)-1] = ev
		return
	}
	t.events = append(t.events, ev)
}

func normalizeRouteMode(mode string) string {
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		return defaultRouteMode
	}
	return mode
}

func normalizeToolName(toolName string) string {
	toolName = strings.TrimSpace(strings.ToLower(toolName))
	if toolName == "" {
		return defaultToolName
	}
	return toolName
}

func normalizeProvider(provider string) string {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return defaultProviderKey
	}
	return provider
}

func appendBoundedSample(samples []int64, value int64, max int) []int64 {
	if max <= 0 {
		max = defaultMaxSamples
	}
	if len(samples) >= max {
		copy(samples, samples[1:])
		samples[len(samples)-1] = value
		return samples
	}
	return append(samples, value)
}

func averageLatency(samples []int64) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum int64
	for _, item := range samples {
		sum += item
	}
	return float64(sum) / float64(len(samples))
}

func percentile95(samples []int64) int64 {
	if len(samples) == 0 {
		return 0
	}
	cp := append([]int64(nil), samples...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	idx := int(float64(len(cp)-1) * 0.95)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
}

func cloneIntMap(src map[string]int64) map[string]int64 {
	if len(src) == 0 {
		return map[string]int64{}
	}
	dst := make(map[string]int64, len(src))
	for key, val := range src {
		dst[key] = val
	}
	return dst
}
