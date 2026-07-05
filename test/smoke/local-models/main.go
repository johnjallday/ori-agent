// Command local-models is a manual smoke/metrics harness for constrained
// structured output on local models (PRD WS9.36). It drives real Ollama models
// with each fixture's JSON schema and reports the first-pass schema-valid rate
// and wall time — the evidence for Success Metric 2.
//
// It is NOT run in CI (it needs a running Ollama with the models pulled). Run it
// manually:
//
//	# Baseline (prompt-only, schema disabled) — capture BEFORE trusting WS3:
//	go run ./test/smoke/local-models -baseline
//
//	# Constrained decoding (schema on) — the WS3 result to compare:
//	go run ./test/smoke/local-models
//
//	# Options:
//	go run ./test/smoke/local-models -models llama3.1:8b,qwen2.5:7b-instruct -runs 3
//
// Env: OLLAMA_BASE_URL (default http://localhost:11434).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
)

// fixture is one output-spec task: a prompt plus the JSON schema its answer must
// satisfy and the keys that must be present with the right kind of value.
type fixture struct {
	name   string
	prompt string
	schema map[string]any
	// required maps a required key to its expected JSON kind (string/number/bool).
	required map[string]string
}

func fixtures() []fixture {
	return []fixture{
		{
			name:     "extract_weather",
			prompt:   "Extract the city and Fahrenheit temperature from this sentence and return JSON: \"It is 72F in Boston right now.\"",
			schema:   objSchema(map[string]string{"city": "string", "temperature_f": "number"}, "city", "temperature_f"),
			required: map[string]string{"city": "string", "temperature_f": "number"},
		},
		{
			name:     "classify_sentiment",
			prompt:   "Classify the sentiment of this review as positive, negative, or neutral, with a 0-1 confidence: \"I absolutely love this product.\"",
			schema:   objSchema(map[string]string{"sentiment": "string", "confidence": "number"}, "sentiment", "confidence"),
			required: map[string]string{"sentiment": "string", "confidence": "number"},
		},
		{
			name:     "pick_priority_task",
			prompt:   "From these tasks pick the highest priority one (higher number = higher priority): A (priority 1), B (priority 5), C (priority 3). Return the task name and its priority.",
			schema:   objSchema(map[string]string{"task": "string", "priority": "integer"}, "task", "priority"),
			required: map[string]string{"task": "string", "priority": "number"},
		},
	}
}

func objSchema(fields map[string]string, required ...string) map[string]any {
	props := map[string]any{}
	for name, typ := range fields {
		props[name] = map[string]any{"type": typ}
	}
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func main() {
	baseline := flag.Bool("baseline", false, "disable the schema (prompt-only) to capture the pre-WS3 baseline")
	modelsCSV := flag.String("models", "llama3.1:8b,qwen2.5:7b-instruct", "comma-separated Ollama models")
	runs := flag.Int("runs", 1, "runs per fixture (schema validity is averaged)")
	flag.Parse()

	baseURL := os.Getenv("OLLAMA_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	provider := llm.NewOllamaProvider(llm.ProviderConfig{BaseURL: baseURL})

	models := splitCSV(*modelsCSV)
	fx := fixtures()

	mode := "constrained (schema on)"
	if *baseline {
		mode = "baseline (prompt-only)"
	}
	fmt.Printf("Local structured-output smoke — %s\n", mode)
	fmt.Printf("Server: %s   models: %s   runs/fixture: %d\n\n", baseURL, strings.Join(models, ", "), *runs)

	var grandTotal, grandPass int
	for _, model := range models {
		var total, pass int
		var elapsed time.Duration
		for _, f := range fx {
			for r := 0; r < *runs; r++ {
				ok, dur := runOne(provider, model, f, *baseline)
				total++
				elapsed += dur
				if ok {
					pass++
				}
			}
		}
		grandTotal += total
		grandPass += pass
		rate := 0.0
		if total > 0 {
			rate = 100 * float64(pass) / float64(total)
		}
		avg := time.Duration(0)
		if total > 0 {
			avg = elapsed / time.Duration(total)
		}
		fmt.Printf("  %-28s  schema-valid %3d/%-3d (%5.1f%%)   avg %v\n", model, pass, total, rate, avg.Round(time.Millisecond))
	}

	rate := 0.0
	if grandTotal > 0 {
		rate = 100 * float64(grandPass) / float64(grandTotal)
	}
	fmt.Printf("\nOverall first-pass schema-valid: %d/%d (%.1f%%)\n", grandPass, grandTotal, rate)
	if !*baseline && rate < 90.0 {
		fmt.Println("NOTE: below the 90% Success-Metric-2 target.")
	}
}

// runOne sends one fixture and reports whether the answer is first-pass
// schema-valid (parses as a JSON object with the required keys present and of
// the expected kind) and how long the call took.
func runOne(p *llm.OllamaProvider, model string, f fixture, baseline bool) (bool, time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	req := llm.ChatRequest{
		Model: model,
		Messages: []llm.Message{
			llm.NewSystemMessage("Return only a single JSON object. No markdown fences, no commentary."),
			llm.NewUserMessage(f.prompt),
		},
		Temperature: 0,
	}
	if !baseline {
		req.ResponseSchema = f.schema
	}

	start := time.Now()
	resp, err := p.Chat(ctx, req)
	dur := time.Since(start)
	if err != nil {
		fmt.Printf("    [%s/%s] error: %v\n", model, f.name, err)
		return false, dur
	}
	return schemaValid(resp.Content, f.required), dur
}

// schemaValid reports whether content is a JSON object containing every required
// key with a value of the expected kind.
func schemaValid(content string, required map[string]string) bool {
	content = llm.StripCodeFence(content)
	var obj map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &obj); err != nil {
		return false
	}
	for key, kind := range required {
		v, ok := obj[key]
		if !ok {
			return false
		}
		switch kind {
		case "string":
			if _, ok := v.(string); !ok {
				return false
			}
		case "number":
			if _, ok := v.(float64); !ok {
				return false
			}
		case "bool":
			if _, ok := v.(bool); !ok {
				return false
			}
		}
	}
	return true
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
