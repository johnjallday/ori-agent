package skills

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// OpenAIMetadata represents optional UI metadata from agents/openai.yaml.
type OpenAIMetadata struct {
	DisplayName      string   `json:"display_name,omitempty"`
	ShortDescription string   `json:"short_description,omitempty"`
	Icon             string   `json:"icon,omitempty"`
	BrandColor       string   `json:"brand_color,omitempty"`
	DefaultPrompt    string   `json:"default_prompt,omitempty"`
	Tools            []string `json:"tools,omitempty"`
	MCPServers       []string `json:"mcp_servers,omitempty"`
	PlanningProfile  bool     `json:"planning_profile,omitempty"`
	Raw              any      `json:"raw,omitempty"`
}

func loadOpenAIMetadata(skillDir string) (*OpenAIMetadata, error) {
	if skillDir == "" {
		return nil, nil
	}

	primary := filepath.Join(skillDir, "agents", "openai.yaml")
	path := primary
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			alt := filepath.Join(skillDir, "agents", "openai.yml")
			if _, altErr := os.Stat(alt); altErr == nil {
				path = alt
			} else {
				return nil, nil
			}
		} else {
			return nil, err
		}
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	raw := map[string]any{}
	if err := yaml.Unmarshal(content, &raw); err != nil {
		return nil, err
	}

	meta := parseOpenAIMetadata(raw)
	meta.Raw = raw
	return meta, nil
}

func parseOpenAIMetadata(raw map[string]any) *OpenAIMetadata {
	if raw == nil {
		return nil
	}

	deps := getMap(raw, "dependencies", "requires")
	ui := getMap(raw, "interface", "ui", "metadata")
	capabilities := getMap(raw, "capabilities", "features")

	meta := &OpenAIMetadata{
		DisplayName:      getString(raw, "display_name", "displayName", "name", "title"),
		ShortDescription: getString(raw, "short_description", "shortDescription", "description"),
		Icon:             getString(raw, "icon", "icon_url", "iconUrl", "icon_path", "iconPath"),
		BrandColor:       getString(raw, "brand_color", "brandColor", "color"),
		DefaultPrompt:    getString(raw, "default_prompt", "defaultPrompt", "prompt", "system_prompt", "systemPrompt", "instructions"),
		Tools:            getStringSlice(raw, "tools", "tool_names", "toolNames", "allowed_tools", "allowed-tools"),
		MCPServers:       getStringSlice(raw, "mcp_servers", "mcpServers", "required_mcp_servers", "required-mcp-servers"),
		PlanningProfile:  getBool(raw, "planning_profile", "planningProfile", "workspace_planning", "workspacePlanning"),
	}

	if ui != nil {
		if meta.DisplayName == "" {
			meta.DisplayName = getString(ui, "display_name", "displayName", "name", "title")
		}
		if meta.ShortDescription == "" {
			meta.ShortDescription = getString(ui, "short_description", "shortDescription", "description")
		}
		if meta.Icon == "" {
			meta.Icon = getString(ui, "icon", "icon_url", "iconUrl", "icon_path", "iconPath")
		}
		if meta.BrandColor == "" {
			meta.BrandColor = getString(ui, "brand_color", "brandColor", "color")
		}
		if meta.DefaultPrompt == "" {
			meta.DefaultPrompt = getString(ui, "default_prompt", "defaultPrompt", "prompt", "system_prompt", "systemPrompt", "instructions")
		}
		if !meta.PlanningProfile {
			meta.PlanningProfile = getBool(ui, "planning_profile", "planningProfile", "workspace_planning", "workspacePlanning")
		}
	}

	if len(meta.Tools) == 0 && deps != nil {
		meta.Tools = getStringSlice(deps, "tools", "tool_names", "toolNames", "allowed_tools", "allowed-tools")
	}
	if len(meta.MCPServers) == 0 && deps != nil {
		meta.MCPServers = getStringSlice(deps, "mcp_servers", "mcpServers", "required_mcp_servers", "required-mcp-servers")
	}
	if !meta.PlanningProfile && capabilities != nil {
		meta.PlanningProfile = getBool(capabilities, "planning_profile", "planningProfile", "workspace_planning", "workspacePlanning")
	}

	return meta
}

func getMap(raw map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if val, ok := raw[key]; ok {
			if m, ok := val.(map[string]any); ok {
				return m
			}
			if m, ok := val.(map[interface{}]interface{}); ok {
				converted := make(map[string]any, len(m))
				for k, v := range m {
					if ks, ok := k.(string); ok {
						converted[ks] = v
					}
				}
				return converted
			}
		}
	}
	return nil
}

func getString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if val, ok := raw[key]; ok {
			if str, ok := val.(string); ok {
				return strings.TrimSpace(str)
			}
		}
	}
	return ""
}

func getStringSlice(raw map[string]any, keys ...string) []string {
	for _, key := range keys {
		if val, ok := raw[key]; ok {
			return interfaceSliceToStrings(val)
		}
	}
	return nil
}

func getBool(raw map[string]any, keys ...string) bool {
	for _, key := range keys {
		val, ok := raw[key]
		if !ok {
			continue
		}
		switch typed := val.(type) {
		case bool:
			return typed
		case string:
			normalized := strings.TrimSpace(strings.ToLower(typed))
			return normalized == "true" || normalized == "yes" || normalized == "1"
		}
	}
	return false
}
