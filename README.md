# 🦆 Ori Agent

![Version](https://img.shields.io/badge/Version-v0.0.10-blue)
![Go](https://img.shields.io/badge/Go-1.25-00add8)

**Ori Agent** is a platform that lets you create customizable AI assistants that can use tools to get things done. Think of it like ChatGPT, but you can add your own custom tools (called plugins) to make it do specific tasks like checking weather, doing calculations, or controlling other software on your computer. Each AI assistant you create can have its own set of tools and settings.

## 🤖 Supported Providers

Ori Agent supports multiple AI providers, giving you flexibility in choosing your preferred AI model:

### Cloud Providers
- **OpenAI**
  - Requires: `OPENAI_API_KEY`
  - Best for: Production use, latest models, reliable performance

- **Anthropic Claude**
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

## 🔌 Plugin Development

Ori Agent uses a plugin system that lets you extend functionality with custom tools. Build plugins as standalone executables that communicate via gRPC.

### Plugin Optimization APIs (New!)

We've introduced three powerful APIs that dramatically simplify plugin development:

#### 1. **YAML-Based Tool Definitions** (70% less code!)
Define parameters in `plugin.yaml` instead of code:

```yaml
tool_definition:
  description: "Your tool description"
  parameters:
    - name: operation
      type: string
      required: true
      enum: [create, list, delete]
    - name: count
      type: integer
      min: 1
      max: 100
```

```go
func (t *MyTool) Definition() pluginapi.Tool {
    tool, _ := t.GetToolDefinition()  // Auto-loads from plugin.yaml
    return tool
}
```

#### 2. **Settings API**
Simple, thread-safe key-value storage for plugin configuration:

```go
sm := t.Settings()
sm.Set("api_key", "sk-123")
apiKey, _ := sm.GetString("api_key")
```

#### 3. **Template Rendering API**
Serve beautiful web pages with Go templates:

```go
//go:embed templates
var templatesFS embed.FS

html, err := pluginapi.RenderTemplate(templatesFS, "templates/page.html", data)
```

### Getting Started with Plugins

- **Documentation**: See [PLUGIN_OPTIMIZATION_GUIDE.md](PLUGIN_OPTIMIZATION_GUIDE.md) for complete migration guide
- **Examples**: Check out `example_plugins/minimal/` and `example_plugins/webapp/`
- **Reference**: See [CLAUDE.md](CLAUDE.md) for detailed plugin development patterns

### Building a Plugin

```bash
cd example_plugins/minimal
go build -o minimal-plugin main.go
cp minimal-plugin ../../uploaded_plugins/
```

Restart Ori Agent, and your plugin will be automatically loaded!

#### Installing Development Tools

```bash
# Install all development tools (linter + security scanner)
make install-tools
```

This installs:
- `golangci-lint` - Go linter
- `govulncheck` - Vulnerability scanner

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 💬 Support

- 🐛 **Issues**: [GitHub Issues](https://github.com/johnjallday/ori-agent/issues)
- 💡 **Feature Requests**: Open an issue with the "enhancement" label
- 💬 **Discussions**: [GitHub Discussions](https://github.com/johnjallday/ori-agent/discussions)

While this app is very functional, there will be a lot of breaking changes. Feel free to give feedbacks.

## 🛣️Roadmap
- blockchain integration
- white paper
- web3 integration for markets.
- sqlite to save conversation history (maybe)


---

Made with ❤️ using Go and modern web technologies
