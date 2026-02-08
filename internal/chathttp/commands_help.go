package chathttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/version"
)

// HandleHelp handles the /help command to show available commands
func (ch *CommandHandler) HandleHelp(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	helpResponse := `## 🤖 Available Commands

**System Commands:**
- **/help** - Show this help message
- **/version** - Show application version and build info
- **/agent** - Display agent status dashboard
- **/agents** - List all available agents
- **/switch <agent-name>** - Switch to a different agent
- **/tools** - List all available plugin tools and operations
- **/skills** - List all available skills
- **/openapp <application-name>** - Open a desktop app directly (e.g., Obsidian)
- **/tool <name> <args>** - Execute a tool directly without LLM decision-making

**Workspace Commands:**
- **/workspace** - Show active workspaces
- **/workspace tasks** - List your pending tasks
- **/workspace task <task-id>** - Show details for a specific task
- **/workspace all** - Show all tasks (any status)

**Agent Management:**
- Use **/agent** to see current agent status and available agents
- Use **/agents** to see a list of all configured agents
- Use **/switch** to change between configured agents
- Each agent can have different plugins and configurations

**Plugin Tools:**
- Use **/tools** to see all available plugin operations
- Each tool shows available options and parameters
- Tools are specific to your current agent configuration
- Use **/tool** to execute tools directly (faster, no LLM overhead)

**Skills:**
- Use **/skills** to list available skills
- Use **/skill <name> <args>** or **/<name>** to run a skill

**Direct Tool Execution:**
The **/tool** command allows you to call tools directly without LLM decision-making:
- Format: /tool <tool_name> {"key": "value"}
- Example: /tool math {"operation": "add", "a": 5, "b": 3}
- Benefits: Faster execution, no API costs, deterministic results

**Tips:**
- Commands must start with **/** (forward slash)
- Agent names are case-sensitive when switching
- Use the web interface to configure plugins and agents
- Workspaces allow multiple agents to collaborate on complex tasks
- Direct tool calls bypass the LLM for instant execution

Type any command above to get started!`

	response := map[string]any{
		"response": helpResponse,
	}
	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}

// HandleVersion handles the /version command to show app version and build info
func (ch *CommandHandler) HandleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	info := version.GetBuildInfo()

	var versionResponse strings.Builder
	versionResponse.WriteString("## 📦 Ori Agent Version\n\n")
	versionResponse.WriteString(fmt.Sprintf("**Version:** %s\n", info["version"]))
	versionResponse.WriteString(fmt.Sprintf("**API Version:** %s\n", info["api_version"]))

	if info["git_commit"] != "unknown" {
		versionResponse.WriteString(fmt.Sprintf("**Git Commit:** %s\n", info["git_commit"]))
	}
	if info["build_date"] != "unknown" {
		versionResponse.WriteString(fmt.Sprintf("**Build Date:** %s\n", info["build_date"]))
	}

	response := map[string]any{
		"response": versionResponse.String(),
	}
	if encErr := json.NewEncoder(w).Encode(response); encErr != nil {
		logger.Error("Failed to encode response", logger.Fields{"error": encErr})
	}
}
