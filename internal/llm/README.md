# LLM Provider Abstraction Layer

This package provides a unified interface for interacting with multiple Large Language Model (LLM) providers including OpenAI, Claude, Ollama, and more.

## Overview

The provider abstraction layer allows Dolphin Agent to support multiple LLM providers through a clean, consistent interface. This design makes it easy to:

- Switch between providers without changing business logic
- Add new providers with minimal code changes
- Test provider implementations independently
- Support both cloud and local LLM providers

## Architecture

```
┌─────────────────────────────────────┐
│       Application Layer             │
└────────────────┬────────────────────┘
                 │
      ┌──────────▼──────────┐
      │  Provider Factory   │
      │    (Registry)       │
      └──────────┬──────────┘
                 │
         ┌───────┴───────┐
         │               │
    ┌────▼────┐    ┌────▼────┐
    │Provider │    │Provider │
    │   A     │    │   B     │
    └─────────┘    └─────────┘
```

## Core Interfaces

### Provider Interface

The main interface that all LLM providers must implement:

```go
type Provider interface {
    Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
    StreamChat(ctx context.Context, req ChatRequest) (StreamReader, error)
    Name() string
    Type() ProviderType
    Capabilities() ProviderCapabilities
    ValidateConfig(config ProviderConfig) error
    DefaultModels() []string
}
```

### Provider Types

- **Cloud**: Hosted services like OpenAI, Claude, Gemini
- **Local**: Self-hosted models like Ollama, LocalAI
- **Hybrid**: Can be both cloud and local

## Usage

### Creating a Factory

```go
// Create a new factory
factory := llm.NewFactory()

// Register providers
factory.Register("openai", openaiProvider)
factory.Register("claude", claudeProvider)
factory.Register("ollama", ollamaProvider)
```

### Getting a Provider

```go
// Get a specific provider
provider, err := factory.GetProvider("openai")
if err != nil {
    log.Fatal(err)
}

// Use the provider
resp, err := provider.Chat(ctx, llm.ChatRequest{
    Model: "gpt-4o",
    Messages: []llm.Message{
        llm.NewUserMessage("Hello, world!"),
    },
    Temperature: 0.7,
})
```

### Making Chat Requests

```go
req := llm.ChatRequest{
    Model:        "gpt-4o",
    Messages:     messages,
    Tools:        tools,
    Temperature:  0.7,
    SystemPrompt: "You are a helpful assistant",
    MaxTokens:    1000,
}

resp, err := provider.Chat(ctx, req)
if err != nil {
    // Handle error
}

// Check response
if llm.HasContent(resp) {
    fmt.Println("Response:", resp.Content)
}

if llm.IsToolCallResponse(resp) {
    // Handle tool calls
    for _, tc := range resp.ToolCalls {
        fmt.Printf("Tool: %s, Args: %s\n", tc.Name, tc.Arguments)
    }
}
```

### Working with Messages

```go
// Create messages using helpers
messages := []llm.Message{
    llm.NewSystemMessage("You are a helpful assistant"),
    llm.NewUserMessage("What is 2+2?"),
    llm.NewAssistantMessage("The answer is 4."),
    llm.NewUserMessage("Thank you!"),
}
```

### Tool Calling

```go
// Define tools
tools := []llm.Tool{
    {
        Name:        "calculator",
        Description: "Perform basic arithmetic",
        Parameters: map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "operation": map[string]interface{}{
                    "type": "string",
                    "enum": []string{"add", "subtract", "multiply", "divide"},
                },
                "a": map[string]interface{}{"type": "number"},
                "b": map[string]interface{}{"type": "number"},
            },
            "required": []string{"operation", "a", "b"},
        },
    },
}

// Include tools in request
req := llm.ChatRequest{
    Model:    "gpt-4o",
    Messages: messages,
    Tools:    tools,
}

resp, err := provider.Chat(ctx, req)

// Handle tool calls
if llm.IsToolCallResponse(resp) {
    for _, tc := range resp.ToolCalls {
        // Parse arguments
        var args struct {
            Operation string  `json:"operation"`
            A         float64 `json:"a"`
            B         float64 `json:"b"`
        }

        err := llm.ParseToolArguments(tc.Arguments, &args)
        if err != nil {
            continue
        }

        // Execute tool
        result := executeCalculator(args)

        // Add tool result to messages
        messages = append(messages, llm.NewToolMessage(tc.ID, result))
    }
}
```

## Provider Capabilities

Each provider reports its capabilities:

```go
caps := provider.Capabilities()

if caps.SupportsTools {
    // Provider supports function calling
}

if caps.SupportsStreaming {
    // Provider supports streaming responses
}

if caps.RequiresAPIKey {
    // Provider requires an API key
}

fmt.Printf("Max context window: %d tokens\n", caps.MaxContextWindow)
```

## Listing Providers

```go
// Get all registered providers
providers := factory.ListProviders()

for _, info := range providers {
    fmt.Printf("Provider: %s\n", info.Name)
    fmt.Printf("  Type: %s\n", info.Type)
    fmt.Printf("  Models: %v\n", info.Models)
    fmt.Printf("  Supports Tools: %v\n", info.Capabilities.SupportsTools)
}
```

## Testing

The package includes a MockProvider for testing:

```go
func TestMyFeature(t *testing.T) {
    factory := llm.NewFactory()

    // Create mock provider
    mock := &llm.MockProvider{
        name: "mock",
        chatFunc: func(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
            return &llm.ChatResponse{
                Content: "Test response",
                Provider: "mock",
            }, nil
        },
    }

    factory.Register("mock", mock)

    // Test your code with the mock
}
```

## Error Handling

```go
resp, err := provider.Chat(ctx, req)
if err != nil {
    // Check error type
    switch {
    case strings.Contains(err.Error(), "rate limit"):
        // Handle rate limiting
    case strings.Contains(err.Error(), "timeout"):
        // Handle timeout
    default:
        // Handle other errors
    }
}

// Check finish reason
switch resp.FinishReason {
case llm.FinishReasonStop:
    // Normal completion
case llm.FinishReasonLength:
    // Hit max tokens
case llm.FinishReasonToolCalls:
    // Model wants to call tools
case llm.FinishReasonContentFilter:
    // Content was filtered
}
```

## Best Practices

1. **Always check capabilities** before using features:
   ```go
   if !provider.Capabilities().SupportsTools {
       return errors.New("provider doesn't support tools")
   }
   ```

2. **Use context for cancellation and timeouts**:
   ```go
   ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
   defer cancel()

   resp, err := provider.Chat(ctx, req)
   ```

3. **Handle tool calls in a loop** until the model produces a final response:
   ```go
   for {
       resp, err := provider.Chat(ctx, req)
       if err != nil {
           return err
       }

       if !llm.IsToolCallResponse(resp) {
           // Final response
           return resp, nil
       }

       // Execute tools and continue
       req.Messages = append(req.Messages, executeTools(resp.ToolCalls)...)
   }
   ```

4. **Validate configuration** before using a provider:
   ```go
   if err := provider.ValidateConfig(config); err != nil {
       return fmt.Errorf("invalid config: %w", err)
   }
   ```

## Adding a New Provider

To add a new provider:

1. Implement the `Provider` interface
2. Define provider-specific configuration
3. Handle format conversion (request/response)
4. Register with the factory
5. Write tests

Example:

```go
type MyProvider struct {
    apiKey string
    client *http.Client
}

func NewMyProvider(config llm.ProviderConfig) *MyProvider {
    return &MyProvider{
        apiKey: config.APIKey,
        client: &http.Client{},
    }
}

func (p *MyProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
    // Convert request to provider format
    // Make API call
    // Convert response to standard format
    return &llm.ChatResponse{
        Content:  "...",
        Provider: "myprovider",
    }, nil
}

// Implement other interface methods...
```

## Future Enhancements

- Streaming support with `StreamChat`
- Provider health checks
- Automatic failover between providers
- Cost tracking per provider
- Performance metrics
- Provider-specific optimizations

## See Also

- [CLAUDE_INTEGRATION_PLAN.md](../../../CLAUDE_INTEGRATION_PLAN.md) - Full Claude integration plan
- [MULTI_PROVIDER_STRATEGY.md](../../../MULTI_PROVIDER_STRATEGY.md) - Multi-provider strategy
