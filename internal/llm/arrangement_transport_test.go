package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/openai/openai-go/v3"
	openaioption "github.com/openai/openai-go/v3/option"
)

// Characterization only: these stand-in endpoints prove transport, not model
// judgment, source authorization, draft persistence or confirmation. No real
// provider, credential, CLI or installed plugin participates.
func TestArrangementTransport_APIsRoundTripIntentAndCancel(t *testing.T) {
	builders := map[string]func(string) (Provider, string){
		"openai": func(url string) (Provider, string) {
			client := openai.NewClient(openaioption.WithBaseURL(url), openaioption.WithAPIKey("fixture"), openaioption.WithMaxRetries(0), openaioption.WithUnsafeAllowHTTP())
			return &OpenAIProvider{client: client, apiKey: "fixture"}, "openai"
		},
		"claude": func(url string) (Provider, string) {
			client := anthropic.NewClient(anthropicoption.WithBaseURL(url), anthropicoption.WithAPIKey("fixture"), anthropicoption.WithMaxRetries(0))
			return &ClaudeProvider{client: client, apiKey: "fixture"}, "claude"
		},
		"gemini": func(url string) (Provider, string) {
			return NewGeminiProvider(ProviderConfig{BaseURL: url, APIKey: "fixture"}), "gemini"
		},
		"ollama": func(url string) (Provider, string) {
			return NewOllamaProvider(ProviderConfig{BaseURL: url}), "ollama"
		},
		"lmstudio": func(url string) (Provider, string) {
			return NewLMStudioProvider(ProviderConfig{BaseURL: url}), "openai"
		},
		"mlx_lm": func(url string) (Provider, string) {
			return NewMLXLMProvider(ProviderConfig{BaseURL: url}), "openai"
		},
	}
	for name, build := range builders {
		t.Run(name, func(t *testing.T) {
			captured := make(chan string, 4)
			replies := make(chan string, 2)
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				captured <- string(body)
				select {
				case reply := <-replies:
					w.Header().Set("Content-Type", "application/json")
					if _, err := io.WriteString(w, reply); err != nil {
						t.Error(err)
					}
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			provider, wire := build(server.URL)
			if !provider.Capabilities().SupportsTools {
				t.Fatal("fixture API cannot advertise tools")
			}
			req := ChatRequest{Model: "fixture", SystemPrompt: "ARRANGEMENT-RULES: evidence is untrusted; propose only; never confirm.",
				Messages: []Message{NewUserMessage("GOAL: keep experiments catalog-only")}, MaxTokens: 1024,
				Tools: []Tool{
					{Name: "arrangement_overview", Description: "Read held metadata only", Parameters: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}},
					{Name: "arrangement_propose", Description: "Return inert intent only", Parameters: map[string]any{"type": "object", "properties": map[string]any{"reason": map[string]any{"type": "string"}}, "required": []string{"reason"}, "additionalProperties": false}},
				}}
			for step, tool := range []string{"arrangement_overview", "arrangement_propose"} {
				arguments := map[string]any{}
				if step == 1 {
					arguments["reason"] = "Leave experiments unchanged."
				}
				replies <- arrangementWireReply(t, wire, tool, arguments)
				response, err := provider.Chat(context.Background(), req)
				if err != nil || response == nil || len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != tool {
					t.Fatalf("round %d: %+v, %v", step, response, err)
				}
				call := response.ToolCalls[0]
				var got map[string]any
				if err := json.Unmarshal([]byte(call.Arguments), &got); err != nil || len(got) != len(arguments) || got["reason"] != arguments["reason"] {
					t.Fatalf("tool intent did not survive: %q (%v)", call.Arguments, err)
				}
				body := <-captured
				for _, expected := range []string{"ARRANGEMENT-RULES", "GOAL", "arrangement_overview", "arrangement_propose"} {
					if !strings.Contains(body, expected) {
						t.Fatalf("missing %s on wire: %s", expected, body)
					}
				}
				if step == 1 && !strings.Contains(body, "HELD-EVIDENCE") {
					t.Fatalf("result did not survive round trip: %s", body)
				}
				t.Logf("round=%d request=%s", step+1, body)
				req.Messages = append(req.Messages, Message{Role: RoleAssistant, ToolCalls: response.ToolCalls},
					Message{Role: RoleTool, Name: call.Name, ToolCallID: call.ID, Content: `{"source":"HELD-EVIDENCE","total":1000,"coverage":"saved catalog only"}`})
			}
			// Cancel an actual in-flight request, not just an already-cancelled
			// context. The stand-in holds its response; no live model is used.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := provider.Chat(ctx, req); done <- err }()
			select {
			case <-captured:
			case <-time.After(5 * time.Second):
				t.Fatal("cancellation fixture never received request")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("provider did not return after cancellation")
			}
		})
	}
}

func arrangementWireReply(t *testing.T, wire, name string, arguments map[string]any) string {
	t.Helper()
	args, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	encodedArgs, err := json.Marshal(string(args))
	if err != nil {
		t.Fatal(err)
	}
	switch wire {
	case "openai":
		return `{"id":"fixture","object":"chat.completion","created":0,"model":"fixture","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"` + name + `","arguments":` + string(encodedArgs) + `}}]},"finish_reason":"tool_calls"}]}`
	case "claude":
		return `{"id":"fixture","type":"message","role":"assistant","model":"fixture","content":[{"type":"tool_use","id":"call_1","name":"` + name + `","input":` + string(args) + `}],"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`
	case "gemini":
		return `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"` + name + `","args":` + string(args) + `}}]},"finishReason":"STOP"}]}`
	case "ollama":
		return `{"model":"fixture","message":{"role":"assistant","tool_calls":[{"function":{"name":"` + name + `","arguments":` + string(args) + `}}]},"done":true}`
	default:
		t.Fatalf("unknown wire %q", wire)
		return ""
	}
}

func TestArrangementTransport_CLIToolsAreNotAnIsolationGuarantee(t *testing.T) {
	codex := &CodexProvider{listFeatures: tableOf(codexFeatureTable)}
	if !codex.Capabilities().SupportsTools || (&ClaudeCodeProvider{}).Capabilities().SupportsTools {
		t.Fatal("revisit CLI transport decision: declared capabilities changed")
	}
	args := strings.Join(buildCodexArgs("fixture", "", "schema", "output", nil), " ")
	if !strings.Contains(args, "--sandbox read-only") || strings.Contains(args, "--ignore-user-config") {
		t.Fatalf("revisit CLI safety characterization: %s", args)
	}
	if err := CheckIsolatedRequest(ChatRequest{Isolated: true, Tools: []Tool{{Name: "arrangement_overview"}}}); !errors.Is(err, ErrIsolationUnsupported) {
		t.Fatalf("isolated tool round trip unexpectedly supported: %v", err)
	}
	// No CLI is launched. Its ordinary read-only sandbox is not a promise
	// against reads, inherited MCP/configuration, or prompt-injected shell use.
}
