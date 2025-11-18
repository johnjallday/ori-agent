# 🦆 Ori Agent

![Version](https://img.shields.io/badge/Version-v0.1.0-blue)
![Go](https://img.shields.io/badge/Go-1.25-00add8)

**Ori Agent** is a platform that lets you create customizable AI assistants that can use tools to get things done. Think of it like ChatGPT, but you can add your own custom tools (called plugins) to make it do specific tasks like checking weather, doing calculations, or controlling other software on your computer. Each AI assistant you create can have its own set of tools and settings.

## 🤖 Supported Providers

Ori Agent supports multiple AI providers, giving you flexibility in choosing your preferred AI model:

### Cloud Providers
- **OpenAI** - GPT-4o, GPT-4o-mini, GPT-4-turbo, o1-preview, o1-mini
  - Requires: `OPENAI_API_KEY`
  - Best for: Production use, latest models, reliable performance

- **Anthropic Claude** - Claude 3.5 Sonnet, Claude 3 Opus, Claude 3 Haiku
  - Requires: `ANTHROPIC_API_KEY`
  - Best for: Long context windows, detailed reasoning

### Local Providers
- **Ollama** - Run models locally on your machine
  - Requires: Ollama installed and running (http://localhost:11434)
  - Best for: Privacy, offline use, cost savings
  - Supports: Llama 3, Mistral, Phi-3, and other Ollama models

## 🚀 Quick Start

### Prerequisites
- Go 1.25 or later
- An API key from one of the supported providers (OpenAI, Claude) **OR** Ollama installed locally

### Installation

1. **Clone the repository**
   ```bash
   git clone https://github.com/johnjallday/ori-agent.git
   cd ori-agent
   ```

2. **Install dependencies**
   ```bash
   go mod tidy
   ```

3. **Set up your API key** (choose one)
   ```bash
   # For OpenAI
   export OPENAI_API_KEY="your-openai-api-key"

   # For Claude
   export ANTHROPIC_API_KEY="your-anthropic-api-key"

   # For Ollama - just make sure it's running
   # No API key needed!
   ```

4. **Build and run**
   ```bash
   ./scripts/build.sh
   ./bin/ori-agent
   ```

5. **Open your browser**
   ```
   http://localhost:8765
   ```

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 💬 Support

- 🐛 **Issues**: [GitHub Issues](https://github.com/johnjallday/ori-agent/issues)
- 💡 **Feature Requests**: Open an issue with the "enhancement" label
- 💬 **Discussions**: [GitHub Discussions](https://github.com/johnjallday/ori-agent/discussions)

---

Made with ❤️ using Go and modern web technologies
