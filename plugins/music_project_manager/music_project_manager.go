package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/johnjallday/dolphin-agent/pluginapi"
	"github.com/openai/openai-go/v2"
)

// Settings represents the plugin configuration
type Settings struct {
	DefaultTemplate string `json:"default_template"`
	ProjectDir      string `json:"project_dir"`
	TemplateDir     string `json:"template_dir"`
	Initialized     bool   `json:"initialized"`
}

// SettingsManager manages plugin settings
type SettingsManager struct {
	settings *Settings
}

var globalSettings = &SettingsManager{}

// musicProjectManagerTool implements pluginapi.Tool for music project management.
type musicProjectManagerTool struct{}

// ensure musicProjectManagerTool implements required interfaces at compile time
var _ pluginapi.Tool = (*musicProjectManagerTool)(nil)
var _ pluginapi.VersionedTool = (*musicProjectManagerTool)(nil)
var _ pluginapi.ConfigurableTool = (*musicProjectManagerTool)(nil)

// Definition returns the OpenAI function definition for music project management operations.
func (m *musicProjectManagerTool) Definition() openai.FunctionDefinitionParam {
	return openai.FunctionDefinitionParam{
		Name:        "music_project_manager",
		Description: openai.String("Manage music projects: create projects, configure settings, and setup plugin"),
		Parameters: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"operation": map[string]any{
					"type":        "string",
					"description": "Operation to perform",
					"enum":        []string{"create_project", "set_project_dir", "set_template_dir", "get_settings", "init_setup", "complete_setup"},
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Project name (required for create_project)",
				},
				"bpm": map[string]any{
					"type":        "integer",
					"description": "BPM for the project (optional for create_project)",
					"minimum":     60,
					"maximum":     300,
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Directory path (required for set_project_dir, set_template_dir)",
				},
				"project_dir": map[string]any{
					"type":        "string",
					"description": "Project directory path (required for complete_setup)",
				},
				"template_dir": map[string]any{
					"type":        "string",
					"description": "Template directory path (required for complete_setup)",
				},
			},
			"required": []string{"operation"},
		},
	}
}

// Call is invoked with the function arguments and dispatches to the appropriate operation.
func (m *musicProjectManagerTool) Call(ctx context.Context, args string) (string, error) {
	var p struct {
		Operation   string `json:"operation"`
		Name        string `json:"name"`
		BPM         int    `json:"bpm"`
		Path        string `json:"path"`
		ProjectDir  string `json:"project_dir"`
		TemplateDir string `json:"template_dir"`
	}

	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	switch p.Operation {
	case "create_project":
		if p.Name == "" {
			return "", fmt.Errorf("name is required for create_project operation")
		}
		return m.handleCreateProject(p.Name, p.BPM)

	case "set_project_dir":
		if p.Path == "" {
			return "", fmt.Errorf("path is required for set_project_dir operation")
		}
		return m.handleSetProjectDir(p.Path)

	case "set_template_dir":
		if p.Path == "" {
			return "", fmt.Errorf("path is required for set_template_dir operation")
		}
		return m.handleSetTemplateDir(p.Path)

	case "get_settings":
		return m.handleGetSettings()

	case "init_setup":
		return m.handleInitSetup()

	case "complete_setup":
		if p.ProjectDir == "" || p.TemplateDir == "" {
			return "", fmt.Errorf("both project_dir and template_dir are required for complete_setup operation")
		}
		return m.handleCompleteSetup(p.ProjectDir, p.TemplateDir)

	default:
		return "", fmt.Errorf("unknown operation %q", p.Operation)
	}
}

// handleCreateProject creates a new music project
func (m *musicProjectManagerTool) handleCreateProject(name string, bpm int) (string, error) {
	if !globalSettings.IsInitialized() {
		return "Music Project Manager needs to be set up first. Please run music_project_manager with operation 'init_setup' to begin the setup process.", nil
	}

	settings := globalSettings.getCurrentSettings()
	if settings.DefaultTemplate == "" {
		return "", fmt.Errorf("default template not configured")
	}

	// Make the project folder
	projectDir := filepath.Join(settings.ProjectDir, name)
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create project directory: %w", err)
	}

	// Copy the .RPP
	dest := filepath.Join(projectDir, name+".RPP")
	data, err := os.ReadFile(settings.DefaultTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to read template file: %w", err)
	}
	if err := os.WriteFile(dest, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write project file: %w", err)
	}

	// Patch TEMPO if needed
	if bpm > 0 {
		content, err := os.ReadFile(dest)
		if err != nil {
			return "", fmt.Errorf("failed to read project file for BPM update: %w", err)
		}
		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			trimmed := strings.TrimLeft(line, " \t")
			if strings.HasPrefix(trimmed, "TEMPO ") {
				indent := line[:len(line)-len(trimmed)]
				parts := strings.Fields(trimmed)
				if len(parts) >= 2 {
					parts[1] = strconv.Itoa(bpm)
					lines[i] = indent + strings.Join(parts, " ")
				}
				break
			}
		}
		if err := os.WriteFile(dest, []byte(strings.Join(lines, "\n")), 0644); err != nil {
			return "", fmt.Errorf("failed to update BPM in project file: %w", err)
		}
	}

	// Launch Reaper
	cmd := exec.Command("open", "-a", "Reaper", dest)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to launch Reaper: %w", err)
	}

	msg := fmt.Sprintf("Created and launched project: %s", dest)
	if bpm > 0 {
		msg += fmt.Sprintf(" (BPM %d)", bpm)
	}
	return msg, nil
}

// handleSetProjectDir sets the project directory
func (m *musicProjectManagerTool) handleSetProjectDir(path string) (string, error) {
	if err := os.MkdirAll(path, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", path, err)
	}

	settings := globalSettings.getCurrentSettings()
	settings.ProjectDir = path
	globalSettings.settings = settings

	// Update agents.json to persist the setting
	if err := updateAgentsJSON(path, ""); err != nil {
		// Don't fail the operation if agents.json update fails, just log it
		return fmt.Sprintf("Project directory set to: %s\n⚠️  Could not persist to agent config: %v\n\nPlease check that the agents directory is writable and the plugin has access to it.", path, err), nil
	}

	return fmt.Sprintf("✅ Project directory set to: %s\n✅ Successfully persisted to agent config", path), nil
}

// handleSetTemplateDir sets the template directory
func (m *musicProjectManagerTool) handleSetTemplateDir(path string) (string, error) {
	if err := os.MkdirAll(path, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", path, err)
	}

	settings := globalSettings.getCurrentSettings()
	settings.TemplateDir = path
	if settings.DefaultTemplate != "" {
		settings.DefaultTemplate = filepath.Join(path, "default.RPP")
	}
	globalSettings.settings = settings

	// Update agents.json to persist the setting
	if err := updateAgentsJSON("", path); err != nil {
		// Don't fail the operation if agents.json update fails, just log it
		return fmt.Sprintf("Template directory set to: %s\n⚠️  Could not persist to agent config: %v\n\nPlease check that the agents directory is writable and the plugin has access to it.", path, err), nil
	}

	return fmt.Sprintf("✅ Template directory set to: %s\n✅ Successfully persisted to agent config", path), nil
}

// handleGetSettings returns current settings
func (m *musicProjectManagerTool) handleGetSettings() (string, error) {
	settings := globalSettings.getCurrentSettings()
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal settings: %w", err)
	}
	return string(data), nil
}

// handleInitSetup checks setup status and provides guidance
func (m *musicProjectManagerTool) handleInitSetup() (string, error) {
	if globalSettings.IsInitialized() {
		settings := globalSettings.getCurrentSettings()
		return "Music Project Manager is already set up and ready to use.\n\nCurrent settings:\n" +
			fmt.Sprintf("- Project Directory: %s\n", settings.ProjectDir) +
			fmt.Sprintf("- Template Directory: %s\n", settings.TemplateDir) +
			fmt.Sprintf("- Default Template: %s\n", settings.DefaultTemplate) +
			"\nUse operation 'get_settings' to view detailed configuration.", nil
	}

	suggestedProjectDir := "/Users/jj/Music/Projects"
	suggestedTemplateDir := "/Users/jj/Music/Templates"

	return fmt.Sprintf("🎵 Welcome to Music Project Manager! \n\nThis is your first time using the plugin. Please complete the setup by providing:\n\n"+
		"1. **Project Directory** - Where new music projects will be created\n"+
		"   Suggested: %s\n\n"+
		"2. **Template Directory** - Where your .RPP template files are stored\n"+
		"   Suggested: %s\n\n"+
		"Please use operation 'complete_setup' with project_dir and template_dir parameters to finish the setup.\n\n"+
		"Example: music_project_manager(operation=\"complete_setup\", project_dir=\"%s\", template_dir=\"%s\")",
		suggestedProjectDir, suggestedTemplateDir, suggestedProjectDir, suggestedTemplateDir), nil
}

// handleCompleteSetup completes initial setup
func (m *musicProjectManagerTool) handleCompleteSetup(projectDir, templateDir string) (string, error) {
	// Validate and create directories
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create project directory %s: %w", projectDir, err)
	}
	if err := os.MkdirAll(templateDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create template directory %s: %w", templateDir, err)
	}

	// Create and save settings
	settings := &Settings{
		ProjectDir:      projectDir,
		TemplateDir:     templateDir,
		DefaultTemplate: filepath.Join(templateDir, "default.RPP"),
		Initialized:     true,
	}

	globalSettings.settings = settings

	// Update agents.json to persist both settings
	persistMessage := ""
	if err := updateAgentsJSON(projectDir, templateDir); err != nil {
		persistMessage = fmt.Sprintf(" (Warning: Could not persist to agent config: %v)", err)
	} else {
		persistMessage = " and persisted to agent config"
	}

	return fmt.Sprintf("✅ Setup completed successfully!\n\n"+
		"Configuration saved%s:\n"+
		"- Project Directory: %s\n"+
		"- Template Directory: %s\n"+
		"- Default Template: %s\n\n"+
		"You can now use operation 'create_project' to create new music projects. "+
		"Make sure to place a default.RPP template file in your template directory for best results.",
		persistMessage, settings.ProjectDir, settings.TemplateDir, settings.DefaultTemplate), nil
}

// Settings interface implementation
func (m *musicProjectManagerTool) GetSettings() (string, error) {
	return globalSettings.GetSettings()
}

func (m *musicProjectManagerTool) SetSettings(settings string) error {
	return globalSettings.SetSettings(settings)
}

func (m *musicProjectManagerTool) GetDefaultSettings() (string, error) {
	return globalSettings.GetDefaultSettings()
}

func (m *musicProjectManagerTool) IsInitialized() bool {
	return globalSettings.IsInitialized()
}

// Version returns the plugin version.
func (m *musicProjectManagerTool) Version() string {
	return "1.0.0"
}

// SettingsManager implementation
func (sm *SettingsManager) GetSettings() (string, error) {
	if sm.settings == nil {
		sm.settings = sm.getDefaultSettings()
	}
	data, err := json.MarshalIndent(sm.settings, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal settings: %w", err)
	}
	return string(data), nil
}

func (sm *SettingsManager) SetSettings(settingsJSON string) error {
	var settings Settings
	if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
		return fmt.Errorf("failed to unmarshal settings: %w", err)
	}
	sm.settings = &settings
	return nil
}

func (sm *SettingsManager) GetDefaultSettings() (string, error) {
	defaults := sm.getDefaultSettings()
	data, err := json.MarshalIndent(defaults, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal default settings: %w", err)
	}
	return string(data), nil
}

func (sm *SettingsManager) IsInitialized() bool {
	if sm.settings == nil {
		sm.settings = sm.getDefaultSettings()
	}
	return sm.settings.Initialized
}

func (sm *SettingsManager) getDefaultSettings() *Settings {
	// Try to load defaults from agents.json first
	if defaults := loadDefaultsFromAgentsJSON(); defaults != nil {
		return defaults
	}

	// Fallback to hardcoded defaults
	return &Settings{
		DefaultTemplate: "/Users/jj/Music/Templates/default.RPP",
		ProjectDir:      "/Users/jj/Music/Projects",
		TemplateDir:     "/Users/jj/Music/Templates",
		Initialized:     false,
	}
}

// loadDefaultsFromAgentsJSON attempts to load default settings from individual agent files
func loadDefaultsFromAgentsJSON() *Settings {
	// First, find the main agents.json to get current agent
	possibleMainPaths := []string{
		"../agents.json",
		"../../agents.json",
		"agents.json",
		"/Users/jj/Workspace/johnj-programming/projects/dolphin/dolphin-agent/agents.json",
	}

	var mainAgentsFilePath string
	for _, path := range possibleMainPaths {
		if _, err := os.Stat(path); err == nil {
			mainAgentsFilePath = path
			break
		}
	}

	if mainAgentsFilePath == "" {
		return nil
	}

	// Read main agents.json to get current agent name
	data, err := os.ReadFile(mainAgentsFilePath)
	if err != nil {
		return nil
	}

	var indexConfig AgentsIndexConfig
	if err := json.Unmarshal(data, &indexConfig); err != nil {
		return nil
	}

	currentAgent := indexConfig.Current
	if currentAgent == "" {
		return nil
	}

	// Try to load the individual agent config file
	baseDir := filepath.Dir(mainAgentsFilePath)
	agentsDir := filepath.Join(baseDir, "agents")
	agentFilePath := filepath.Join(agentsDir, currentAgent+".json")

	var agentConfig IndividualAgentConfig
	if agentData, err := os.ReadFile(agentFilePath); err == nil {
		// Individual agent file exists
		if err := json.Unmarshal(agentData, &agentConfig); err != nil {
			return nil
		}
	} else {
		// Fall back to reading from main agents.json for backward compatibility
		var mainConfig AgentsConfig
		if err := json.Unmarshal(data, &mainConfig); err != nil {
			return nil
		}

		if agent, exists := mainConfig.Agents[currentAgent]; exists {
			agentConfig = IndividualAgentConfig{
				Settings: agent.Settings,
				Plugins:  agent.Plugins,
			}
		} else {
			return nil
		}
	}

	// Look for music_project_manager plugin in the agent config
	if plugin, exists := agentConfig.Plugins["music_project_manager"]; exists {
		if params, ok := plugin.Definition.Parameters["properties"].(map[string]interface{}); ok {
			settings := &Settings{
				Initialized: true, // If we found settings, consider initialized
			}

			// Extract default values from the plugin definition
			if projectDirParam, exists := params["project_dir"].(map[string]interface{}); exists {
				if defaultVal, hasDefault := projectDirParam["default"].(string); hasDefault {
					settings.ProjectDir = defaultVal
				}
			}

			if templateDirParam, exists := params["template_dir"].(map[string]interface{}); exists {
				if defaultVal, hasDefault := templateDirParam["default"].(string); hasDefault {
					settings.TemplateDir = defaultVal
					settings.DefaultTemplate = filepath.Join(defaultVal, "default.RPP")
				}
			}

			// If we got valid directories, return this settings object
			if settings.ProjectDir != "" && settings.TemplateDir != "" {
				return settings
			}
		}
	}

	return nil
}

func (sm *SettingsManager) getCurrentSettings() *Settings {
	if sm.settings == nil {
		sm.settings = sm.getDefaultSettings()
	}
	return sm.settings
}

// IndividualAgentConfig represents the structure of an individual agent file
type IndividualAgentConfig struct {
	Settings AgentSettings           `json:"Settings"`
	Plugins  map[string]PluginConfig `json:"Plugins"`
}

// AgentsIndexConfig represents the main agents.json structure (for finding current agent)
type AgentsIndexConfig struct {
	Agents  map[string]interface{} `json:"agents"` // We only need to read the current agent name
	Current string                 `json:"current"`
}

type AgentSettings struct {
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
}

type PluginConfig struct {
	Definition PluginDefinition `json:"Definition"`
	Path       string           `json:"Path"`
	Version    string           `json:"Version"`
}

type PluginDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// AgentInfo represents an agent in the main agents.json file
type AgentInfo struct {
	Settings AgentSettings           `json:"Settings"`
	Plugins  map[string]PluginConfig `json:"Plugins"`
}

// AgentsConfig represents the complete agents.json structure (legacy format)
type AgentsConfig struct {
	Agents  map[string]AgentInfo `json:"agents"`
	Current string               `json:"current"`
}

// updateAgentsJSON updates individual agent configuration files with new directory settings
func updateAgentsJSON(projectDir, templateDir string) error {
	// First, find and read the main agents.json to get the current agent name
	possibleMainPaths := []string{
		"/Users/jj/Workspace/johnj-programming/projects/dolphin/dolphin-agent/agents.json",
		"../../../agents.json",
		"../../agents.json", 
		"../agents.json",
		"agents.json",
	}

	var mainAgentsFilePath string
	for _, path := range possibleMainPaths {
		if _, err := os.Stat(path); err == nil {
			mainAgentsFilePath = path
			break
		}
	}

	if mainAgentsFilePath == "" {
		return fmt.Errorf("could not find main agents.json file in any of these locations: %v", possibleMainPaths)
	}

	// Read main agents.json to get current agent name
	data, err := os.ReadFile(mainAgentsFilePath)
	if err != nil {
		return fmt.Errorf("failed to read main agents.json: %w", err)
	}

	var indexConfig AgentsIndexConfig
	if err := json.Unmarshal(data, &indexConfig); err != nil {
		return fmt.Errorf("failed to parse main agents.json: %w", err)
	}

	currentAgent := indexConfig.Current
	if currentAgent == "" {
		return fmt.Errorf("no current agent specified in agents.json")
	}

	// Find the individual agent config file
	baseDir := filepath.Dir(mainAgentsFilePath)
	agentsDir := filepath.Join(baseDir, "agents")
	agentFilePath := filepath.Join(agentsDir, currentAgent+".json")

	// Create agents directory if it doesn't exist
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		return fmt.Errorf("failed to create agents directory: %w", err)
	}

	// Read individual agent config (or create if doesn't exist)
	var agentConfig IndividualAgentConfig
	if agentData, err := os.ReadFile(agentFilePath); err == nil {
		// File exists, parse it
		if err := json.Unmarshal(agentData, &agentConfig); err != nil {
			return fmt.Errorf("failed to parse individual agent config: %w", err)
		}
	} else {
		// File doesn't exist, try to migrate from main agents.json
		var mainConfig AgentsConfig
		if err := json.Unmarshal(data, &mainConfig); err != nil {
			return fmt.Errorf("failed to parse main agents.json for migration: %w", err)
		}

		// Check if the agent exists in main config
		if agent, exists := mainConfig.Agents[currentAgent]; exists {
			// Migrate from main config
			agentConfig = IndividualAgentConfig{
				Settings: agent.Settings,
				Plugins:  agent.Plugins,
			}
		} else {
			// Create a minimal config
			agentConfig = IndividualAgentConfig{
				Settings: AgentSettings{
					Model:       "gpt-4.1-nano",
					Temperature: 1.0,
				},
				Plugins: make(map[string]PluginConfig),
			}
		}
	}

	// Update the music_project_manager plugin configuration
	if plugin, exists := agentConfig.Plugins["music_project_manager"]; exists {
		// Update the default values in the plugin parameters
		if params, ok := plugin.Definition.Parameters["properties"].(map[string]interface{}); ok {
			if projectDir != "" {
				if projectDirParam, exists := params["project_dir"].(map[string]interface{}); exists {
					projectDirParam["default"] = projectDir
				} else {
					// If project_dir parameter doesn't exist, create it
					params["project_dir"] = map[string]interface{}{
						"description": "Project directory path (required for complete_setup)",
						"type":        "string",
						"default":     projectDir,
					}
				}

				// Also update path default to parent of project_dir
				if pathParam, exists := params["path"].(map[string]interface{}); exists {
					pathParam["default"] = filepath.Dir(projectDir)
				} else {
					// If path parameter doesn't exist, create it
					params["path"] = map[string]interface{}{
						"description": "Directory path (required for set_project_dir, set_template_dir)",
						"type":        "string",
						"default":     filepath.Dir(projectDir),
					}
				}
			}

			if templateDir != "" {
				if templateDirParam, exists := params["template_dir"].(map[string]interface{}); exists {
					templateDirParam["default"] = templateDir
				} else {
					// If template_dir parameter doesn't exist, create it
					params["template_dir"] = map[string]interface{}{
						"description": "Template directory path (required for complete_setup)",
						"type":        "string",
						"default":     templateDir,
					}
				}
			}
		}

		// Update the plugin in the agent config
		agentConfig.Plugins["music_project_manager"] = plugin
	} else {
		return fmt.Errorf("music_project_manager plugin not found in agent %s", currentAgent)
	}

	// Write the updated individual agent config
	updatedData, err := json.MarshalIndent(agentConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal updated agent config: %w", err)
	}

	if err := os.WriteFile(agentFilePath, updatedData, 0644); err != nil {
		return fmt.Errorf("failed to write to %s: %w", agentFilePath, err)
	}

	return nil
}

// Tool is the exported symbol that the host application will look up.
var Tool musicProjectManagerTool
