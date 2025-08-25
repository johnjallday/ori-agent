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

	return fmt.Sprintf("Project directory set to: %s", path), nil
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

	return fmt.Sprintf("Template directory set to: %s", path), nil
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

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	suggestedProjectDir := filepath.Join(homeDir, "Music", "Projects")
	suggestedTemplateDir := filepath.Join(homeDir, "Music", "Templates")

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

	return fmt.Sprintf("✅ Setup completed successfully!\n\n"+
		"Configuration saved:\n"+
		"- Project Directory: %s\n"+
		"- Template Directory: %s\n"+
		"- Default Template: %s\n\n"+
		"You can now use operation 'create_project' to create new music projects. "+
		"Make sure to place a default.RPP template file in your template directory for best results.",
		settings.ProjectDir, settings.TemplateDir, settings.DefaultTemplate), nil
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
	homeDir, _ := os.UserHomeDir()
	return &Settings{
		DefaultTemplate: filepath.Join(homeDir, "Music", "Templates", "default.RPP"),
		ProjectDir:      filepath.Join(homeDir, "Music", "Projects"),
		TemplateDir:     filepath.Join(homeDir, "Music", "Templates"),
		Initialized:     false,
	}
}

func (sm *SettingsManager) getCurrentSettings() *Settings {
	if sm.settings == nil {
		sm.settings = sm.getDefaultSettings()
	}
	return sm.settings
}

// Tool is the exported symbol that the host application will look up.
var Tool musicProjectManagerTool

