package review

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/session"
)

// Detector analyzes sessions for problematic patterns.
type Detector struct {
	config DetectionConfig
}

// NewDetector creates a new detector with the given configuration.
func NewDetector(config DetectionConfig) *Detector {
	return &Detector{config: config}
}

// DetectUserRetries finds user messages that are similar and sent within
// a short time window, indicating the previous response was unhelpful.
func (d *Detector) DetectUserRetries(ctx context.Context, messages []session.Message, agentName string) []Issue {
	issues := []Issue{}

	// Filter to user messages only
	userMessages := make([]session.Message, 0)
	for _, msg := range messages {
		if msg.Role == session.RoleUser {
			userMessages = append(userMessages, msg)
		}
	}

	if len(userMessages) < 2 {
		return issues
	}

	// Track which messages have been grouped into issues
	grouped := make(map[string]bool)

	for i := 0; i < len(userMessages)-1; i++ {
		if grouped[userMessages[i].ID] {
			continue
		}

		current := userMessages[i]
		retryGroup := []session.Message{current}

		// Look for similar messages within the time window
		for j := i + 1; j < len(userMessages); j++ {
			next := userMessages[j]

			// Check time window
			timeDiff := next.CreatedAt.Sub(current.CreatedAt)
			if timeDiff > time.Duration(d.config.UserRetryWindowSeconds)*time.Second {
				break
			}

			// Check similarity
			similarity := textSimilarity(current.Content, next.Content)
			if similarity >= d.config.UserRetrySimilarityThreshold {
				retryGroup = append(retryGroup, next)
				grouped[next.ID] = true
				// Update current for next comparison (chain detection)
				current = next
			}
		}

		// If we found retries, create an issue
		if len(retryGroup) >= 2 {
			grouped[userMessages[i].ID] = true

			issue := Issue{
				ID:              uuid.New().String(),
				SessionID:       retryGroup[0].SessionID,
				AgentName:       agentName,
				Type:            IssueTypeUserRetry,
				OccurrenceCount: len(retryGroup),
				FirstMessageID:  retryGroup[0].ID,
				LastMessageID:   retryGroup[len(retryGroup)-1].ID,
				ContextSummary:  summarizeContent(retryGroup[0].Content, 100),
				ContentHash:     hashContent(retryGroup[0].Content),
				CreatedAt:       time.Now(),
			}

			issues = append(issues, issue)
		}
	}

	return issues
}

// textSimilarity calculates similarity between two text strings.
// Returns a value between 0 (completely different) and 1 (identical).
// Uses a combination of Jaccard similarity on tokens and length ratio.
func textSimilarity(a, b string) float64 {
	// Normalize and tokenize
	tokensA := tokenize(a)
	tokensB := tokenize(b)

	if len(tokensA) == 0 && len(tokensB) == 0 {
		return 1.0
	}
	if len(tokensA) == 0 || len(tokensB) == 0 {
		return 0.0
	}

	// Calculate Jaccard similarity
	setA := make(map[string]bool)
	for _, t := range tokensA {
		setA[t] = true
	}

	setB := make(map[string]bool)
	for _, t := range tokensB {
		setB[t] = true
	}

	// Intersection
	intersection := 0
	for t := range setA {
		if setB[t] {
			intersection++
		}
	}

	// Union
	union := make(map[string]bool)
	for t := range setA {
		union[t] = true
	}
	for t := range setB {
		union[t] = true
	}

	if len(union) == 0 {
		return 0.0
	}

	jaccard := float64(intersection) / float64(len(union))

	// Also factor in length similarity
	lenA := float64(len(tokensA))
	lenB := float64(len(tokensB))
	lenRatio := 1.0
	if lenA > lenB {
		lenRatio = lenB / lenA
	} else if lenB > lenA {
		lenRatio = lenA / lenB
	}

	// Weighted combination
	return (jaccard*0.7 + lenRatio*0.3)
}

// tokenize splits text into normalized tokens for comparison.
func tokenize(text string) []string {
	// Convert to lowercase
	text = strings.ToLower(text)

	// Split on whitespace and punctuation
	tokens := make([]string, 0)
	current := strings.Builder{}

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(r)
		} else {
			if current.Len() > 0 {
				token := current.String()
				// Skip very short tokens (noise)
				if len(token) > 1 {
					tokens = append(tokens, token)
				}
				current.Reset()
			}
		}
	}

	// Don't forget the last token
	if current.Len() > 1 {
		tokens = append(tokens, current.String())
	}

	return tokens
}

// summarizeContent creates a brief summary of content.
func summarizeContent(content string, maxLen int) string {
	content = strings.TrimSpace(content)

	// Replace newlines with spaces
	content = strings.ReplaceAll(content, "\n", " ")
	content = strings.ReplaceAll(content, "\r", " ")

	// Collapse multiple spaces
	for strings.Contains(content, "  ") {
		content = strings.ReplaceAll(content, "  ", " ")
	}

	if len(content) <= maxLen {
		return content
	}

	// Truncate and add ellipsis
	return content[:maxLen-3] + "..."
}

// hashContent creates a hash of content for deduplication.
func hashContent(content string) string {
	// Normalize before hashing
	normalized := strings.ToLower(strings.TrimSpace(content))
	hash := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(hash[:8]) // First 8 bytes is enough for dedup
}

// DetectToolRetryLoops finds cases where the same tool was called repeatedly
// with identical or near-identical arguments without success.
func (d *Detector) DetectToolRetryLoops(ctx context.Context, toolCalls []session.ToolCall, agentName string) []Issue {
	issues := []Issue{}

	if len(toolCalls) < d.config.ToolRetryMinCount {
		return issues
	}

	// Group tool calls by tool name
	byTool := make(map[string][]session.ToolCall)
	for _, tc := range toolCalls {
		byTool[tc.ToolName] = append(byTool[tc.ToolName], tc)
	}

	// Check each tool for retry patterns
	for toolName, calls := range byTool {
		if len(calls) < d.config.ToolRetryMinCount {
			continue
		}

		// Group by normalized arguments
		byArgs := make(map[string][]session.ToolCall)
		for _, tc := range calls {
			normalizedArgs := normalizeArguments(tc.Arguments)
			argHash := hashContent(normalizedArgs)
			byArgs[argHash] = append(byArgs[argHash], tc)
		}

		// Check for retry loops (same args called multiple times)
		for argHash, argCalls := range byArgs {
			if len(argCalls) >= d.config.ToolRetryMinCount {
				// Check if there were errors or no successful resolution
				hasErrors := false
				for _, tc := range argCalls {
					if tc.Error != "" {
						hasErrors = true
						break
					}
				}

				// Create issue for this retry loop
				issue := Issue{
					ID:              uuid.New().String(),
					SessionID:       argCalls[0].SessionID,
					AgentName:       agentName,
					Type:            IssueTypeToolRetryLoop,
					ToolName:        toolName,
					OccurrenceCount: len(argCalls),
					FirstMessageID:  argCalls[0].MessageID,
					LastMessageID:   argCalls[len(argCalls)-1].MessageID,
					ContextSummary:  summarizeContent(argCalls[0].Arguments, 100),
					ContentHash:     argHash,
					CreatedAt:       time.Now(),
				}

				// Add note about errors if present
				if hasErrors {
					issue.ContextSummary = "[with errors] " + issue.ContextSummary
				}

				issues = append(issues, issue)
			}
		}
	}

	return issues
}

// DetectIgnoredErrors finds cases where a tool returned an error and was
// immediately called again with the same arguments.
func (d *Detector) DetectIgnoredErrors(ctx context.Context, toolCalls []session.ToolCall, agentName string) []Issue {
	issues := []Issue{}

	if len(toolCalls) < 2 {
		return issues
	}

	// Track which tool calls have been grouped into issues
	grouped := make(map[string]bool)

	for i := 0; i < len(toolCalls)-1; i++ {
		current := toolCalls[i]

		// Skip if already grouped or no error
		if grouped[current.ID] || current.Error == "" {
			continue
		}

		// Look for identical follow-up calls
		retryGroup := []session.ToolCall{current}
		currentArgs := normalizeArguments(current.Arguments)

		for j := i + 1; j < len(toolCalls); j++ {
			next := toolCalls[j]

			// Must be same tool
			if next.ToolName != current.ToolName {
				break
			}

			// Check if arguments are identical
			nextArgs := normalizeArguments(next.Arguments)
			if currentArgs != nextArgs {
				break
			}

			retryGroup = append(retryGroup, next)
			grouped[next.ID] = true

			// If this one also has an error, continue looking
			if next.Error == "" {
				break
			}
		}

		// If we found ignored error retries
		if len(retryGroup) >= 2 {
			grouped[current.ID] = true

			issue := Issue{
				ID:              uuid.New().String(),
				SessionID:       current.SessionID,
				AgentName:       agentName,
				Type:            IssueTypeIgnoredError,
				ToolName:        current.ToolName,
				OccurrenceCount: len(retryGroup),
				FirstMessageID:  current.MessageID,
				LastMessageID:   retryGroup[len(retryGroup)-1].MessageID,
				ContextSummary:  summarizeContent(current.Error, 100),
				ContentHash:     hashContent(current.Arguments),
				CreatedAt:       time.Now(),
			}

			issues = append(issues, issue)
		}
	}

	return issues
}

// normalizeArguments normalizes JSON arguments for comparison.
// Handles whitespace differences and key ordering.
func normalizeArguments(args string) string {
	// Trim whitespace
	args = strings.TrimSpace(args)

	// For simple comparison, just normalize whitespace
	// A more sophisticated approach would parse JSON and re-serialize
	args = strings.ReplaceAll(args, "\n", "")
	args = strings.ReplaceAll(args, "\r", "")
	args = strings.ReplaceAll(args, "\t", "")

	// Collapse multiple spaces
	for strings.Contains(args, "  ") {
		args = strings.ReplaceAll(args, "  ", " ")
	}

	return args
}
