# Multi-Provider Implementation - Next Phases

## Completed ✅

### Phase 4: Claude Provider Implementation
- ✅ Added Claude provider with Anthropic SDK (anthropic-sdk-go v1.14.0)
- ✅ Added Claude models to frontend dropdown
  - Claude 3.5 Sonnet (Oct 2024, Jun 2024)
  - Claude 3.5 Haiku
  - Claude 3 Opus, Sonnet, Haiku
- ✅ Implemented model-based routing (detects `claude-` prefix)
- ✅ Simple Claude chat handler (text responses only)
- ✅ Config support for ANTHROPIC_API_KEY (settings.json + env var)
- ✅ Increased timeouts to 3 minutes for complex requests
- ✅ Updated config manager to support both OpenAI and Anthropic API keys

**Files Modified:**
- `internal/llm/claude_provider.go` - Claude provider implementation
- `internal/llm/claude_provider_test.go` - Comprehensive tests
- `internal/server/server.go` - Claude provider registration
- `internal/config/config.go` - Added AnthropicAPIKey field and GetAnthropicAPIKey()
- `internal/chathttp/handlers.go` - Added handleClaudeChat() method
- `internal/web/static/js/modules/agents.js` - Added Claude models to dropdown
- `examples/provider_chat.go` - Multi-provider example

## What's Missing / Next Phases

### Phase 5: Tool Calling for Claude (High Priority)

**Problem:** Currently, the Claude handler doesn't support tool calling. If you have plugins enabled, Claude won't be able to use them.

**What's Needed:**
- Add tool call handling in `handleClaudeChat` method
  - Check if `resp.ToolCalls` is present
  - Execute tool calls with proper error handling
  - Convert tool results back to messages
- Handle multi-turn tool execution (similar to OpenAI flow)
- Test with existing plugins (reaper, github, etc.)

**Files to Modify:**
- `internal/chathttp/handlers.go` - Extend `handleClaudeChat()` method

**Estimated Effort:** 2-3 hours

---

### Phase 6: Conversation History for Claude (High Priority)

**Problem:** Claude only sees the system message + current user message. It doesn't see the conversation history, so multi-turn conversations don't work properly.

**What's Needed:**
- Convert the full `ag.Messages` history to unified `llm.Message` format
- Pass conversation history in each Claude request
- Handle both text and tool messages in history
- Preserve context across multiple messages

**Files to Modify:**
- `internal/chathttp/handlers.go` - Update `handleClaudeChat()` to include history

**Estimated Effort:** 1-2 hours

---

### Phase 7: Provider Selection UI (Medium Priority)

**Problem:** Provider is auto-detected by model name. No way to configure or see which provider is being used.

**What's Needed:**
- Settings page updates:
  - Configure provider preferences
  - Show API key status for each provider (OpenAI, Claude)
  - Set/update API keys via UI
- Chat UI updates:
  - Show which provider is being used
  - Allow manual provider override
  - Provider-specific model filtering

**Files to Modify:**
- `internal/web/static/js/modules/settings.js` - Add provider settings UI
- `internal/settingshttp/handlers.go` - Add endpoints for provider config
- `internal/web/templates/*.tmpl` - Update settings UI

**Estimated Effort:** 4-6 hours

---

### Phase 8: Ollama Provider (Optional)

**Goal:** Add local model support via Ollama.

**What's Needed:**
- Implement `OllamaProvider` (similar to Claude/OpenAI providers)
- No API key required
- Custom endpoint configuration (default: http://localhost:11434)
- Support popular models:
  - llama3, llama3.1, llama3.2
  - mistral, mixtral
  - codellama, deepseek-coder
- Model auto-discovery via Ollama API

**Files to Create/Modify:**
- `internal/llm/ollama_provider.go` - New provider implementation
- `internal/llm/ollama_provider_test.go` - Tests
- `internal/server/server.go` - Register Ollama provider
- `internal/config/config.go` - Add OllamaEndpoint field

**Estimated Effort:** 1 day

---

### Phase 9: Advanced Features (Optional)

**Streaming Responses:**
- Implement `StreamChat()` for Claude and OpenAI providers
- Update frontend to handle SSE (Server-Sent Events)
- Show partial responses as they arrive

**Provider Failover:**
- Automatic failover if primary provider fails
- Configurable fallback chain (e.g., GPT-4 → Claude → GPT-3.5)

**Cost Tracking:**
- Track token usage per provider
- Estimate costs based on provider pricing
- Show cost analytics in UI

**Performance Comparison:**
- Track response times per provider
- Compare quality/speed trade-offs
- Provider benchmarking tools

**Estimated Effort:** 1-2 weeks total

---

## Recommended Priority

### Immediate (to make Claude fully functional):
1. **Phase 5** - Tool calling for Claude (so it can use your plugins)
2. **Phase 6** - Conversation history for Claude (so multi-turn works)

### Short-term:
3. **Phase 7** - Provider selection UI (better UX)

### Long-term:
4. **Phase 8** - Ollama (local models)
5. **Phase 9** - Advanced features

---

## Current Status

**What Works:**
- ✅ Claude text responses (single-turn)
- ✅ Model selection in UI
- ✅ Automatic provider routing based on model name
- ✅ Claude provider registration with API key
- ✅ Both OpenAI and Claude configured side-by-side

**What Doesn't Work:**
- ❌ Claude tool calling (plugins won't work with Claude)
- ❌ Claude conversation history (no multi-turn context)
- ❌ Streaming responses for Claude
- ⚠️  Limited error handling for provider failures

---

## Technical Notes

### Provider Detection Logic
Currently in `internal/chathttp/handlers.go:301-305`:
```go
if strings.HasPrefix(ag.Settings.Model, "claude-") && h.llmFactory != nil {
    h.handleClaudeChat(w, r, ag, q, tools, current, base)
    return
}
```

This works but is brittle. Consider:
- Adding a `Provider` field to agent settings
- Using provider registry to map models to providers
- Supporting custom model names

### API Key Management
API keys are loaded from:
1. `settings.json` (persistent)
2. Environment variables (fallback)

Both are checked via `config.Manager`:
- `GetAPIKey()` - OpenAI
- `GetAnthropicAPIKey()` - Claude

### Message History Format
OpenAI messages are stored in `ag.Messages` as `[]openai.ChatCompletionMessageParamUnion`.
To support Claude, we need to:
1. Convert history when calling Claude
2. Store responses back in OpenAI format (for compatibility)
3. Eventually consider a unified storage format

---

## Questions / Considerations

1. **Should we add a unified message storage format?**
   - Pro: Cleaner, provider-agnostic
   - Con: Migration effort, backwards compatibility

2. **How should we handle provider-specific features?**
   - Example: Claude's "thinking" tokens, OpenAI's function calling nuances
   - Need abstraction that doesn't lose provider capabilities

3. **Should we support multiple API keys per provider?**
   - Example: Personal vs. work OpenAI key
   - Team-based key rotation

4. **How to handle rate limits across providers?**
   - Automatic throttling
   - Queue management
   - Provider selection based on availability

---

## Documentation Links

- [Phase 1 Complete](PHASE1_COMPLETE.md) - Provider abstraction
- [Phase 2 Complete](PHASE2_COMPLETE.md) - OpenAI provider
- [Phase 3 Complete](PHASE3_COMPLETE.md) - Integration & validation
- [Phase 4 Complete](PHASE4_COMPLETE.md) - Claude provider
- [LLM Package Documentation](internal/llm/README.md)
- [Claude API Documentation](https://docs.anthropic.com/en/api/)
- [OpenAI API Documentation](https://platform.openai.com/docs/api-reference)

---

**Last Updated:** 2025-10-22
**Status:** Phase 4 Complete - Claude provider added (text-only)
**Next:** Phase 5 (Tool Calling) or Phase 6 (Conversation History)
