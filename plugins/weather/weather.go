package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/johnjallday/dolphin-agent/pluginapi"
	"github.com/openai/openai-go/v2"
)

// ensure weatherTool implements pluginapi.Tool and pluginapi.VersionedTool
var _ pluginapi.Tool = (*weatherTool)(nil)
var _ pluginapi.VersionedTool = (*weatherTool)(nil)

// weatherTool implements pluginapi.Tool for fetching weather.
type weatherTool struct{}

// Definition returns the OpenAI function definition for get_weather.
func (w *weatherTool) Definition() openai.FunctionDefinitionParam {
	return openai.FunctionDefinitionParam{
		Name:        "get_weather",
		Description: openai.String("Get weather for a given location"),
		Parameters: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"location": map[string]any{
					"type":        "string",
					"description": "Location to get weather for",
				},
			},
			"required": []string{"location"},
		},
	}
}

// Call is invoked with the function arguments and returns weather data.
func (w *weatherTool) Call(ctx context.Context, args string) (string, error) {
	var p struct {
		Location string `json:"location"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", err
	}
	// TODO: replace with real API call.
	result := fmt.Sprintf("Sunny, 25°C in %s", p.Location)
	return result, nil
}

// Version returns the plugin version.
func (w *weatherTool) Version() string {
	return "1.0.0"
}

// Tool is the exported symbol that the host looks up.
var Tool weatherTool
