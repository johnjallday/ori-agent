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

## 🔧 Development Tools

### Dependency Management

Ori Agent includes comprehensive dependency management tools to keep your dependencies up-to-date and secure.

#### Quick Commands

```bash
# Check for available updates
make deps-check

# Check for security vulnerabilities
make deps-vuln

# Update all dependencies (interactive)
make deps-update

# Update patch versions only (safer)
make deps-update-patch

# Verify dependencies
make deps-verify

# Clean up go.mod
make deps-tidy
```

#### Security Scanning

We use `govulncheck` to scan for known vulnerabilities:

```bash
# Run security scan
make deps-vuln

# Install govulncheck manually
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...
```

#### Automated Updates (GitHub)

Dependabot is configured to automatically check for dependency updates weekly:
- **Schedule**: Every Monday at 9:00 AM
- **Strategy**: Groups minor and patch updates together
- **Pull Requests**: Automatically creates PRs for updates
- Configuration: `.github/dependabot.yml`

#### Advanced Dependency Commands

```bash
# Show why a dependency is needed
make deps-why DEP=github.com/example/package

# Generate dependency graph (requires graphviz)
make deps-graph

# Show outdated dependencies
make deps-outdated
```

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

---

Made with ❤️ using Go and modern web technologies
