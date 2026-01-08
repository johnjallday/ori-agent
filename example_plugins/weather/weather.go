package main

//go:generate ../../bin/ori-plugin-gen -yaml=plugin.yaml -output=weather_generated.go

import (
	"context"
	"fmt"

	"github.com/johnjallday/ori-agent/pluginapi"
)

// GetWeatherTool implements pluginapi.Tool for fetching weather.
// Note: Compile-time interface check is in weather_generated.go
type GetWeatherTool struct {
	pluginapi.BasePlugin // Embed BasePlugin to get version/metadata methods for free
}

// Note: Definition() is inherited from BasePlugin, which automatically reads from plugin.yaml
// Note: Call() is auto-generated in weather_generated.go from plugin.yaml

// Execute contains the business logic - called by the generated Call() method
func (w *GetWeatherTool) Execute(ctx context.Context, params *Params) (string, error) {
	// TODO: replace with real API call.
	result := fmt.Sprintf("Sunny, 25°C in %s", params.Location)
	return result, nil
}

func main() {
	pluginapi.ServePlugin(&GetWeatherTool{}, configYAML)
}
