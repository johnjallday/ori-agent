# Ori Agent Documentation

This directory contains detailed documentation for Ori Agent.

## Quick Links

### Getting Started
- [Main README](../README.md) - Project overview and quick start
- [macOS Installation](./INSTALLATION_MACOS.md) - Installing on macOS
- [Linux Installation](./INSTALLATION_LINUX.md) - Installing on Linux
- [Windows Installation](./INSTALLATION_WINDOWS.md) - Installing on Windows

### API & Development
- [API Reference](./api/API_REFERENCE.md) - HTTP API endpoint documentation
- [LLM Provider Guide](../internal/llm/README.md) - LLM provider abstraction and implementation

### Feature Guides
- [Ori–Herdr Devflow Bridge](./herdr-devflow.md) - Local programmable Herdr worktree and agent workflow,
  including Overnight Runs
- [Herdr Wake Service macOS dogfood](./herdr-standalone-wake-dogfood.md) - Manual standalone daemon,
  coexistence, recovery, and uninstall verification
- [Claude usage-signal contract](./herdr-devflow-claude-usage-signal.md) - What Ori reads to recognize a
  Claude session limit, and everything it deliberately does not
- [Scheduler Nodes Guide](./SCHEDULER_NODES_GUIDE.md) - Complete guide to using scheduler nodes for task automation
- [Multi-Agent Support](./features/multi-agent-support.md) - Running multiple agents
- [Home Assistant Task Routing](./features/home-assistant-task-routing.md) - "Ask Ori" task routing
- [Project Templates](./features/project-templates.md) - Folder-skeleton workspace templates
- [Custom Workspace Dashboard](./features/custom-workspace-dashboard.md) - Write your own HTML
  dashboard for a workspace, reading live workspace data in a sandboxed frame
- [Session File Management](./features/session-file-management.md) - Managing files in sessions
- [Task Input Templating](./features/task-input-templating.md) - Templated task inputs
- [Task Output Contracts](./features/task-output-contracts.md) - Structured task outputs
- [Workspace Runs Harness Model](./features/workspace-runs-harness-model.md) - Workspace run execution model
- [Premium Features](./premium_features.md) - Premium feature overview

### Testing & Quality
- [Smoke Tests Guide](./testing/SMOKE_TESTS.md) - Automated installer smoke testing (CI/CD)
- [Testing Installers](./TESTING_INSTALLERS.md) - Manual installer testing guide (VMs, Docker)
- [Test Cheat Sheet](./testing/TEST_CHEATSHEET.md) - Quick command reference
- [Testing Setup Summary](./testing/TESTING_SETUP_SUMMARY.md) - Overview of testing infrastructure
- [Direct Tool Testing](./testing/DIRECT_TOOL_TESTING.md) - Direct tool launch feature testing guide
- [Ollama Testing](./testing/OLLAMA_TESTING.md) - Testing with local Ollama models
- [User Test Guide](./testing/USER_TEST_GUIDE.md) - Interactive user testing system

### Release & Deployment
- [Release Checklist](./RELEASE_CHECKLIST.md) - Pre-release validation checklist
- [Dependency Management](./DEPENDENCY_MANAGEMENT.md) - Managing Go dependencies
- [Building the MSI Installer](./BUILD_MSI.md) - Windows MSI build guide

### Project Policies
- [Ori Services](../ORI_SERVICES.md) - Private services access and scope
- [Trademarks](../TRADEMARKS.md) - Branding and trademark usage

### Feature Planning
- [AI Features Roadmap](./features/AI_FEATURES_ROADMAP.md) - Planned AI feature work
- [Agent Output Viewing Plan](./features/AGENT_OUTPUT_VIEWING_PLAN.md) - Implementation plan for viewing agent outputs
- [Progress Tracking Plan](./features/PROGRESS_TRACKING_PLAN.md) - Implementation plan for progress tracking
- [System Home Context Routing Plan](./features/system-home-context-routing-plan.md) - Home context routing plan
- [PRD-to-Task Coverage Audit](./PRD_TASK_COVERAGE_AUDIT.md) - Final planning-quality check before creating a feature worktree
- [Open-Core Boundaries](./architecture/open-core-boundaries.md) - Separation of OSS core and private services
- [Herdr Standalone Wake v1 Contract](./architecture/herdr-standalone-wake-v1-contract.md) - Fixed installer, platform, path, ownership, and compatibility decisions

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
├── INSTALLATION_MACOS.md               # macOS installation guide
├── INSTALLATION_LINUX.md               # Linux installation guide
├── INSTALLATION_WINDOWS.md             # Windows installation guide
├── BUILD_MSI.md                        # Windows MSI build guide
├── TESTING_INSTALLERS.md               # Manual installer testing guide
├── RELEASE_CHECKLIST.md                # Pre-release validation checklist
├── DEPENDENCY_MANAGEMENT.md            # Go dependency management guide
├── SCHEDULER_NODES_GUIDE.md            # Scheduler nodes usage guide
├── PRD_TASK_COVERAGE_AUDIT.md          # PRD-to-task planning audit
├── premium_features.md                 # Premium feature overview
│
├── api/
│   └── API_REFERENCE.md                # HTTP API documentation
│
├── testing/
│   ├── SMOKE_TESTS.md                  # Automated installer smoke testing
│   ├── TEST_CHEATSHEET.md              # Quick testing commands
│   ├── TESTING_SETUP_SUMMARY.md        # Testing infrastructure overview
│   ├── DIRECT_TOOL_TESTING.md          # Direct tool launch feature testing guide
│   ├── OLLAMA_TESTING.md               # Ollama-based local model testing
│   └── USER_TEST_GUIDE.md              # Interactive user testing system
│
├── features/                           # Feature guides and implementation plans
│   ├── AI_FEATURES_ROADMAP.md
│   ├── AGENT_OUTPUT_VIEWING_PLAN.md
│   ├── PROGRESS_TRACKING_PLAN.md
│   ├── home-assistant-task-routing.md
│   ├── multi-agent-support.md
│   ├── project-templates.md
│   ├── session-file-management.md
│   ├── system-home-context-routing-plan.md
│   ├── task-input-templating.md
│   ├── task-output-contracts.md
│   └── workspace-runs-harness-model.md
│
├── architecture/
│   ├── herdr-standalone-wake-v1-contract.md # Standalone wake privilege and compatibility contract
│   └── open-core-boundaries.md         # Open-core vs private service boundaries
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
- [MCP Specification](https://modelcontextprotocol.io/)
- [OpenAI API Reference](https://platform.openai.com/docs/api-reference)
- [Anthropic Claude API](https://docs.anthropic.com/)
