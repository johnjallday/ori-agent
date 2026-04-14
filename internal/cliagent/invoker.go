package cliagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// OutputFormat identifies how CLI output should be parsed.
type OutputFormat string

const (
	FormatClaudeStreamJSON OutputFormat = "claude_stream_json" // Newline-delimited JSON from claude --output-format stream-json
	FormatCodexJSONL       OutputFormat = "codex_jsonl"        // JSONL from codex exec --json
	FormatGeminiStreamJSON OutputFormat = "gemini_stream_json" // Newline-delimited JSON from gemini --output-format stream-json
)

// CLIInvocation describes a single CLI process to run.
type CLIInvocation struct {
	CLIPath    string
	Args       []string
	WorkingDir string
	Timeout    time.Duration
	Format     OutputFormat
	OutputFile string // Optional file where CLI writes output (Codex --output-last-message)
}

// RawCLIOutput holds the parsed output from a CLI invocation.
type RawCLIOutput struct {
	Events []CLIEvent
	Output string    // Final text output
	Usage  StepUsage // Parsed token usage
	Stderr string
}

// CLIInvoker handles running CLI processes and parsing their output.
type CLIInvoker struct{}

// NewCLIInvoker creates a new CLIInvoker.
func NewCLIInvoker() *CLIInvoker {
	return &CLIInvoker{}
}

// Invoke runs a CLI process and parses its output according to the format.
func (inv *CLIInvoker) Invoke(ctx context.Context, invocation CLIInvocation) (*RawCLIOutput, error) {
	timeout := invocation.Timeout
	if timeout == 0 {
		timeout = DefaultStepTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, invocation.CLIPath, invocation.Args...)
	if invocation.WorkingDir != "" {
		cmd.Dir = invocation.WorkingDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr == "" {
			stderrStr = strings.TrimSpace(stdout.String())
		}
		if stderrStr == "" {
			stderrStr = err.Error()
		}
		return nil, fmt.Errorf("cli invocation failed: %s", stderrStr)
	}

	switch invocation.Format {
	case FormatClaudeStreamJSON:
		return parseClaudeStreamJSON(stdout.Bytes(), stderr.String())
	case FormatCodexJSONL:
		return parseCodexJSONL(stdout.Bytes(), stderr.String(), invocation.OutputFile)
	case FormatGeminiStreamJSON:
		return parseGeminiStreamJSON(stdout.Bytes(), stderr.String())
	default:
		// Plain text fallback
		return &RawCLIOutput{
			Output: strings.TrimSpace(stdout.String()),
			Stderr: strings.TrimSpace(stderr.String()),
		}, nil
	}
}

// parseClaudeStreamJSON parses Claude's stream-json output.
// Each line is a JSON object with a "type" field.
func parseClaudeStreamJSON(data []byte, stderrStr string) (*RawCLIOutput, error) {
	raw := &RawCLIOutput{Stderr: stderrStr}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	var lastText string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue // skip malformed lines
		}

		eventType, _ := obj["type"].(string)

		event := CLIEvent{
			Timestamp: time.Now(),
			Type:      eventType,
			Payload:   obj,
		}

		switch eventType {
		case "assistant":
			if msg, ok := obj["message"].(map[string]any); ok {
				if content, ok := msg["content"].([]any); ok {
					for _, block := range content {
						if b, ok := block.(map[string]any); ok {
							if text, ok := b["text"].(string); ok {
								lastText = text
								event.Content = text
							}
						}
					}
				}
			}
		case "result":
			if result, ok := obj["result"].(string); ok {
				lastText = result
				event.Content = result
			}
			raw.Usage = parseClaudeUsageFromResult(obj)
		}

		raw.Events = append(raw.Events, event)
	}

	raw.Output = lastText
	return raw, nil
}

// parseClaudeUsageFromResult extracts usage from a Claude result event.
func parseClaudeUsageFromResult(obj map[string]any) StepUsage {
	var usage StepUsage

	// Try top-level usage
	if u, ok := obj["usage"].(map[string]any); ok {
		if v, ok := u["input_tokens"].(float64); ok {
			usage.InputTokens = int(v)
		}
		if v, ok := u["output_tokens"].(float64); ok {
			usage.OutputTokens = int(v)
		}
	}

	// Try total_cost_usd
	if cost, ok := obj["total_cost_usd"].(float64); ok {
		usage.CostUSD = cost
	}

	return usage
}

// parseCodexJSONL parses Codex's JSONL output.
func parseCodexJSONL(data []byte, stderrStr string, outputFile string) (*RawCLIOutput, error) {
	raw := &RawCLIOutput{Stderr: stderrStr}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	var lastText string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}

		eventType, _ := obj["type"].(string)

		event := CLIEvent{
			Timestamp: time.Now(),
			Type:      eventType,
			Payload:   obj,
		}

		switch eventType {
		case "item.completed":
			if item, ok := obj["item"].(map[string]any); ok {
				if text, ok := item["text"].(string); ok {
					lastText = text
					event.Content = text
				}
			}
		case "turn.completed":
			if u, ok := obj["usage"].(map[string]any); ok {
				if v, ok := u["input_tokens"].(float64); ok {
					raw.Usage.InputTokens = int(v)
				}
				if v, ok := u["output_tokens"].(float64); ok {
					raw.Usage.OutputTokens = int(v)
				}
			}
		}

		raw.Events = append(raw.Events, event)
	}

	// Try reading output from the output file if available
	if outputFile != "" {
		if fileData, err := os.ReadFile(outputFile); err == nil {
			if text := strings.TrimSpace(string(fileData)); text != "" {
				lastText = text
			}
		}
	}

	raw.Output = lastText
	return raw, nil
}

// parseGeminiStreamJSON parses Gemini CLI's stream-json output.
// Each line is a JSON object with a "type" field (init, message, tool_use, tool_result, result).
func parseGeminiStreamJSON(data []byte, stderrStr string) (*RawCLIOutput, error) {
	raw := &RawCLIOutput{Stderr: stderrStr}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	var lastText string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue // skip non-JSON lines (e.g. YOLO warnings on stderr mixed into stdout)
		}

		eventType, _ := obj["type"].(string)

		event := CLIEvent{
			Timestamp: time.Now(),
			Type:      eventType,
			Payload:   obj,
		}

		switch eventType {
		case "message":
			role, _ := obj["role"].(string)
			if role == "assistant" {
				if content, ok := obj["content"].(string); ok {
					lastText = content
					event.Content = content
				}
			}
		case "result":
			raw.Usage = parseGeminiUsageFromResult(obj)
		}

		raw.Events = append(raw.Events, event)
	}

	raw.Output = lastText
	return raw, nil
}

// parseGeminiUsageFromResult extracts usage from a Gemini result event.
func parseGeminiUsageFromResult(obj map[string]any) StepUsage {
	var usage StepUsage

	stats, ok := obj["stats"].(map[string]any)
	if !ok {
		return usage
	}

	if v, ok := stats["input_tokens"].(float64); ok {
		usage.InputTokens = int(v)
	}
	if v, ok := stats["output_tokens"].(float64); ok {
		usage.OutputTokens = int(v)
	}
	// Gemini free tier doesn't report cost, leave CostUSD at 0

	return usage
}

// mapRawToStepResult converts a RawCLIOutput to a StepResult.
func mapRawToStepResult(stepNumber int, raw *RawCLIOutput) *StepResult {
	status := StepCompleted
	if raw.Output == "" && len(raw.Events) == 0 {
		status = StepFailed
	}

	return &StepResult{
		StepNumber: stepNumber,
		Output:     raw.Output,
		Events:     raw.Events,
		Usage:      raw.Usage,
		Status:     status,
	}
}
