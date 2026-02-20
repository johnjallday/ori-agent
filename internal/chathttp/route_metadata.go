package chathttp

import (
	"encoding/json"
	"strings"
)

const (
	routeModeAssistantChat  = "assistant_chat"
	routeModeDirectTool     = "direct_tool"
	routeModeSpecialistFlow = "specialist_handoff"
)

type chatRouteMetadata struct {
	Mode      string
	ToolName  string
	Provider  string
	Reason    string
	ToolCount int
}

func attachRouteMetadata(payload map[string]any, meta chatRouteMetadata) map[string]any {
	if payload == nil {
		payload = make(map[string]any)
	}

	mode := strings.TrimSpace(meta.Mode)
	if mode == "" {
		mode = routeModeAssistantChat
	}

	route := map[string]any{
		"mode": mode,
	}
	payload["route_mode"] = mode

	if toolName := strings.TrimSpace(meta.ToolName); toolName != "" {
		route["tool_name"] = toolName
		payload["tool_name"] = toolName
	}
	if provider := strings.TrimSpace(meta.Provider); provider != "" {
		route["provider"] = provider
		payload["provider"] = provider
	}
	if reason := strings.TrimSpace(meta.Reason); reason != "" {
		route["reason"] = reason
	}
	if meta.ToolCount > 0 {
		route["tool_count"] = meta.ToolCount
		payload["tool_count"] = meta.ToolCount
	}

	payload["route"] = route
	return payload
}

func inferUtilityProvider(toolName, rawResult string) string {
	name := strings.TrimSpace(strings.ToLower(toolName))
	switch name {
	case "time":
		return "system-clock"
	case "browser":
		return "browser-automation"
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(rawResult), &payload); err == nil {
		if source, ok := payload["source"].(string); ok && strings.TrimSpace(source) != "" {
			return strings.TrimSpace(source)
		}
	}

	switch name {
	case "weather":
		return "open-meteo.com"
	case "web_search":
		return "duckduckgo.com"
	case "web_fetch":
		return "web-fetch"
	default:
		return ""
	}
}

func isNativeUtilityToolName(toolName string) bool {
	switch strings.TrimSpace(strings.ToLower(toolName)) {
	case "time", "weather", "web_search", "web_fetch", "browser":
		return true
	default:
		return false
	}
}
