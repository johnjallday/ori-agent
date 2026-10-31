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

### Development Tools
- [External development tools](./devtools.md) - Source/target/runtime boundary,
  retained commands, offline validation and recovery
- [Ori–Herdr Devflow Bridge](./herdr-devflow.md) - Entry to the external operating guide
- [Herdr Wake Service macOS dogfood](./herdr-standalone-wake-dogfood.md) - Entry to the
  separately authorized external manual protocol
- [Claude usage-signal contract](./herdr-devflow-claude-usage-signal.md) - External contract location
- [Away Dispatcher](./away-dispatcher.md) - Entry to the external operating guide; legacy
  dispatcher/tick paths remain as compatibility shims
- [DevOps Explore](./devops-explore.md) - Entry to the external guide and prompt assets;
  `./scripts/devops.sh explore` remains the compatibility entrypoint

### Feature Guides
- [Scheduler Nodes Guide](./SCHEDULER_NODES_GUIDE.md) - Complete guide to using scheduler nodes for task automation
- [Custom Workspace Dashboard](./features/custom-workspace-dashboard.md) - Write your own HTML
  dashboard for a workspace, reading live workspace data in a sandboxed frame
- [Moving a Workspace](./features/moving-workspaces.md) - Copy a workspace folder to another computer
  and continue with its history (and your assistant)
- [Workspace Next Action](./workspace-next-action.md) - The shared **Next action** recommendation
  in Map and Details, and how it is chosen
- [Workspace Team Readiness](./workspace-team-readiness.md) - The Create Workspace staffing
  contract: when a blueprint role counts as filled
- [Plugins](./plugins.md) - Installing Claude Code- and Codex-compatible plugins (MCP servers,
  skills) and Workspace Surface contributions
- [Plugin-owned Setup Quests](./plugin-setup-quests.md) - The install quest and the plugin's own
  pre-workspace setup quest
- [Setup Quests for Your Project Templates](./user-setup-quests.md) - Attach a saved setup guide
  to a project template you own
- [Ori's REAPER Integration](./reaper-integration.md) - The optional REAPER contribution, Music
  Production Home, project roles and Sample Library
- [Backup & Recovery](./backup-and-recovery.md) - Observing an external backup application's
  metadata and guiding recovery
- [File Curator Guidance](./file-curator-guidance.md) - File Janitor's optional Curator: what it
  explains and what it cannot approve
- [Premium Features](./premium_features.md) - Premium feature overview

### Testing & Quality
- [Smoke Tests Guide](./testing/SMOKE_TESTS.md) - Automated installer smoke testing (CI/CD)
- [Testing Installers](./TESTING_INSTALLERS.md) - Manual installer testing guide (VMs, Docker)
- [Test Cheat Sheet](./testing/TEST_CHEATSHEET.md) - Quick command reference
- [Testing Setup Summary](./testing/TESTING_SETUP_SUMMARY.md) - Overview of testing infrastructure
- [Direct Tool Testing](./testing/DIRECT_TOOL_TESTING.md) - Direct tool launch feature testing guide
- [Ollama Testing](./testing/OLLAMA_TESTING.md) - Testing with local Ollama models
- [User Test Guide](./testing/USER_TEST_GUIDE.md) - Interactive user testing system
- [Folder-first Manual Test](./testing/folder-first-manual.md) - Folder-first walkthrough on an
  isolated disposable server

### Release & Deployment
- [Release Checklist](./RELEASE_CHECKLIST.md) - Pre-release validation checklist
- [RC Testing Protocol](./RC_TEST_PROTOCOL.md) - Core and change-specific checks for every
  exact release candidate before stable promotion
- [CI Triage Runbook](./CI_TRIAGE.md) - Turning red CI on dev or a release branch green at the root, through PRs
- [Dependency Management](./DEPENDENCY_MANAGEMENT.md) - Managing Go dependencies
- [Building the MSI Installer](./BUILD_MSI.md) - Windows MSI build guide
- [README Maintenance](./README_MAINTENANCE.md) - Source of truth for the root README and its
  product screenshots ([screenshot manifest](./readme-screenshots.json))

### Project Policies
- [Ori Services](../ORI_SERVICES.md) - Private services access and scope
- [Trademarks](../TRADEMARKS.md) - Branding and trademark usage
- [Character Asset Provenance Register](./CHARACTER_ASSET_PROVENANCE.md) - Review record for every
  shipped character asset ([hashes](./character-asset-hashes.txt))

### Feature Planning
- [PRD-to-Task Coverage Audit](./PRD_TASK_COVERAGE_AUDIT.md) - Final planning-quality check before creating a feature worktree
- [Feature Discovery Playbook](./feature-discovery/PLAYBOOK.md) - Weekly routine that produces a
  ranked shortlist of feature candidates
- [Dogfooding Gameplan](./feature-discovery/DOGFOODING.md) - Run Ori's capture → discover →
  decide → build loop inside Ori
- [Feature Discovery Reports](./feature-discovery/reports/2026-07-18.md) - Dated discovery
  reports (first run: 2026-07-18)

### Architecture & Contracts
- [Anchored Folder Workspaces](./architecture/anchored-folder-workspace.md) - Managed,
  anchored and imported shapes; explicit folder access and the sidecar lifecycle

#### Assistant
- [Personal Assistant Foundation Contract](./architecture/personal-assistant-foundation-contract.md) -
  The Personal Assistant foundation contract v1 and its amendments (guided Personal HQ, follow-up
  ownership, portable import)
- [Assistant Program Contract](./architecture/assistant-program-contract.md) - Trusted-blueprint
  contract (v1 and v2) for one durable assistant station shared by project workspaces
- [Conversation-first Contract](./architecture/assistant-conversation-first.md) - Characterized
  baseline and implementation seam for the assistant's conversation-first chat
- [Conversation Context](./architecture/assistant-conversation-context.md) - The disposable,
  Session-owned recap checkpoint behind Personal Assistant conversations
- [Conversation Folder Context](./architecture/assistant-chat-folder-context.md) - Findings and
  contract for attaching a folder to an assistant conversation
- [Workspace Awareness](./architecture/assistant-workspace-awareness.md) - Context-following
  conversations, setup continuity, grounded notes/tasks and permitted file reading
- [Assistant Operations ("Operate Ori")](./architecture/assistant-operations.md) - The hired
  assistant opens a workspace, selects an agent and prepares a task the user reviews and starts
- [Connect Your Second Brain](./architecture/second-brain.md) - The user's notes (an Obsidian vault
  or Markdown folder) as a read-only Personal HQ source

#### Workspaces & placement
- [Workspace Build Sessions](./architecture/workspace-build-sessions.md) - "Build with your assistant": the draft is the create request, validation, storage, privacy
- [Workspace Goal Runs](./architecture/workspace-goal-runs.md) - How a goal run turns the workspace goal into Backlog suggestions and findings, and when it starts
- [Assisted Workspace Placement](./architecture/assisted-workspace-placement.md) - Propose →
  review/edit → click to place and create, for the built-in File Janitor inbox-folder proposal
- [Unified Workspace Placement](./architecture/unified-workspace-placement.md) - The same review
  and map placement for every other creation flow
- [Visual Folder Setup](./architecture/visual-folder-setup.md) - One display seam for folder
  review, saved setup progress, recovery and receipt
- [Workspace Arrangement](./architecture/workspace-arrangement.md) - Workspace arrangement v1:
  the bounded core for proposing, reviewing and applying an arrangement into collections and groups
- [Group Templates](./architecture/group-templates.md) - Group Templates v1: Create Group creates
  or reuses one exact Assistant Program Home
- [Template Group Requirements](./architecture/template-group-requirements.md) - Whether a
  template's project is standalone or belongs in an Assistant Program Home
- [Independent Program Homes](./architecture/independent-program-homes.md) - Separating a reusable
  Assistant Program Home from the project integration that attaches teams to it
- [Project Library](./architecture/project-library.md) - Implementation evidence and contract for
  a Home's project library: roots, scans and portfolio authority

#### Setup & onboarding
- [Specialist Setup Journey Contract](./architecture/specialist-setup-journey-contract.md) - Host
  contracts for generic specialist setup journeys, with REAPER as the v1 declaration
- [Host-owned Setup Quests](./architecture/host-setup-quests.md) - Contract for host-owned setup
  quests, including the goal-first Gmail quest for Email Ops
- [User-owned Setup Quests](./architecture/user-owned-setup-quests.md) - One inert quest
  declaration embedded in a user-owned project template
- [Assistant-led Setup Contract](./architecture/assistant-led-setup-contract.md) - Assistant-led
  File Janitor setup: findings baseline and delivered contract
- [File Janitor Creation Flow](./architecture/file-janitor-creation-flow.md) - Reviewed File
  Janitor creation and preparation from an attached folder
- [Music Setup Onboarding](./architecture/music-setup-onboarding.md) - State and consent contract
  for the Music Production Home onboarding journey
- [Blueprint Intake](./architecture/blueprint-intake.md) - Setup step kinds, Create Workspace
  inputs, bundled skills, reviewed record application and re-intake automation

#### Plugins & surfaces
- [Plugin Workspace Surfaces](./architecture/plugin-workspace-surfaces.md) - Architecture
  decision for trusted plugin contributions through one versioned Workspace Surface protocol
- [Workspace Surface Protocol v1](./architecture/plugin-workspace-surfaces-v1.md) - The frozen,
  normative Workspace Surface protocol v1 contract

#### Integrations (Google, email, REAPER)
- [Google Account Connection Spike](./architecture/google-account-connection-spike.md) -
  Compatibility spike for the Google Account Connection hub (Gmail, Calendar, Drive)
- [Google OAuth Client Setup](./architecture/google-account-connection-oauth-setup.md) - Setting
  up the OAuth client a self-hosted instance needs for **Connect Google**
- [Google Account Connection Release Readiness](./architecture/google-account-connection-release-readiness.md) -
  Feature flags, operator configuration and the OAuth verification gate
- [Email Setup Card](./architecture/email-setup-card.md) - Connecting a mailbox from an email
  address and an app password
- [Needs You](./architecture/email-needs-you.md) - How Email Ops sorts the last week of mail into
  needs you, worth knowing and probably ignorable
- [REAPER Studio](./architecture/reaper-studio.md) - Resource workspaces and the resource catalog
  (contract v1)

#### Backup, reset & continuity
- [Backup & Recovery Contract](./architecture/backup-and-recovery-contract.md) - Integration
  contract v1: Ori observes approved backup metadata; the external application does the work
- [Settings Reset](./architecture/settings-reset.md) - Scope and lifecycle of Replay Setup, Reset
  Getting Started, Reset Selected Data and Start Fresh
- [Workspace Continuity Contract](./architecture/workspace-continuity-contract.md) - Portable
  workspace continuity v1: moving a workspace folder with its history

#### Platform boundaries
- [Open-Core Boundaries](./architecture/open-core-boundaries.md) - Separation of OSS core and private services
- [Herdr Standalone Wake v1 Contract](./architecture/herdr-standalone-wake-v1-contract.md) - Location of the verbatim accepted contract in the external toolbox

### Design & Examples
- [Assistant Folder Response Prototype](./design/assistant-folder-response-prototype.html) -
  Fixture-backed folder-response prototype (not production)
- [Workspace Dashboard Example](./examples/workspace-dashboard/index.html) - Worked example for
  the custom workspace dashboard guide
- [REAPER Web Remote Skill](./examples/skills/reaper-web-remote/SKILL.md) - Example skill that
  drives REAPER over its Web Remote HTTP interface from a CLI agent's shell

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
├── RC_TEST_PROTOCOL.md                 # Release-candidate testing protocol
├── CI_TRIAGE.md                        # Red-CI runbook shared by agents and people
├── DEPENDENCY_MANAGEMENT.md            # Go dependency management guide
├── README_MAINTENANCE.md               # Root README and screenshot maintenance
├── readme-screenshots.json             # Accepted README screenshot manifest
├── CHARACTER_ASSET_PROVENANCE.md       # Character asset review register
├── character-asset-hashes.txt          # SHA-256 of shipped character assets
├── SCHEDULER_NODES_GUIDE.md            # Scheduler nodes usage guide
├── PRD_TASK_COVERAGE_AUDIT.md          # PRD-to-task planning audit
├── premium_features.md                 # Premium feature overview
├── devtools.md                         # External development tools
├── herdr-*.md                          # Herdr devflow / wake entry pages (external guides)
├── away-dispatcher.md                  # Away Dispatcher entry page (external guide)
├── devops-explore.md                   # DevOps Explore entry page (external guide)
├── plugins.md                          # Installing plugins and Workspace Surfaces
├── plugin-setup-quests.md              # Plugin-owned setup quests
├── user-setup-quests.md                # Setup quests for user-owned templates
├── reaper-integration.md               # Ori's REAPER integration
├── backup-and-recovery.md              # Backup & Recovery guide
├── file-curator-guidance.md            # File Janitor's optional Curator
├── workspace-next-action.md            # Map/Details Next action
├── workspace-team-readiness.md         # Create Workspace staffing contract
│
├── api/
│   └── API_REFERENCE.md                # HTTP API documentation
│
├── architecture/                       # Architecture decisions and contracts (see
│   │                                   #   "Architecture & Contracts" above)
│   └── file-janitor-creation-flow/     # Screenshots for file-janitor-creation-flow.md
│
├── design/                             # Fixture-backed design prototypes
│
├── examples/
│   ├── skills/reaper-web-remote/       # Example skill
│   └── workspace-dashboard/            # Custom workspace dashboard example
│
├── feature-discovery/
│   ├── PLAYBOOK.md                     # Weekly feature discovery routine
│   ├── DOGFOODING.md                   # Dogfooding gameplan
│   └── reports/                        # Dated discovery reports
│
├── features/
│   ├── custom-workspace-dashboard.md   # Custom HTML workspace dashboards
│   └── moving-workspaces.md            # Moving a workspace to another computer
│
├── images/                             # Images used by the root README and docs
│
├── testing/
│   ├── SMOKE_TESTS.md                  # Automated installer smoke testing
│   ├── TEST_CHEATSHEET.md              # Quick testing commands
│   ├── TESTING_SETUP_SUMMARY.md        # Testing infrastructure overview
│   ├── DIRECT_TOOL_TESTING.md          # Direct tool launch feature testing guide
│   ├── OLLAMA_TESTING.md               # Ollama-based local model testing
│   ├── USER_TEST_GUIDE.md              # Interactive user testing system
│   └── folder-first-manual.md          # Folder-first manual walkthrough
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
4. **Feature guides** → `docs/features/`
5. **Architecture decisions and contracts** → `docs/architecture/`
6. **UI/design documentation** → `docs/ui/`
7. **Code-specific docs** → Keep next to the code (e.g., `internal/*/README.md`)
8. **High-level guides** → Keep in root (e.g., `README.md`, `CLAUDE.md`)

### Documentation Standards

- Use clear, descriptive titles
- Include table of contents for long documents
- Provide code examples where applicable
- Keep documents focused on a single topic
- Update references when moving files
- Use relative links for internal documentation
- Add new documents to this index

## External Resources

- [Go Documentation](https://go.dev/doc/)
- [MCP Specification](https://modelcontextprotocol.io/)
- [OpenAI API Reference](https://platform.openai.com/docs/api-reference)
- [Anthropic Claude API](https://docs.anthropic.com/)
