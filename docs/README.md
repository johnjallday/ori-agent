# Ori Agent Documentation

This directory contains detailed documentation for Ori Agent.

## Quick Links

### Getting Started
- [Main README](../README.md) - Project overview and quick start

### API & Development
- [API Reference](./api/API_REFERENCE.md) - HTTP API endpoint documentation
- [LLM Provider Guide](../internal/llm/README.md) - LLM provider abstraction and implementation

### Testing & Quality
- [Smoke Tests Guide](./SMOKE_TESTS.md) - Automated installer smoke testing (CI/CD)
- [Testing Installers](./TESTING_INSTALLERS.md) - Manual installer testing guide (VMs, Docker)
- [Test Cheat Sheet](./testing/TEST_CHEATSHEET.md) - Quick command reference
- [Testing Setup Summary](./testing/TESTING_SETUP_SUMMARY.md) - Overview of testing infrastructure
- [Direct Tool Testing](./testing/DIRECT_TOOL_TESTING.md) - Direct tool launch feature testing guide

### Release & Deployment
- [Release Checklist](./RELEASE_CHECKLIST.md) - Pre-release validation checklist
- [Dependency Management](./DEPENDENCY_MANAGEMENT.md) - Managing Go dependencies

### Feature Planning
- [Agent Output Viewing Plan](./features/AGENT_OUTPUT_VIEWING_PLAN.md) - Implementation plan for viewing agent outputs
- [Progress Tracking Plan](./features/PROGRESS_TRACKING_PLAN.md) - Implementation plan for progress tracking

### UI Documentation
- [Form Styling Index](./ui/FORM_STYLING_INDEX.md) - Navigation guide for all form styling docs
- [Form Design Reference](./ui/AGENT_FORM_DESIGN_REFERENCE.md) - Complete form design system
- [Form Comparison](./ui/FORM_COMPARISON.md) - Comparison between different form implementations
- [Form Quick Reference](./ui/AGENT_FORM_QUICK_REFERENCE.md) - Quick lookup for form styling

## Documentation Structure

```
docs/
├── README.md                           # This file
│
├── SMOKE_TESTS.md                      # Automated installer smoke testing
├── TESTING_INSTALLERS.md               # Manual installer testing guide
├── RELEASE_CHECKLIST.md                # Pre-release validation checklist
├── DEPENDENCY_MANAGEMENT.md            # Go dependency management guide
│
├── api/
│   └── API_REFERENCE.md                # HTTP API documentation
│
├── testing/
│   ├── TEST_CHEATSHEET.md              # Quick testing commands
│   ├── TESTING_SETUP_SUMMARY.md        # Testing infrastructure overview
│   └── DIRECT_TOOL_TESTING.md          # Direct tool launch feature testing guide
│
├── features/
│   ├── AGENT_OUTPUT_VIEWING_PLAN.md    # Agent output viewing implementation plan
│   └── PROGRESS_TRACKING_PLAN.md       # Progress tracking implementation plan
│
└── ui/
    ├── FORM_STYLING_INDEX.md           # Form styling documentation index
    ├── AGENT_FORM_DESIGN_REFERENCE.md  # Complete form design reference
    ├── FORM_COMPARISON.md              # Form comparison (main vs workspace)
    └── AGENT_FORM_QUICK_REFERENCE.md   # Quick reference for form styling
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
3. **Testing documentation** → `docs/testing/` or `docs/` (for major guides)
4. **Feature planning docs** → `docs/features/`
5. **UI/design documentation** → `docs/ui/`
6. **Code-specific docs** → Keep next to the code (e.g., `internal/*/README.md`)
7. **High-level guides** → Keep in root (e.g., `README.md`, `CLAUDE.md`)

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
