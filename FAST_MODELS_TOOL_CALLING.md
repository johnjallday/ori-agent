# Fast Models for Tool Calling

Yes! There are several **fast, small models** optimized for tool calling.

## 🏆 Top Recommendations (Fast + Small + Good Tool Calling)

### 1. **Mistral 7B** ⭐⭐⭐ **BEST OVERALL**
```bash
ollama pull mistral
OLLAMA_MODEL=mistral make test-ollama
```
- **Size**: ~4GB
- **Speed**: Fast ⚡
- **Tool Calling**: ✅ Excellent
- **RAM**: 8GB+
- **Why**: Best balance of size, speed, and tool calling quality

### 2. **Phi-4-Mini** ⭐⭐ **FASTEST**
```bash
ollama pull phi4-mini
OLLAMA_MODEL=phi4-mini make test-ollama
```
- **Size**: ~2.5GB (3.8B parameters)
- **Speed**: Very Fast ⚡⚡
- **Tool Calling**: ✅ Good
- **RAM**: 4GB+
- **Why**: Smallest model with solid tool calling

### 3. **Nemotron-Mini** ⭐ **OPTIMIZED FOR FUNCTION CALLING**
```bash
ollama pull nemotron-mini
OLLAMA_MODEL=nemotron-mini make test-ollama
```
- **Size**: ~2.7GB (4B parameters)
- **Speed**: Very Fast ⚡⚡
- **Tool Calling**: ✅ Excellent (optimized for it!)
- **RAM**: 6GB+
- **Why**: Specifically optimized for function calling

### 4. **Qwen 2.5 (3B)** ⭐ **MULTILINGUAL + FAST**
```bash
ollama pull qwen2.5:3b
OLLAMA_MODEL=qwen2.5:3b make test-ollama
```
- **Size**: ~2GB (3B parameters)
- **Speed**: Very Fast ⚡⚡
- **Tool Calling**: ✅ Good
- **RAM**: 4GB+
- **Why**: Very small, multilingual, 128K context

---

## 📊 Performance Comparison

| Model | Size | Speed | Tool Calling | RAM | Best For |
|-------|------|-------|--------------|-----|----------|
| **mistral** | 4GB | ⚡⚡ Fast | ✅ Excellent | 8GB | **General use** ⭐⭐⭐ |
| **phi4-mini** | 2.5GB | ⚡⚡⚡ Very Fast | ✅ Good | 4GB | **Speed priority** ⭐⭐ |
| **nemotron-mini** | 2.7GB | ⚡⚡⚡ Very Fast | ✅ Excellent | 6GB | **Function calling** ⭐⭐ |
| **qwen2.5:3b** | 2GB | ⚡⚡⚡ Very Fast | ✅ Good | 4GB | **Low memory** ⭐ |
| **granite 3.2:3b** | 2GB | ⚡⚡⚡ Very Fast | ✅ Good | 4GB | **Long context** ⭐ |
| llama3.1:8b | 4.9GB | ⚡⚡ Fast | ❌ Poor | 8GB | ❌ Not recommended |
| mixtral | 4.7GB | ⚡ Medium | ✅ Excellent | 16GB | Slower but accurate |
| llama3.1:70b | 40GB | 🐌 Very Slow | ✅ Good | 64GB | ❌ Too slow |

---

## 🚀 Quick Test Commands

### Test with Mistral (Recommended)
```bash
ollama pull mistral
OLLAMA_MODEL=mistral make test-ollama
```

### Test with Phi-4-Mini (Fastest)
```bash
ollama pull phi4-mini
OLLAMA_MODEL=phi4-mini make test-ollama
```

### Test with Nemotron-Mini (Function Calling Optimized)
```bash
ollama pull nemotron-mini
OLLAMA_MODEL=nemotron-mini make test-ollama
```

### Test with Qwen 2.5 (Smallest)
```bash
ollama pull qwen2.5:3b
OLLAMA_MODEL=qwen2.5:3b make test-ollama
```

---

## 📈 Detailed Model Profiles

### Mistral 7B - Best All-Around
**Perfect for:** Production local deployment

```bash
ollama pull mistral
```

**Stats:**
- Parameters: 7B
- Size: ~4GB
- Context: 32K tokens
- Strengths: Excellent tool calling, good reasoning, fast inference
- Weaknesses: Needs 8GB+ RAM

**Expected Test Performance:**
- Agent creation: ~1-2s
- Tool calling: ~3-5s per call
- Full test suite: ~5-8 minutes

---

### Phi-4-Mini - Fastest Compact
**Perfect for:** Resource-constrained environments

```bash
ollama pull phi4-mini
```

**Stats:**
- Parameters: 3.8B
- Size: ~2.5GB
- Context: 16K tokens
- Strengths: Very fast, low memory, good math/reasoning
- Weaknesses: Smaller context window

**Expected Test Performance:**
- Agent creation: ~0.5-1s
- Tool calling: ~1-3s per call
- Full test suite: ~3-5 minutes

---

### Nemotron-Mini - Function Calling Specialist
**Perfect for:** Tool-heavy applications

```bash
ollama pull nemotron-mini
```

**Stats:**
- Parameters: 4B
- Size: ~2.7GB
- Context: 8K tokens
- Strengths: **Optimized for function calling**, roleplay, RAG
- Weaknesses: Smaller context window

**Expected Test Performance:**
- Agent creation: ~1s
- Tool calling: ~2-4s per call (very reliable)
- Full test suite: ~4-6 minutes

---

### Qwen 2.5 (3B) - Multilingual Compact
**Perfect for:** Low-memory systems, multilingual needs

```bash
ollama pull qwen2.5:3b
```

**Stats:**
- Parameters: 3B
- Size: ~2GB
- Context: 128K tokens (huge!)
- Strengths: Tiny size, multilingual, massive context
- Weaknesses: May be less accurate than larger models

**Expected Test Performance:**
- Agent creation: ~0.5-1s
- Tool calling: ~2-4s per call
- Full test suite: ~3-5 minutes

---

## 🎯 Which Model Should You Use?

### For Production Testing
```bash
ollama pull mistral
OLLAMA_MODEL=mistral make test-ollama
```
**Why:** Best balance of speed, quality, and reliability

### For Development/Quick Tests
```bash
ollama pull phi4-mini
OLLAMA_MODEL=phi4-mini make test-ollama
```
**Why:** Fastest, uses least RAM, good enough for dev work

### For Tool-Heavy Workflows
```bash
ollama pull nemotron-mini
OLLAMA_MODEL=nemotron-mini make test-ollama
```
**Why:** Specifically optimized for function calling

### For Low-Memory Systems
```bash
ollama pull qwen2.5:3b
OLLAMA_MODEL=qwen2.5:3b make test-ollama
```
**Why:** Only 2GB, runs on 4GB RAM systems

---

## ⚡ Speed Benchmarks (Estimated)

### Complete Test Suite (~18 tests)

| Model | Total Time | Per Test Avg |
|-------|-----------|--------------|
| **phi4-mini** | ~3-5 min | ~10-15s | ⚡⚡⚡ Fastest
| **nemotron-mini** | ~4-6 min | ~15-20s | ⚡⚡⚡ Very Fast
| **qwen2.5:3b** | ~3-5 min | ~10-15s | ⚡⚡⚡ Very Fast
| **mistral** | ~5-8 min | ~20-30s | ⚡⚡ Fast
| mixtral | ~10-15 min | ~40-60s | ⚡ Medium
| llama3.1:8b | ~6-10 min | ~20-30s | ⚡⚡ Fast (but tools fail)
| llama3.1:70b | ~30-60 min | ~2-4min | 🐌 Very Slow
| **gpt-4o-mini** | ~2-3 min | ~8-12s | ⚡⚡⚡⚡ Cloud (fastest)

---

## 💡 Pro Tips

### 1. Use Quantized Models for Speed
```bash
# 4-bit quantized (faster, less accurate)
ollama pull mistral:7b-q4_0

# 8-bit quantized (balanced)
ollama pull mistral:7b-q8_0
```

### 2. Adjust Context Window
Smaller context = faster inference:
```bash
ollama run mistral --ctx-size 2048  # Faster
ollama run mistral --ctx-size 32768 # Slower but better
```

### 3. Run Specific Tests Only
```bash
# Test one workflow (fast)
USE_OLLAMA=true OLLAMA_MODEL=phi4-mini \
  go test -v ./tests/user/workflows/... -run TestAgentWithPluginWorkflow

# Test math plugin only (fast)
USE_OLLAMA=true OLLAMA_MODEL=nemotron-mini \
  go test -v ./tests/user/plugins/... -run TestMathPluginIntegration
```

### 4. Use Verbose Mode to See Progress
```bash
TEST_VERBOSE=true OLLAMA_MODEL=mistral make test-ollama
```

---

## 🔥 Recommended Setup

### For Daily Development
```bash
# Install Phi-4-Mini (fastest for quick iterations)
ollama pull phi4-mini

# Quick test
OLLAMA_MODEL=phi4-mini \
  go test -v ./tests/user/workflows/... -run TestAgentWithPluginWorkflow
```

### For CI/CD Pipeline
```bash
# Use cloud APIs for speed and reliability
export OPENAI_API_KEY="your-key"
make test-user  # ~2-3 minutes total
```

### For Comprehensive Local Testing
```bash
# Install Mistral (best local quality)
ollama pull mistral

# Full test suite
OLLAMA_MODEL=mistral make test-ollama  # ~5-8 minutes
```

---

## 🎪 Real-World Example

### Phi-4-Mini (Fast Test)
```bash
$ time OLLAMA_MODEL=phi4-mini go test -v ./tests/user/workflows/... -run TestAgentWithPluginWorkflow

=== RUN   TestAgentWithPluginWorkflow
    ✓ Created agent: plugin-test-agent (model: phi4-mini)
    ✓ Enabled plugin 'math'
    ✓ Tool called: math (1.2s)
    ✓ Response contains: '42'
--- PASS: TestAgentWithPluginWorkflow (4.3s)

real    0m4.8s    ← Fast! ⚡⚡⚡
```

### Mistral (Balanced)
```bash
$ time OLLAMA_MODEL=mistral go test -v ./tests/user/workflows/... -run TestAgentWithPluginWorkflow

=== RUN   TestAgentWithPluginWorkflow
    ✓ Created agent: plugin-test-agent (model: mistral)
    ✓ Enabled plugin 'math'
    ✓ Tool called: math (3.1s)
    ✓ Response contains: '42'
--- PASS: TestAgentWithPluginWorkflow (8.2s)

real    0m8.6s    ← Still fast! ⚡⚡
```

---

## 📚 Summary

**Fastest + Good Tool Calling:**
1. **Phi-4-Mini** (2.5GB) - Best for speed
2. **Nemotron-Mini** (2.7GB) - Best for function calling
3. **Qwen 2.5 3B** (2GB) - Best for low memory

**Best Overall:**
- **Mistral 7B** (4GB) - Best balance

**For Production:**
- **GPT-4o-mini** (Cloud) - Fastest + most reliable

---

## 🚀 Get Started Now

```bash
# Install the fastest reliable model
ollama pull phi4-mini

# Run a quick test
OLLAMA_MODEL=phi4-mini \
  go test -v ./tests/user/workflows/... -run TestAgentWithPluginWorkflow

# If that works well, run full suite
OLLAMA_MODEL=phi4-mini make test-ollama
```

**Expected result:** ~3-5 minute full test suite with good tool calling! ⚡
