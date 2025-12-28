# AI Features Analysis & Roadmap

This document outlines the current AI-powered features in Ori Agent and opportunities for future enhancements.

---

## Current AI-Powered Features

### 1. Agent Auto-Configuration
- **Location**: `internal/agenthttp/auto_config.go`
- **Description**: AI-powered wizard that generates optimal agent configurations based on user description
- **How it works**:
  - User provides description of agent's purpose
  - System model (Claude/GPT) analyzes and recommends:
    - Agent type (tool-calling, general, research)
    - Optimal model selection
    - Temperature settings
    - Custom system prompt
  - Validates and sanitizes AI responses
- **Page**: Integrated in agent creation flow on `/agents`

### 2. Intelligent Task Orchestration
- **Location**: `internal/agentstudio/orchestrator.go`
- **Description**: AI-powered mission breakdown and autonomous agent coordination
- **Capabilities**:
  - Analyzes high-level missions using LLM
  - Automatically breaks missions into subtasks
  - Assigns tasks to most appropriate agents
  - Executes tasks sequentially with real-time event streaming
- **Page**: Agent Studios workspace canvas (`/studios/[id]/canvas`)

### 3. Cost Tracking & Usage Analytics
- **Location**: `internal/llm/cost_tracker.go`
- **Features**:
  - Tracks API usage across providers (OpenAI, Anthropic, Ollama)
  - Calculates costs per agent, model, and provider
  - Maintains usage records with detailed breakdown
  - Time-range based analytics
- **Page**: Usage & Cost Tracking (`/usage`)

### 4. Tool Error Hints
- **Location**: `internal/chathttp/tool_error_hints.go`
- **Description**: AI-augmented error messages for tool execution failures
  - Detects common plugin manager errors
  - Suggests copy-paste solutions for fixing uncommitted changes
  - Provides context-aware remediation steps

### 5. Direct Tool Calling
- **Location**: `internal/chathttp/direct_tool_executor.go`
- **Description**: Ability to call plugins directly via `/tool` command
  - Bypasses LLM decision-making for specific tasks
  - Supports JSON arguments
  - Provides helpful error messages with available tools
  - Returns structured results with execution metrics

### 6. Multi-Provider LLM Support
- **Location**: `internal/llm/` directory
- **Providers**: OpenAI, Anthropic Claude, Ollama
- **Features**:
  - Unified provider interface
  - Factory pattern for provider selection
  - Cost calculation per provider
  - Model capability awareness

---

## Opportunities for New AI Features

### High Impact

#### A. Smart Plugin Recommendations
- **Description**: Suggest plugins based on agent configuration and purpose
- **Implementation**:
  - Analyze agent's system prompt and description
  - Match against plugin capabilities
  - Recommend MCP servers for specific tasks
  - Identify missing capabilities
- **Location**: New module `internal/pluginhttp/recommender.go`
- **Pages**: `/agents`, `/agents-edit`, `/plugins`

#### B. AI Plugin Configuration Wizard
- **Description**: AI-guided plugin setup similar to agent auto-config
- **Implementation**:
  - Analyze plugin requirements
  - Generate configuration from user description
  - Test configurations and suggest optimizations
  - Provide step-by-step setup guidance
- **Location**: Extend `internal/pluginhttp/` handlers
- **Pages**: `/plugins`, plugin detail modals

#### C. Conversation Memory & Summarization
- **Description**: Track and summarize conversation history
- **Implementation**:
  - Maintain conversation history per agent
  - Use LLM to summarize long conversations
  - Suggest relevant previous contexts
  - Enable context carryover between sessions
- **Location**: New module `internal/chathttp/conversation_memory.go`
- **Pages**: Chat interface, session management

#### D. Smart Onboarding
- **Description**: AI-guided initial setup based on use case
- **Implementation**:
  - Ask user about their goals
  - Recommend initial agent configurations
  - Suggest plugins and MCP servers
  - Create quick-start templates
- **Location**: Extend `internal/onboardinghttp/handlers.go`
- **Pages**: `/onboarding`, initial setup flow

### Medium Impact

#### E. Agent Performance Insights
- **Description**: Analyze agent usage patterns and suggest improvements
- **Implementation**:
  - Track success rates and execution times
  - Identify performance bottlenecks
  - Suggest model/temperature adjustments
  - Recommend system prompt refinements
- **Location**: Extend `internal/agenthttp/statistics.go`
- **Pages**: `/agents-detail`, dashboard

#### F. Cost Optimization Advisor
- **Description**: AI-powered cost analysis and recommendations
- **Implementation**:
  - Identify expensive operations
  - Suggest cheaper model alternatives
  - Predict monthly costs
  - Trade-off analysis (quality vs cost)
- **Location**: Extend `internal/llm/cost_tracker.go`
- **Pages**: `/usage`

#### G. Workspace Template Generator
- **Description**: Auto-generate agent teams for missions
- **Implementation**:
  - Analyze user's mission description
  - Suggest optimal agent configurations
  - Recommend task workflows
  - Create pre-configured workspace templates
- **Location**: `internal/agentstudio/templates/`
- **Pages**: `/studios`, workspace creation

#### H. Smart Search
- **Description**: Semantic search across plugins, agents, and MCP servers
- **Implementation**:
  - Vector-based similarity search
  - Contextual suggestions
  - Related items discovery
  - Natural language queries
- **Location**: New module `internal/searchhttp/`
- **Pages**: Global search, marketplace

### Quick Wins

#### I. Auto-Generate Plugin Descriptions
- **Description**: Analyze uploaded plugins and generate descriptions
- **Implementation**:
  - Parse plugin metadata
  - Analyze function signatures
  - Generate human-readable descriptions
  - Suggest categorization
- **Location**: `internal/pluginhttp/plugins_handler.go`
- **Pages**: `/plugins` upload flow

#### J. Model Selection Assistant
- **Description**: Recommend model based on task complexity
- **Implementation**:
  - Analyze task requirements
  - Consider cost constraints
  - Suggest appropriate model tier
  - Explain trade-offs
- **Location**: New module `internal/llm/model_selector.go`
- **Pages**: `/models`, agent configuration

---

## Technical Integration Patterns

### Using System Model (lightweight tasks)
Best for: Configuration analysis, plugin categorization, error hints

```go
systemModel := h.configManager.GetSystemModel()
provider := h.llmFactory.GetSystemModelProvider()
```

### Using Agent's Configured Model (interactive features)
Best for: Conversation enhancement, task optimization

```go
provider := agent.GetLLMProvider()
```

### Cost Tracking Integration
All AI calls should be tracked:

```go
costTracker.RecordUsage(agentName, model, inputTokens, outputTokens)
```

---

## Pages with AI Enhancement Potential

| Page | Current AI | Potential Enhancements |
|------|-----------|------------------------|
| `/agents` | Auto-config | Plugin recommendations, performance insights |
| `/agents-edit` | - | Refinement suggestions, model recommendations |
| `/agents-detail` | - | Performance analysis, optimization tips |
| `/plugins` | - | Smart recommendations, auto-config, descriptions |
| `/marketplace` | - | Semantic search, personalized recommendations |
| `/settings` | - | Intelligent setup wizard |
| `/usage` | Cost tracking | Optimization advisor, predictions |
| `/studios` | Task orchestration | Template generation, team advisor |
| `/workspace-canvas` | Mission breakdown | Task optimization, dependency analysis |
| `/models` | - | Selection assistant, capability matching |
| `/mcp` | - | Server recommendations, auto-configuration |

---

## Implementation Priority

1. **Phase 1 - Quick Wins**
   - Auto-generate plugin descriptions
   - Model selection assistant

2. **Phase 2 - High Impact**
   - Smart plugin recommendations
   - AI plugin configuration wizard
   - Smart onboarding

3. **Phase 3 - Medium Impact**
   - Agent performance insights
   - Cost optimization advisor
   - Conversation memory

4. **Phase 4 - Advanced**
   - Workspace template generator
   - Smart semantic search
   - Team composition advisor
