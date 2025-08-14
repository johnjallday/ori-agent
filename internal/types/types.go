package types

import (
	"github.com/johnjallday/dolphin-agent/pluginapi"
	"github.com/openai/openai-go/v2"
)

type Settings struct {
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
}

type LoadedPlugin struct {
	Tool       pluginapi.Tool                 `json:"-"`
	Definition openai.FunctionDefinitionParam `json:"Definition"`
	Path       string                         `json:"Path"`
}

type Agent struct {
	Settings Settings                                 `json:"Settings"`
	Plugins  map[string]LoadedPlugin                  `json:"Plugins"`
	Messages []openai.ChatCompletionMessageParamUnion `json:"-"` // in-memory only
}

type PluginRegistry struct {
	Plugins []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Path        string `json:"path"`
	} `json:"plugins"`
}
