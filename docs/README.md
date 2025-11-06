# Ori Agent Documentation

This directory contains detailed documentation for Ori Agent.

## Quick Links

### Getting Started
- [Main README](../README.md) - Project overview and quick start
- [Testing Guide](../TESTING.md) - Comprehensive testing documentation

### API & Development
- [API Reference](./api/API_REFERENCE.md) - HTTP API endpoint documentation
- [LLM Provider Guide](../internal/llm/README.md) - LLM provider abstraction and implementation

### Testing
- [Testing Guide](../TESTING.md) - Complete testing guide (main document)
- [Test Cheat Sheet](./testing/TEST_CHEATSHEET.md) - Quick command reference
- [Testing Setup Summary](./testing/TESTING_SETUP_SUMMARY.md) - Overview of testing infrastructure

## Documentation Structure

```
docs/
├── README.md                           # This file
│
├── api/
│   └── API_REFERENCE.md                # HTTP API documentation
│
└── testing/
    ├── TEST_CHEATSHEET.md              # Quick testing commands
    └── TESTING_SETUP_SUMMARY.md        # Testing infrastructure overview
```

## Project-Specific Documentation

Some documentation lives alongside the code it documents:

- `internal/llm/README.md` - LLM provider implementation guide
- `example_plugins/*/README.md` - Individual plugin documentation
- `scripts/README.md` - Build and utility scripts

## Contributing

When adding documentation:

1. **User-facing guides** → `docs/` directory
2. **API documentation** → `docs/api/`
3. **Testing documentation** → `docs/testing/`
4. **Code-specific docs** → Keep next to the code (e.g., `internal/*/README.md`)
5. **High-level guides** → Keep in root (e.g., `README.md`, `TESTING.md`)

### Documentation Standards

- Use clear, descriptive titles
- Include table of contents for long documents
- Provide code examples where applicable
- Keep documents focused on a single topic
- Update references when moving files
- Use relative links for internal documentation

## External Resources

- [Go Documentation](https://go.dev/doc/)
- [HashiCorp go-plugin](https://github.com/hashicorp/go-plugin)
- [OpenAI API Reference](https://platform.openai.com/docs/api-reference)
- [Anthropic Claude API](https://docs.anthropic.com/)
