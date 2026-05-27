# <img src="assets/logo-readme.svg" alt="Ori Agent logo" width="28" height="28" style="vertical-align: text-bottom;" /> Ori Agent

![Version](https://img.shields.io/badge/Version-v0.0.71-blue) ![Go](https://img.shields.io/badge/Go-1.25.10-00add8)

**Ori Agent** is a local-first AI agent management platform. Spin up multiple named agents, each with its own model, prompt, and tool loadout, and run them through a browser UI or API. Group agents into **workspaces** to collaborate on tasks, give a workspace an autonomous **mission** that runs on a schedule, and triage everything it finds from a single **Action Center**. Agents use MCP (Model Context Protocol) servers and Skills for tool capabilities—everything stays on your machine unless you opt into cloud LLMs.

If you want to keep your information local, this is a way to go.

I don't plan on promoting this until Q426 or Q127 (This date may change).
If you are here early, then welcome! Hope you enjoy this. 
Let me know, how this is.

## Open-Core Boundary

Ori Agent core is open-source and can be used commercially. Web3/token and marketplace payment services are not included in this repo and are operated privately.

Open-source core includes:
- Agent runtime, orchestration, MCP integration, and skills system
- UI, settings, and workspace management
- Web3 wallet UI for local metadata only (no on-chain operations)

Private services (not included):
- Ori Token issuance, daily credits, cashout, and anti-cheat
- Marketplace payments and Ori-specific monetization flows

Details: `docs/architecture/open-core-boundaries.md`

Additional terms:
- `ORI_SERVICES.md` (private services access)
- `TRADEMARKS.md` (branding and trademark use)


## ✨ What You Can Do

- **Multiple agents** — create named agents, each with its own provider/model, system prompt, skills, and MCP servers.
- **Workspaces** — group agents to collaborate, with shared notes, files, and tasks scoped to the workspace.
- **Tasks & scheduling** — assign tasks to agents and run them on a cron-like schedule, with structured result storage (CSV/JSON output specs).
- **Missions & Action Center** — let a workspace work autonomously on a recurring cadence under an autonomy policy, and triage its findings (“opportunities”) from one cross-workspace inbox.
- **Multi-agent orchestration** — chain agents into workflows and combine their results.
- **MCP servers & Skills** — extend agents with external tools (MCP) and reusable prompt-based capabilities (Skills), bound per workspace.
- **Sessions** — persistent, searchable chat history with folders and tagging.
- **Private Vault** — local encrypted storage for secrets and sensitive records, backed by the OS keychain.
- **Usage & cost tracking** — monitor token usage and spend across providers.
- **Runs anywhere** — browser UI, HTTP API, or the macOS menu bar app.

## 🤖 Supported Providers

Ori Agent supports multiple AI providers, giving you flexibility in choosing your preferred AI model:

### Cloud Providers
- **OpenAI**
  - Requires: `OPENAI_API_KEY`
  - Best for: Production use, latest models, reliable performance

- **Anthropic Claude**
  - Requires: `ANTHROPIC_API_KEY`
  - Best for: Long context windows, detailed reasoning

- **Google Gemini**
  - Requires: `GEMINI_API_KEY`
  - Best for: Large context, multimodal tasks

### Local Providers

Run models entirely on your machine—no API key required. Each defaults to a local endpoint you can override via environment variable.

- **Ollama** - Run models locally on your machine
  - Endpoint: `OLLAMA_BASE_URL` (default `http://localhost:11434`)
  - Best for: Privacy, offline use, cost savings
  - Supports: Llama 3, Mistral, Phi-3, and other Ollama models

- **LM Studio** - OpenAI-compatible local server
  - Endpoint: `LM_STUDIO_BASE_URL` (default `http://localhost:1234/v1`), model via `LM_STUDIO_MODEL`

- **MLX LM** - Apple Silicon local inference via `mlx_lm.server`
  - Endpoint: `MLX_LM_BASE_URL` (default `http://localhost:8080/v1`), model via `MLX_LM_MODEL`

### CLI Agent Providers

If you already use a coding-agent CLI, Ori Agent auto-detects its login and registers it as a provider—no extra API key needed.

- **Codex** - registered when OpenAI Codex CLI credentials are found
- **Claude Code** - registered when Claude CLI credentials are found

## 🚀 Quick Start
### For Mac Users
Download and install the DMG from the latest release:
- https://github.com/johnjallday/ori-agent/releases/latest

Open the DMG and drag `OriAgent.app` to Applications. On macOS, Ori Agent runs as a **menu bar app** that starts/stops the server, shows status, and can auto-start on login.


### For Devs

### Prerequisites
- Go 1.25 or later
- An API key from one of the cloud providers (OpenAI, Claude, or Gemini) **OR** a local provider running (Ollama, LM Studio, or MLX LM)

### Installation

#### Option 1: macOS DMG Installer (Recommended for macOS)

1. **Download the DMG** from the [latest release](https://github.com/johnjallday/ori-agent/releases/latest)

2. **Open the DMG** and drag `OriAgent.app` to Applications

3. **Handle macOS Security Warning**

   When first opening OriAgent, macOS may show one of these warnings:
   - *"OriAgent is damaged and can't be opened"* (most common)
   - *"Apple cannot verify OriAgent is free of malware"*

   **This is normal for open-source apps not notarized by Apple.** To install safely:

   **Method 1 - Right-Click (Easiest):**
   1. Drag `OriAgent.app` to Applications folder
   2. Right-click (or Control+click) `OriAgent.app` in Applications
   3. Select "Open" from the menu
   4. Click "Open" in the dialog that appears

   **Method 2 - Terminal Command:**
   ```bash
   xattr -rc /Applications/OriAgent.app
   open /Applications/OriAgent.app
   ```

   After the first launch, you can open normally by double-clicking.

4. **Configure your API key** through the Settings panel in the app, or export it:
   ```bash
   export OPENAI_API_KEY="your-api-key"
   ```

5. **Access the interface** at `http://localhost:8765`

#### Option 2: Build from Source

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

#### Option 3: Linux Package Installer

1. **Download the package** from the [latest release](https://github.com/johnjallday/ori-agent/releases/latest):

   **Debian/Ubuntu:**
   ```bash
   # Download .deb file
   wget https://github.com/johnjallday/ori-agent/releases/latest/download/ori-agent_{version}_amd64.deb

   # Install
   sudo dpkg -i ori-agent_{version}_amd64.deb
   ```

   **Red Hat/Fedora/CentOS:**
   ```bash
   # Download .rpm file
   wget https://github.com/johnjallday/ori-agent/releases/latest/download/ori-agent_{version}_amd64.rpm

   # Install
   sudo rpm -i ori-agent_{version}_amd64.rpm
   ```

2. **Configure API key** via environment variable or `/etc/ori-agent/settings.json`

3. **Start the service**:
   ```bash
   sudo systemctl start ori-agent
   sudo systemctl enable ori-agent  # Auto-start on boot
   ```

4. **Access the interface** at `http://localhost:8765`

#### Option 4: Windows Archive

1. **Download** `ori-agent_{version}_windows_x86_64.tar.gz` from the [latest release](https://github.com/johnjallday/ori-agent/releases/latest)

2. **Extract** the archive to a folder (e.g., `C:\Program Files\OriAgent`)

3. **Set API key** via Environment Variables or create `settings.json` in the same folder

4. **Run** `ori-agent.exe`

5. **Access the interface** at `http://localhost:8765`

## 🗂️ Workspaces, Missions & Action Center

A **workspace** groups one or more agents around a shared context—notes, files, and tasks—so they can collaborate instead of running in isolation. Each workspace binds its own MCP servers and Skills, so the same agent can have different tool access in different workspaces.

### Tasks & Scheduling

- Assign tasks to agents and run them on demand or on a **cron-like schedule**.
- Capture results in a structured format with **output specs** (CSV/JSON), so recurring runs append clean, validated rows you can use downstream.
- Multi-step **workflows** let you orchestrate several agents and combine their outputs.

### Missions

Give a workspace a **mission** and it runs autonomously on a cadence, surfacing findings without you kicking off each run. A mission's reach is bounded by its **autonomy policy**:

| Policy | What it can do |
|--------|----------------|
| **Watch** | Read-only tools. No writes anywhere. |
| **Propose** | Reads plus workspace-internal writes (draft notes/artifacts, recommended-task drafts). External-effect tools stay denied. |

Each MCP/skill binding is classified by side effect (read / write / external), and the autonomy gate enforces the policy per tool call—so a mission can't take an action you haven't authorized for that workspace.

### Action Center

Missions produce **opportunities** (findings) that collect in the **Action Center**, a cross-workspace triage inbox. From there you can open, **snooze**, **resolve**, or **dismiss** each item. Snoozed items drop out of the active view until their window elapses; a resolved issue that a later run re-detects re-surfaces automatically so it doesn't get silently buried.

## 🔌 MCP Servers

Ori Agent connects to **Model Context Protocol** servers for external tool capabilities. MCP servers are configured and enabled per workspace, and the **MCP** page lets you add servers, browse the tools they expose, and classify each binding's side effect for use under mission autonomy policies.

## 🧩 Skills

Ori Agent supports per-agent Skills compatible with the Claude/OpenAI skill format.

### Skill Locations

- `agents/<agent_id>/skills` (agent-scoped)
- `agents/skills` (repo-scoped)
- `.agents/skills` (compatibility; lowest priority)

Each Skill is a directory containing `SKILL.md` with YAML frontmatter (`name`, `description`) and a prompt body. Optional `agents/openai.yaml` provides UI metadata and dependency hints.

### Naming Rules

- `name`: lowercase letters, numbers, hyphens only; max 64 chars; must not include `anthropic` or `claude`; no XML tags.
- `description`: required; max 1024 chars; no XML tags.

## 💬 Session Management

Ori Agent includes a comprehensive session management system for organizing and managing your chat conversations.

### Features

- **Persistent Chat Sessions**: All conversations are automatically saved and can be resumed anytime
- **Folder Organization**: Group related sessions into folders for better organization
- **Multi-Tab Support**: Work with multiple sessions simultaneously in separate browser tabs
- **Full-Text Search**: Quickly find messages across all your sessions
- **Tagging System**: Add tags to sessions for easy categorization and filtering
- **Automatic Cleanup**: Optionally clean up old, inactive sessions to manage storage

### Session Storage

Sessions are stored in a SQLite database with an in-memory LRU cache for fast access:

- **Cache**: Recently accessed sessions are kept in memory for instant retrieval
- **Database**: All sessions are persisted to SQLite with full-text search support
- **Hybrid Architecture**: Automatic cache warming and write-through for optimal performance

### Storage Management

Configure session storage limits via the Settings page or `settings.json`:

| Setting | Default | Description |
|---------|---------|-------------|
| `session_cleanup_enabled` | `true` | Enable automatic cleanup of old sessions |
| `session_cleanup_days` | `30` | Days of inactivity before a session is eligible for cleanup |
| `session_max_count` | `1000` | Maximum number of sessions to keep (0 = unlimited) |

**Via Settings Page:**
1. Navigate to Settings → Session Management
2. Configure cleanup options and limits
3. View current storage statistics
4. Run manual cleanup if needed

**Via settings.json:**
```json
{
  "session_cleanup_enabled": true,
  "session_cleanup_days": 30,
  "session_max_count": 1000
}
```

### Session API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/sessions` | GET | List all sessions (with pagination, filtering) |
| `/api/sessions` | POST | Create a new session |
| `/api/sessions/{id}` | GET | Get a specific session |
| `/api/sessions/{id}` | PUT | Update session metadata |
| `/api/sessions/{id}` | DELETE | Delete a session |
| `/api/sessions/{id}/messages` | GET | Get messages for a session |
| `/api/sessions/{id}/messages` | POST | Add a message to a session |
| `/api/sessions/search` | GET | Search across all sessions |
| `/api/sessions/storage/stats` | GET | Get storage statistics |
| `/api/sessions/cleanup` | POST | Trigger manual cleanup |

### Performance

The session system is optimized for handling many sessions efficiently:

- **100+ Sessions**: Tested to handle 150+ sessions with sub-millisecond list operations
- **Concurrent Access**: Thread-safe operations support multiple tabs and clients
- **Efficient Search**: Full-text search returns results in under 500µs for typical workloads

## 🔐 Private Vault

Ori Agent now includes a local encrypted vault for sensitive records that should not live in normal session history.

### What Goes In The Vault

- Connector secrets and API credentials
- Saved email snippets or private personal notes
- Sensitive identifiers or structured personal records

### What Does Not Go In The Vault

- Normal chat/session history by default
- Theme, onboarding, or general preference settings
- Global search indexes

### Storage Model

- New API-key writes prefer the configured secure secret store instead of persisting raw values in `settings.json`
- Existing installs still read legacy keys from `settings.json` for backward compatibility
- Vault records are encrypted with a per-install data encryption key stored in the secure secret store
- Free-form vault tags are encrypted at rest and are excluded from standard session search

### Platform Behavior

- macOS: uses Keychain when available
- Linux desktop: uses Secret Service when available
- Linux/headless fallback: requires `ORI_VAULT_PASSPHRASE`
- Windows: secure-store plumbing is present, but the vault should be treated as unavailable until a writable backend is configured

Ori Agent does not silently fall back to plaintext secret storage when secure storage is unavailable.

### Settings UI

Use **Settings → Private Vault** to:

- check vault status and backend mode
- unlock passphrase fallback mode
- create, browse, update, and delete saved vault entries
- review or revoke workspace-scoped persistent grants
- export encrypted vault bundles with explicit confirmation and a vault password

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 💬 Support

- 🐛 **Issues**: [GitHub Issues](https://github.com/johnjallday/ori-agent/issues)
- 💡 **Feature Requests**: Open an issue with the "enhancement" label
- 💬 **Discussions**: [GitHub Discussions](https://github.com/johnjallday/ori-agent/discussions)

While this app is very functional, there will be a lot of breaking changes. Feel free to give feedback.

buymeacoffee.com/johnjallday

---

Made with ❤️ using Go and modern web technologies
