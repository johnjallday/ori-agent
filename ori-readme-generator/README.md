# Ori README Generator

A plugin for [ori-agent](https://github.com/johnjallday/ori-agent) that automatically generates and updates README sections based on codebase analysis.

## What It Does

This plugin analyzes the ori-agent codebase and generates documentation sections:

- **Plugins Section**: Lists all available plugins from the plugin registry

## Installation

### Build the plugin

```bash
cd ori-readme-generator
go mod tidy
go build -o readme-generator main.go
```

### Load into ori-agent

1. Copy the built binary to ori-agent's `uploaded_plugins/` directory:
   ```bash
   cp readme-generator ../uploaded_plugins/
   ```

2. Restart ori-agent - it will automatically detect the new plugin

## Usage

Once loaded, you can use it in a chat with an agent:

```
Generate the plugins section for the README
```

The agent will call the `generate_readme` tool with the appropriate parameters.

## Development

### Current Features (v0.1.0)

- [x] Plugin registry scanning
- [x] Plugins section generation
- [x] Markdown table output

### Planned Features

- [ ] API endpoints scanning
- [ ] LLM providers detection
- [ ] Configuration options documentation
- [ ] Architecture overview generation
- [ ] Full README generation with section preservation

## License

MIT
