# Testing with Ollama (Llama 3.1)

This guide shows how to run automated tests using Ollama with local models like Llama 3.1 instead of OpenAI or Claude APIs.

## Prerequisites

1. **Install Ollama**: https://ollama.ai
2. **Start Ollama server**:
   ```bash
   ollama serve
   ```
3. **Pull Llama 3.1** (or any model you want):
   ```bash
   ollama pull llama3.1
   ```

## Quick Start

### Option 1: Using Make (Recommended)

```bash
# Run tests with Ollama (defaults to llama3.1)
make test-ollama

# Run with verbose output
TEST_VERBOSE=true make test-ollama

# Use a different model
OLLAMA_MODEL=llama3.1:70b make test-ollama
```

### Option 2: Using the Script Directly

```bash
# Run with default settings (llama3.1)
./scripts/test-with-ollama.sh

# Specify a different model
./scripts/test-with-ollama.sh --model llama3.2

# Use verbose output
./scripts/test-with-ollama.sh --verbose

# Custom Ollama host
./scripts/test-with-ollama.sh --host http://192.168.1.100:11434
```

### Option 3: Manual Test Execution

```bash
# Build everything first
make build plugins

# Set environment variables
export USE_OLLAMA=true
export OLLAMA_MODEL=llama3.1
export OLLAMA_HOST=http://localhost:11434

# Run tests
go test -v -timeout 10m ./tests/user/...
```

## Available Models

Common Ollama models you can use:

- **llama3.1** - Meta's Llama 3.1 (8B, recommended)
- **llama3.1:70b** - Llama 3.1 70B (better quality, slower)
- **llama3.2** - Llama 3.2 (latest)
- **mistral** - Mistral 7B
- **mixtral** - Mixtral 8x7B
- **codellama** - Code-specialized Llama
- **phi** - Microsoft Phi-2

List available models:
```bash
ollama list
```

Pull a new model:
```bash
ollama pull llama3.1
```

## What Gets Tested

The test suite creates agents, enables plugins, and sends prompts just like a real user:

### Workflow Tests
- ✅ Create agent with Llama 3.1
- ✅ Send simple chat messages
- ✅ Enable math plugin and test calculations
- ✅ Enable multiple plugins
- ✅ Test multiple agents independently
- ✅ Test error handling

### Plugin Tests
- ✅ Math plugin integration (addition, multiplication, division)
- ✅ Weather plugin integration
- ✅ Multiple plugins on one agent
- ✅ Agent-aware plugin contexts
- ✅ All shared plugins (music, reaper, macOS tools, etc.)

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `USE_OLLAMA` | `false` | Enable Ollama provider |
| `OLLAMA_MODEL` | `llama3.1` | Model to use for tests |
| `OLLAMA_HOST` | `http://localhost:11434` | Ollama server URL |
| `TEST_VERBOSE` | `false` | Show detailed test output |
| `TEST_CLEANUP` | `true` | Clean up test artifacts after run |

## Troubleshooting

### "Cannot connect to Ollama"

Make sure Ollama is running:
```bash
ollama serve
```

Check if it's accessible:
```bash
curl http://localhost:11434
```

### "Model not found"

Pull the model first:
```bash
ollama pull llama3.1
```

### Tests are slow

Local models are slower than cloud APIs. You can:
- Use a smaller model (llama3.1:8b vs llama3.1:70b)
- Reduce timeout: `go test -timeout 20m ...`
- Run fewer tests: `go test -run TestCompleteAgentWorkflow ...`

### Model gives wrong answers

Some smaller models may struggle with tool calling. Try:
- Using a larger model: `llama3.1:70b` or `mixtral`
- Adjusting temperature in agent settings
- Testing with simpler prompts first

## Performance Tips

1. **Use quantized models** for faster inference:
   ```bash
   ollama pull llama3.1:8b-q4_0
   ```

2. **Run specific tests** instead of the full suite:
   ```bash
   go test -v ./tests/user/workflows/... -run TestAgentWithPluginWorkflow
   ```

3. **Increase timeout** for large models:
   ```bash
   go test -timeout 20m ./tests/user/...
   ```

## Comparison: Ollama vs OpenAI/Claude

| Feature | Ollama | OpenAI/Claude |
|---------|--------|---------------|
| **Cost** | Free (local) | Paid API |
| **Privacy** | 100% local | Data sent to cloud |
| **Speed** | Slower (local CPU/GPU) | Faster (cloud GPUs) |
| **Quality** | Good (smaller models) | Excellent |
| **Setup** | Requires installation | Just API key |
| **Internet** | Not required | Required |

## Examples

### Run all tests with Llama 3.1:
```bash
make test-ollama
```

### Run specific test with Mistral:
```bash
OLLAMA_MODEL=mistral go test -v ./tests/user/workflows/... \
  -run TestAgentWithPluginWorkflow
```

### Run with verbose output and custom host:
```bash
./scripts/test-with-ollama.sh \
  --model llama3.2 \
  --host http://192.168.1.100:11434 \
  --verbose
```

## See Also

- [Main Testing Guide](../TESTING.md)
- [Test Cheatsheet](TEST_CHEATSHEET.md)
- [Ollama Documentation](https://ollama.ai/docs)
- [LLM Provider Guide](../../internal/llm/README.md)
