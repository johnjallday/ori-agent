package review

import (
	"context"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/session"
)

func TestTextSimilarity(t *testing.T) {
	tests := []struct {
		name   string
		a      string
		b      string
		minSim float64
		maxSim float64
	}{
		{
			name:   "identical strings",
			a:      "how do I fix this error",
			b:      "how do I fix this error",
			minSim: 0.99,
			maxSim: 1.0,
		},
		{
			name:   "nearly identical with typo",
			a:      "how do I fix this error",
			b:      "how do I fix this eror",
			minSim: 0.7,
			maxSim: 1.0,
		},
		{
			name:   "same question rephrased",
			a:      "how do I fix this error",
			b:      "how can I fix this error",
			minSim: 0.7,
			maxSim: 1.0,
		},
		{
			name:   "similar question",
			a:      "why is the database not connecting",
			b:      "database connection is not working",
			minSim: 0.4,
			maxSim: 0.8,
		},
		{
			name:   "completely different",
			a:      "how do I fix this error",
			b:      "what is the weather today",
			minSim: 0.0,
			maxSim: 0.3,
		},
		{
			name:   "empty strings",
			a:      "",
			b:      "",
			minSim: 0.99,
			maxSim: 1.0,
		},
		{
			name:   "one empty",
			a:      "hello",
			b:      "",
			minSim: 0.0,
			maxSim: 0.1,
		},
		{
			name:   "case insensitive",
			a:      "How Do I Fix This",
			b:      "how do i fix this",
			minSim: 0.99,
			maxSim: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sim := textSimilarity(tt.a, tt.b)
			if sim < tt.minSim || sim > tt.maxSim {
				t.Errorf("textSimilarity(%q, %q) = %v, want between %v and %v",
					tt.a, tt.b, sim, tt.minSim, tt.maxSim)
			}
		})
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple sentence",
			input:    "hello world",
			expected: []string{"hello", "world"},
		},
		{
			name:     "with punctuation",
			input:    "Hello, World!",
			expected: []string{"hello", "world"},
		},
		{
			name:     "with numbers",
			input:    "version 123 release",
			expected: []string{"version", "123", "release"},
		},
		{
			name:     "short tokens filtered",
			input:    "I am a test",
			expected: []string{"am", "test"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tokenize(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("tokenize(%q) = %v, want %v", tt.input, result, tt.expected)
				return
			}
			for i, token := range result {
				if token != tt.expected[i] {
					t.Errorf("tokenize(%q)[%d] = %q, want %q", tt.input, i, token, tt.expected[i])
				}
			}
		})
	}
}

func TestDetectUserRetries(t *testing.T) {
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		messages       []session.Message
		config         DetectionConfig
		expectedIssues int
	}{
		{
			name: "no retries - single message",
			messages: []session.Message{
				{ID: "1", SessionID: "s1", Role: session.RoleUser, Content: "hello", CreatedAt: baseTime},
			},
			config:         DefaultDetectionConfig(),
			expectedIssues: 0,
		},
		{
			name: "no retries - different content",
			messages: []session.Message{
				{ID: "1", SessionID: "s1", Role: session.RoleUser, Content: "how do I fix this error", CreatedAt: baseTime},
				{ID: "2", SessionID: "s1", Role: session.RoleAssistant, Content: "Try this...", CreatedAt: baseTime.Add(10 * time.Second)},
				{ID: "3", SessionID: "s1", Role: session.RoleUser, Content: "what is the weather today", CreatedAt: baseTime.Add(30 * time.Second)},
			},
			config:         DefaultDetectionConfig(),
			expectedIssues: 0,
		},
		{
			name: "retry detected - same question within 60s",
			messages: []session.Message{
				{ID: "1", SessionID: "s1", Role: session.RoleUser, Content: "how do I fix this error", CreatedAt: baseTime},
				{ID: "2", SessionID: "s1", Role: session.RoleAssistant, Content: "Try this...", CreatedAt: baseTime.Add(10 * time.Second)},
				{ID: "3", SessionID: "s1", Role: session.RoleUser, Content: "how do I fix this error", CreatedAt: baseTime.Add(30 * time.Second)},
			},
			config:         DefaultDetectionConfig(),
			expectedIssues: 1,
		},
		{
			name: "retry detected - similar question within 60s",
			messages: []session.Message{
				{ID: "1", SessionID: "s1", Role: session.RoleUser, Content: "how do I fix this error", CreatedAt: baseTime},
				{ID: "2", SessionID: "s1", Role: session.RoleAssistant, Content: "Try this...", CreatedAt: baseTime.Add(10 * time.Second)},
				{ID: "3", SessionID: "s1", Role: session.RoleUser, Content: "how can I fix this error", CreatedAt: baseTime.Add(30 * time.Second)},
			},
			config:         DefaultDetectionConfig(),
			expectedIssues: 1,
		},
		{
			name: "no retry - outside time window",
			messages: []session.Message{
				{ID: "1", SessionID: "s1", Role: session.RoleUser, Content: "how do I fix this error", CreatedAt: baseTime},
				{ID: "2", SessionID: "s1", Role: session.RoleAssistant, Content: "Try this...", CreatedAt: baseTime.Add(10 * time.Second)},
				{ID: "3", SessionID: "s1", Role: session.RoleUser, Content: "how do I fix this error", CreatedAt: baseTime.Add(2 * time.Minute)},
			},
			config:         DefaultDetectionConfig(),
			expectedIssues: 0,
		},
		{
			name: "multiple retries - chain detection",
			messages: []session.Message{
				{ID: "1", SessionID: "s1", Role: session.RoleUser, Content: "fix error please", CreatedAt: baseTime},
				{ID: "2", SessionID: "s1", Role: session.RoleAssistant, Content: "...", CreatedAt: baseTime.Add(5 * time.Second)},
				{ID: "3", SessionID: "s1", Role: session.RoleUser, Content: "fix error please", CreatedAt: baseTime.Add(15 * time.Second)},
				{ID: "4", SessionID: "s1", Role: session.RoleAssistant, Content: "...", CreatedAt: baseTime.Add(20 * time.Second)},
				{ID: "5", SessionID: "s1", Role: session.RoleUser, Content: "fix error please", CreatedAt: baseTime.Add(30 * time.Second)},
			},
			config:         DefaultDetectionConfig(),
			expectedIssues: 1, // Should be grouped as one issue with count 3
		},
		{
			name: "high sensitivity - detect with lower threshold",
			messages: []session.Message{
				{ID: "1", SessionID: "s1", Role: session.RoleUser, Content: "database error connection failed", CreatedAt: baseTime},
				{ID: "2", SessionID: "s1", Role: session.RoleAssistant, Content: "...", CreatedAt: baseTime.Add(5 * time.Second)},
				{ID: "3", SessionID: "s1", Role: session.RoleUser, Content: "connection to database failing", CreatedAt: baseTime.Add(30 * time.Second)},
			},
			config:         ConfigForSensitivity("high"),
			expectedIssues: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := NewDetector(tt.config)
			issues := detector.DetectUserRetries(context.Background(), tt.messages, "test-agent")

			if len(issues) != tt.expectedIssues {
				t.Errorf("DetectUserRetries() found %d issues, want %d", len(issues), tt.expectedIssues)
				for i, issue := range issues {
					t.Logf("Issue %d: %+v", i, issue)
				}
			}

			// Verify issue structure for detected issues
			for _, issue := range issues {
				if issue.ID == "" {
					t.Error("Issue ID should not be empty")
				}
				if issue.SessionID == "" {
					t.Error("Issue SessionID should not be empty")
				}
				if issue.Type != IssueTypeUserRetry {
					t.Errorf("Issue Type = %v, want %v", issue.Type, IssueTypeUserRetry)
				}
				if issue.OccurrenceCount < 2 {
					t.Errorf("Issue OccurrenceCount = %d, want >= 2", issue.OccurrenceCount)
				}
			}
		})
	}
}

func TestSummarizeContent(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		maxLen   int
		expected string
	}{
		{
			name:     "short content",
			content:  "hello world",
			maxLen:   100,
			expected: "hello world",
		},
		{
			name:     "long content truncated",
			content:  "this is a very long content that should be truncated",
			maxLen:   20,
			expected: "this is a very lo...",
		},
		{
			name:     "newlines replaced",
			content:  "hello\nworld",
			maxLen:   100,
			expected: "hello world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := summarizeContent(tt.content, tt.maxLen)
			if result != tt.expected {
				t.Errorf("summarizeContent(%q, %d) = %q, want %q",
					tt.content, tt.maxLen, result, tt.expected)
			}
		})
	}
}

func TestHashContent(t *testing.T) {
	// Same content should produce same hash
	hash1 := hashContent("hello world")
	hash2 := hashContent("hello world")
	if hash1 != hash2 {
		t.Errorf("Same content produced different hashes: %s vs %s", hash1, hash2)
	}

	// Different content should produce different hash
	hash3 := hashContent("goodbye world")
	if hash1 == hash3 {
		t.Errorf("Different content produced same hash: %s", hash1)
	}

	// Case insensitive
	hash4 := hashContent("Hello World")
	if hash1 != hash4 {
		t.Errorf("Case difference produced different hashes: %s vs %s", hash1, hash4)
	}
}

func TestConfigForSensitivity(t *testing.T) {
	low := ConfigForSensitivity("low")
	if low.ToolRetryMinCount != 5 {
		t.Errorf("Low sensitivity ToolRetryMinCount = %d, want 5", low.ToolRetryMinCount)
	}

	medium := ConfigForSensitivity("medium")
	if medium.ToolRetryMinCount != 3 {
		t.Errorf("Medium sensitivity ToolRetryMinCount = %d, want 3", medium.ToolRetryMinCount)
	}

	high := ConfigForSensitivity("high")
	if high.ToolRetryMinCount != 2 {
		t.Errorf("High sensitivity ToolRetryMinCount = %d, want 2", high.ToolRetryMinCount)
	}
}

func TestDetectToolRetryLoops(t *testing.T) {
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		toolCalls      []session.ToolCall
		config         DetectionConfig
		expectedIssues int
	}{
		{
			name:           "no tool calls",
			toolCalls:      []session.ToolCall{},
			config:         DefaultDetectionConfig(),
			expectedIssues: 0,
		},
		{
			name: "fewer than threshold",
			toolCalls: []session.ToolCall{
				{ID: "1", SessionID: "s1", ToolName: "search", Arguments: `{"q": "test"}`, CreatedAt: baseTime},
				{ID: "2", SessionID: "s1", ToolName: "search", Arguments: `{"q": "test"}`, CreatedAt: baseTime.Add(time.Second)},
			},
			config:         DefaultDetectionConfig(), // requires 3
			expectedIssues: 0,
		},
		{
			name: "retry loop detected - same args 3 times",
			toolCalls: []session.ToolCall{
				{ID: "1", SessionID: "s1", MessageID: "m1", ToolName: "search", Arguments: `{"q": "test"}`, CreatedAt: baseTime},
				{ID: "2", SessionID: "s1", MessageID: "m2", ToolName: "search", Arguments: `{"q": "test"}`, CreatedAt: baseTime.Add(time.Second)},
				{ID: "3", SessionID: "s1", MessageID: "m3", ToolName: "search", Arguments: `{"q": "test"}`, CreatedAt: baseTime.Add(2 * time.Second)},
			},
			config:         DefaultDetectionConfig(),
			expectedIssues: 1,
		},
		{
			name: "no retry - different arguments",
			toolCalls: []session.ToolCall{
				{ID: "1", SessionID: "s1", ToolName: "search", Arguments: `{"q": "test1"}`, CreatedAt: baseTime},
				{ID: "2", SessionID: "s1", ToolName: "search", Arguments: `{"q": "test2"}`, CreatedAt: baseTime.Add(time.Second)},
				{ID: "3", SessionID: "s1", ToolName: "search", Arguments: `{"q": "test3"}`, CreatedAt: baseTime.Add(2 * time.Second)},
			},
			config:         DefaultDetectionConfig(),
			expectedIssues: 0,
		},
		{
			name: "retry loop with errors noted",
			toolCalls: []session.ToolCall{
				{ID: "1", SessionID: "s1", MessageID: "m1", ToolName: "read_file", Arguments: `{"path": "/foo"}`, Error: "file not found", CreatedAt: baseTime},
				{ID: "2", SessionID: "s1", MessageID: "m2", ToolName: "read_file", Arguments: `{"path": "/foo"}`, Error: "file not found", CreatedAt: baseTime.Add(time.Second)},
				{ID: "3", SessionID: "s1", MessageID: "m3", ToolName: "read_file", Arguments: `{"path": "/foo"}`, Error: "file not found", CreatedAt: baseTime.Add(2 * time.Second)},
			},
			config:         DefaultDetectionConfig(),
			expectedIssues: 1,
		},
		{
			name: "multiple tools - only one has retry",
			toolCalls: []session.ToolCall{
				{ID: "1", SessionID: "s1", MessageID: "m1", ToolName: "search", Arguments: `{"q": "a"}`, CreatedAt: baseTime},
				{ID: "2", SessionID: "s1", MessageID: "m2", ToolName: "read", Arguments: `{"path": "/x"}`, CreatedAt: baseTime.Add(time.Second)},
				{ID: "3", SessionID: "s1", MessageID: "m3", ToolName: "search", Arguments: `{"q": "a"}`, CreatedAt: baseTime.Add(2 * time.Second)},
				{ID: "4", SessionID: "s1", MessageID: "m4", ToolName: "read", Arguments: `{"path": "/y"}`, CreatedAt: baseTime.Add(3 * time.Second)},
				{ID: "5", SessionID: "s1", MessageID: "m5", ToolName: "search", Arguments: `{"q": "a"}`, CreatedAt: baseTime.Add(4 * time.Second)},
			},
			config:         DefaultDetectionConfig(),
			expectedIssues: 1, // Only search has 3 identical calls
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := NewDetector(tt.config)
			issues := detector.DetectToolRetryLoops(context.Background(), tt.toolCalls, "test-agent")

			if len(issues) != tt.expectedIssues {
				t.Errorf("DetectToolRetryLoops() found %d issues, want %d", len(issues), tt.expectedIssues)
				for i, issue := range issues {
					t.Logf("Issue %d: %+v", i, issue)
				}
			}

			// Verify issue structure
			for _, issue := range issues {
				if issue.Type != IssueTypeToolRetryLoop {
					t.Errorf("Issue Type = %v, want %v", issue.Type, IssueTypeToolRetryLoop)
				}
				if issue.ToolName == "" {
					t.Error("Issue ToolName should not be empty for tool retry loop")
				}
			}
		})
	}
}

func TestDetectIgnoredErrors(t *testing.T) {
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		toolCalls      []session.ToolCall
		expectedIssues int
	}{
		{
			name:           "no tool calls",
			toolCalls:      []session.ToolCall{},
			expectedIssues: 0,
		},
		{
			name: "single tool call with error - no retry",
			toolCalls: []session.ToolCall{
				{ID: "1", SessionID: "s1", ToolName: "read", Arguments: `{"path": "/x"}`, Error: "not found", CreatedAt: baseTime},
			},
			expectedIssues: 0,
		},
		{
			name: "error followed by same call - ignored error detected",
			toolCalls: []session.ToolCall{
				{ID: "1", SessionID: "s1", MessageID: "m1", ToolName: "read", Arguments: `{"path": "/x"}`, Error: "not found", CreatedAt: baseTime},
				{ID: "2", SessionID: "s1", MessageID: "m2", ToolName: "read", Arguments: `{"path": "/x"}`, Result: "success", CreatedAt: baseTime.Add(time.Second)},
			},
			expectedIssues: 1,
		},
		{
			name: "error followed by different args - not ignored",
			toolCalls: []session.ToolCall{
				{ID: "1", SessionID: "s1", ToolName: "read", Arguments: `{"path": "/x"}`, Error: "not found", CreatedAt: baseTime},
				{ID: "2", SessionID: "s1", ToolName: "read", Arguments: `{"path": "/y"}`, Result: "success", CreatedAt: baseTime.Add(time.Second)},
			},
			expectedIssues: 0,
		},
		{
			name: "error followed by different tool - not ignored",
			toolCalls: []session.ToolCall{
				{ID: "1", SessionID: "s1", ToolName: "read", Arguments: `{"path": "/x"}`, Error: "not found", CreatedAt: baseTime},
				{ID: "2", SessionID: "s1", ToolName: "write", Arguments: `{"path": "/x"}`, Result: "success", CreatedAt: baseTime.Add(time.Second)},
			},
			expectedIssues: 0,
		},
		{
			name: "chain of ignored errors",
			toolCalls: []session.ToolCall{
				{ID: "1", SessionID: "s1", MessageID: "m1", ToolName: "read", Arguments: `{"path": "/x"}`, Error: "not found", CreatedAt: baseTime},
				{ID: "2", SessionID: "s1", MessageID: "m2", ToolName: "read", Arguments: `{"path": "/x"}`, Error: "not found", CreatedAt: baseTime.Add(time.Second)},
				{ID: "3", SessionID: "s1", MessageID: "m3", ToolName: "read", Arguments: `{"path": "/x"}`, Error: "not found", CreatedAt: baseTime.Add(2 * time.Second)},
			},
			expectedIssues: 1, // One issue with occurrence_count = 3
		},
		{
			name: "no error in first call",
			toolCalls: []session.ToolCall{
				{ID: "1", SessionID: "s1", ToolName: "read", Arguments: `{"path": "/x"}`, Result: "content", CreatedAt: baseTime},
				{ID: "2", SessionID: "s1", ToolName: "read", Arguments: `{"path": "/x"}`, Result: "content", CreatedAt: baseTime.Add(time.Second)},
			},
			expectedIssues: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := NewDetector(DefaultDetectionConfig())
			issues := detector.DetectIgnoredErrors(context.Background(), tt.toolCalls, "test-agent")

			if len(issues) != tt.expectedIssues {
				t.Errorf("DetectIgnoredErrors() found %d issues, want %d", len(issues), tt.expectedIssues)
				for i, issue := range issues {
					t.Logf("Issue %d: %+v", i, issue)
				}
			}

			// Verify issue structure
			for _, issue := range issues {
				if issue.Type != IssueTypeIgnoredError {
					t.Errorf("Issue Type = %v, want %v", issue.Type, IssueTypeIgnoredError)
				}
				if issue.ToolName == "" {
					t.Error("Issue ToolName should not be empty for ignored error")
				}
			}
		})
	}
}

func TestNormalizeArguments(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "already normalized",
			input:    `{"key": "value"}`,
			expected: `{"key": "value"}`,
		},
		{
			name:     "with newlines",
			input:    "{\n  \"key\": \"value\"\n}",
			expected: `{ "key": "value"}`,
		},
		{
			name:     "with tabs",
			input:    "{\t\"key\":\t\"value\"}",
			expected: `{"key":"value"}`,
		},
		{
			name:     "with extra spaces",
			input:    `{"key":   "value"}`,
			expected: `{"key": "value"}`,
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeArguments(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeArguments(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
